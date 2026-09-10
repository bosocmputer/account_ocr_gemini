package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DocumentImageGroupRef mirrors the fields we need from the main API's
// documentImageGroups collection. Field names/casing and the shape of
// References/ImageReferences were confirmed directly against the dev
// database (Mongo Compass) before writing this — this collection belongs to
// the main API team, not this service, so nothing here is guessed.
type DocumentImageGroupRef struct {
	GuidFixed    string `bson:"guidfixed"`
	ShopID       string `bson:"shopid"`
	TaskGuid     string `bson:"taskguid"`
	Title        string `bson:"title"`
	Status       int    `bson:"status"`
	OcrAnalyzeAI string `bson:"ocranalyzeai"` // field is absent entirely on many docs — zero value "" covers that
	References   []struct {
		GuidFixed string `bson:"guidfixed"`
		Module    string `bson:"module"`
		DocNo     string `bson:"docno"`
	} `bson:"references"`
	ImageReferences []struct {
		XOrder            int    `bson:"xorder"`
		DocumentImageGUID string `bson:"documentimageguid"`
		ImageURI          string `bson:"imageuri"`
		Name              string `bson:"name"`
	} `bson:"imagereferences"`
}

// HasOcrResult reports whether this group has already been read by AI.
// ocranalyzeai can be "" or absent from the document entirely (older docs
// predate this field) — both mean "not yet analyzed". Callers must use this
// helper rather than checking OcrAnalyzeAI == "" directly, so this
// (deliberately loose) definition stays in exactly one place.
func (g DocumentImageGroupRef) HasOcrResult() bool {
	return strings.TrimSpace(g.OcrAnalyzeAI) != ""
}

// IsAlreadyRecorded reports whether this group has already been saved as a
// journal entry (references is populated). This can be true even when
// HasOcrResult() is false — an accountant can key a document in manually
// without ever running it through AI — so both checks are needed to decide
// whether a batch run should skip a document.
func (g DocumentImageGroupRef) IsAlreadyRecorded() bool {
	return len(g.References) > 0
}

// ListEligibleGroupsForTask returns every documentImageGroups document for a
// task that has status 1 ("ผ่าน"/approved), without filtering on
// ocranalyzeai/references — callers filter those themselves so they can
// count how many were skipped and why (see internal/api's batch-ocr preview
// endpoint).
//
// documentImageGroups has no index beyond the default _id_ (confirmed via
// getIndexes() against the dev database), so this query is always a full
// collection scan. We don't create an index here — this collection belongs
// to the main API team — so a longer timeout than the rest of this file's
// 5s default is used deliberately.
func ListEligibleGroupsForTask(shopID, taskGuid string) ([]DocumentImageGroupRef, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	filter := bson.M{
		"shopid":   shopID,
		"taskguid": taskGuid,
		"status":   1,
	}
	projection := bson.M{
		"guidfixed":       1,
		"shopid":          1,
		"taskguid":        1,
		"title":           1,
		"status":          1,
		"ocranalyzeai":    1,
		"references":      1,
		"imagereferences": 1,
	}
	// Sort by _id, not xorder — xorder only exists at the imagereferences
	// level in this schema, not on the group document itself.
	opts := options.Find().SetProjection(projection).SetSort(bson.D{{Key: "_id", Value: 1}})

	collection := mongoDB.Collection(configs.DOCUMENT_IMAGE_GROUP_COLLECTION)
	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", configs.DOCUMENT_IMAGE_GROUP_COLLECTION, err)
	}
	defer cursor.Close(ctx)

	var groups []DocumentImageGroupRef
	if err = cursor.All(ctx, &groups); err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", configs.DOCUMENT_IMAGE_GROUP_COLLECTION, err)
	}

	return groups, nil
}

