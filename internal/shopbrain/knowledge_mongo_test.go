package shopbrain

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestMongoKnowledgeAndRotation(t *testing.T) {
	uri := os.Getenv("SHOPBRAIN_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("SHOPBRAIN_TEST_MONGO_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := NewMongoFeedback(uri, "shopbrain_test_"+primitive.NewObjectID().Hex())
	db, err := store.database(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()) })
	now := time.Now().UTC()
	row := KnowledgeItem{ID: "one", ShopID: "shopA", Kind: "alias", Data: KnowledgeData{Name: "ชื่อเก่า"}, Active: true, Revision: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: "t", UpdatedBy: "t"}
	if err := store.SaveKnowledge(ctx, nil, row, "create"); err != nil {
		t.Fatal(err)
	}
	next := row
	next.Revision = 2
	next.Data.Name = "ชื่อใหม่"
	if err := store.SaveKnowledge(ctx, &row, next, "update"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveKnowledge(ctx, &row, next, "update"); !errors.Is(err, errKnowledgeConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err := store.KnowledgeHistory(ctx, "other", row.ID); !errors.Is(err, errKnowledgeNotFound) {
		t.Fatalf("read history across shops: %v", err)
	}
	history, err := store.KnowledgeHistory(ctx, row.ShopID, row.ID)
	if err != nil || len(history) != 2 || history[0].Before.Data.Name != row.Data.Name || history[0].After.Data.Name != next.Data.Name {
		t.Fatalf("unexpected history: %+v %v", history, err)
	}

	_, old, _ := NewMCPToken("shopA", "before", now)
	_, fresh, _ := NewMCPToken("shopA", "", now.Add(time.Second))
	if err := store.CreateToken(ctx, old); err != nil {
		t.Fatal(err)
	}
	result, err := store.RotateToken(ctx, "shopA", old.ID, old.Hint, fresh)
	if err != nil || result.Name != old.Name || result.Hint != fresh.Hint {
		t.Fatalf("rotate: %+v %v", result, err)
	}
	if _, err := store.FindToken(ctx, old.Hash); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("old key still works: %v", err)
	}
	if _, err := store.FindToken(ctx, fresh.Hash); err != nil {
		t.Fatalf("new key does not work: %v", err)
	}
	if _, err := store.RotateToken(ctx, "shopA", old.ID, old.Hint, old); !errors.Is(err, errTokenRotationConflict) {
		t.Fatalf("stale rotation: %v", err)
	}
}
