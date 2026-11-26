package shopbrain

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const knowledgeCollection = "shop_knowledge"
const knowledgeHistoryCollection = "shop_knowledge_history"

var errKnowledgeNotFound = errors.New("shop knowledge not found")
var errKnowledgeConflict = errors.New("shop knowledge revision conflict")

type KnowledgeHistory struct {
	ID          string         `json:"id" bson:"_id"`
	KnowledgeID string         `json:"knowledge_id" bson:"knowledge_id"`
	ShopID      string         `json:"shopid" bson:"shopid"`
	Action      string         `json:"action" bson:"action"`
	Before      *KnowledgeItem `json:"before,omitempty" bson:"before,omitempty"`
	After       *KnowledgeItem `json:"after,omitempty" bson:"after,omitempty"`
	By          string         `json:"by" bson:"by"`
	At          time.Time      `json:"at" bson:"at"`
}

type KnowledgeStore interface {
	ListKnowledge(ctx context.Context, shopID string) ([]KnowledgeItem, error)
	GetKnowledge(ctx context.Context, shopID, id string) (*KnowledgeItem, error)
	SaveKnowledge(ctx context.Context, before *KnowledgeItem, after KnowledgeItem, action string) error
	KnowledgeHistory(ctx context.Context, shopID, id string) ([]KnowledgeHistory, error)
}

type knowledgeDocument struct {
	KnowledgeItem  `bson:",inline"`
	PendingHistory *KnowledgeHistory `bson:"pending_history,omitempty"`
}

func knowledgeFilter(shopID, id string) bson.M {
	return bson.M{"shopid": shopID, "_id": id}
}

func (m *MongoFeedback) ListKnowledge(ctx context.Context, shopID string) ([]KnowledgeItem, error) {
	db, err := m.database(ctx)
	if err != nil {
		return nil, err
	}
	cursor, err := db.Collection(knowledgeCollection).Find(ctx, bson.M{"shopid": shopID}, options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}, {Key: "_id", Value: 1}}).SetProjection(bson.M{"pending_history": 0}))
	if err != nil {
		return nil, err
	}
	rows := []KnowledgeItem{}
	err = cursor.All(ctx, &rows)
	return rows, err
}

func readKnowledge(ctx context.Context, db *mongo.Database, shopID, id string) (knowledgeDocument, error) {
	var doc knowledgeDocument
	err := db.Collection(knowledgeCollection).FindOne(ctx, knowledgeFilter(shopID, id)).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = errKnowledgeNotFound
	}
	return doc, err
}

func (m *MongoFeedback) GetKnowledge(ctx context.Context, shopID, id string) (*KnowledgeItem, error) {
	db, err := m.database(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := readKnowledge(ctx, db, shopID, id)
	return &doc.KnowledgeItem, err
}

// The current value and its audit entry are committed in the same Mongo
// document. Publishing the audit entry to the history collection is
// idempotent, so this also works on standalone Mongo without transactions.
func publishKnowledgeHistory(ctx context.Context, db *mongo.Database, event *KnowledgeHistory) error {
	if event == nil {
		return nil
	}
	_, err := db.Collection(knowledgeHistoryCollection).UpdateOne(ctx, knowledgeFilter(event.ShopID, event.ID), bson.M{"$setOnInsert": event}, options.Update().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return err
	}
	filter := knowledgeFilter(event.ShopID, event.KnowledgeID)
	filter["pending_history._id"] = event.ID
	_, err = db.Collection(knowledgeCollection).UpdateOne(ctx, filter, bson.M{"$unset": bson.M{"pending_history": ""}})
	return err
}

func (m *MongoFeedback) SaveKnowledge(ctx context.Context, before *KnowledgeItem, after KnowledgeItem, action string) error {
	db, err := m.database(ctx)
	if err != nil {
		return err
	}
	event := &KnowledgeHistory{ID: fmt.Sprintf("%s:%d", after.ID, after.Revision), KnowledgeID: after.ID, ShopID: after.ShopID, Action: action, Before: before, After: &after, By: after.UpdatedBy, At: after.UpdatedAt}
	row := knowledgeDocument{KnowledgeItem: after, PendingHistory: event}
	current := db.Collection(knowledgeCollection)
	if before == nil {
		_, err = current.InsertOne(ctx, row)
	} else {
		prior, readErr := readKnowledge(ctx, db, after.ShopID, after.ID)
		if readErr != nil {
			return readErr
		}
		if prior.Revision != before.Revision {
			return errKnowledgeConflict
		}
		if err := publishKnowledgeHistory(ctx, db, prior.PendingHistory); err != nil {
			return err
		}
		filter := knowledgeFilter(after.ShopID, after.ID)
		filter["revision"] = before.Revision
		var result *mongo.UpdateResult
		result, err = current.ReplaceOne(ctx, filter, row)
		if err == nil && result.MatchedCount != 1 {
			return errKnowledgeConflict
		}
	}
	if err != nil {
		return err
	}
	if err := publishKnowledgeHistory(ctx, db, event); err != nil {
		// The event remains attached to the committed revision for retry.
		log.Print("shopbrain: knowledge history projection pending")
	}
	return nil
}

func (m *MongoFeedback) KnowledgeHistory(ctx context.Context, shopID, id string) ([]KnowledgeHistory, error) {
	db, err := m.database(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := readKnowledge(ctx, db, shopID, id)
	if err != nil {
		return nil, err
	}
	if err := publishKnowledgeHistory(ctx, db, doc.PendingHistory); err != nil {
		return nil, err
	}
	cursor, err := db.Collection(knowledgeHistoryCollection).Find(ctx, bson.M{"shopid": shopID, "knowledge_id": id}, options.Find().SetSort(bson.D{{Key: "after.revision", Value: -1}}))
	if err != nil {
		return nil, err
	}
	rows := []KnowledgeHistory{}
	err = cursor.All(ctx, &rows)
	return rows, err
}