// RefreshGroupStates re-reads ocranalyzeai/references for a batch of groups
// in a single query, keyed by guidfixed for O(1) lookup.
//
// This exists specifically so the batch worker never has to look up one
// group at a time: documentImageGroups has no index (see
// ListEligibleGroupsForTask's comment above), so a per-document recheck
// would be one full collection scan per document — 100 scans for a
// 100-document batch, against a collection shared with the main API team.
// Batching guids into one $in query cuts that down to roughly one scan per
// 10 documents (see internal/batchocr's worker, which calls this
// periodically rather than per item). Do not "simplify" this back into a
// per-item lookup.
func RefreshGroupStates(shopID, taskGuid string, guids []string) (map[string]DocumentImageGroupRef, error) {
	result := make(map[string]DocumentImageGroupRef, len(guids))
	if len(guids) == 0 {
		return result, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	filter := bson.M{
		"shopid":    shopID,
		"taskguid":  taskGuid,
		"guidfixed": bson.M{"$in": guids},
	}
	projection := bson.M{
		"guidfixed":    1,
		"ocranalyzeai": 1,
		"references":   1,
	}

	collection := mongoDB.Collection(configs.DOCUMENT_IMAGE_GROUP_COLLECTION)
	cursor, err := collection.Find(ctx, filter, options.Find().SetProjection(projection))
	if err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", configs.DOCUMENT_IMAGE_GROUP_COLLECTION, err)
	}
	defer cursor.Close(ctx)

	var groups []DocumentImageGroupRef
	if err = cursor.All(ctx, &groups); err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", configs.DOCUMENT_IMAGE_GROUP_COLLECTION, err)
	}

	for _, g := range groups {
		result[g.GuidFixed] = g
	}
	return result, nil
}

// SetGroupOcrAnalyzeAI writes a batch-produced OCR/accounting result back
// onto a documentImageGroups document, returning whether the write actually
// happened.
//
// The filter matches ocranalyzeai being "", absent entirely, or explicitly
// null — all three mean "not yet analyzed" in this collection (confirmed:
// many real documents simply don't have the field at all, they are not
// guaranteed to have it set to ""). Filtering on just `ocranalyzeai: ""`
// would silently fail to match those documents: MatchedCount would come
// back 0, the caller would treat that as "someone already wrote a result"
// and mark the item skipped, and the AI cost for that document would have
// been paid for nothing. Do not narrow this filter to a single equality
// check.
//
// This also doubles as the race-condition guard against a user manually
// clicking "AI วิเคราะห์" on the same document while a batch run is also
// working through it (a real scenario — batches can run for hours while the
// task stays open): whichever write reaches Mongo first wins, and the
// loser's MatchedCount comes back 0 without touching the document.
//
// No upsert: if the document has been deleted, we must not resurrect it as
// a bare {shopid, guidfixed, ocranalyzeai} stub.
//
// This is the first UpdateOne (rather than Find/FindOne) in this package —
// both shopid and the ocranalyzeai condition must stay in the filter, not
// just guidfixed, so a mismatch can only mean "someone else already wrote a
// result" or "wrong shop", never "field name typo matched everything".
func SetGroupOcrAnalyzeAI(shopID, guidfixed, ocrJSON string) (written bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	collection := mongoDB.Collection(configs.DOCUMENT_IMAGE_GROUP_COLLECTION)

	filter := bson.M{
		"shopid":    shopID,
		"guidfixed": guidfixed,
		"$or": []bson.M{
			{"ocranalyzeai": ""},
			{"ocranalyzeai": bson.M{"$exists": false}},
			{"ocranalyzeai": nil},
		},
	}
	update := bson.M{"$set": bson.M{"ocranalyzeai": ocrJSON}}

	res, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("failed to update %s: %w", configs.DOCUMENT_IMAGE_GROUP_COLLECTION, err)
	}

	if res.MatchedCount > 0 {
		return true, nil
	}

	// MatchedCount == 0: either someone else already wrote a result (fine,
	// not an error — the caller should mark this item skipped), or the
	// document was deleted out from under us (an actual error condition).
	existsCtx, existsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer existsCancel()
	count, countErr := collection.CountDocuments(existsCtx, bson.M{"shopid": shopID, "guidfixed": guidfixed})
	if countErr != nil {
		return false, fmt.Errorf("failed to verify document existence in %s: %w", configs.DOCUMENT_IMAGE_GROUP_COLLECTION, countErr)
	}
	if count == 0 {
		return false, fmt.Errorf("document %s not found in %s (deleted?)", guidfixed, configs.DOCUMENT_IMAGE_GROUP_COLLECTION)
	}

	return false, nil
}
