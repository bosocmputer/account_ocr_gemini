package shopbrain

import (
	"context"
	"os"
	"testing"
	"time"
)

// Runs only with SHOPBRAIN_TEST_MONGO_URI set (a throwaway MongoDB), e.g.
// docker run -d -p 127.0.0.1:27099:27017 -e MONGO_INITDB_ROOT_USERNAME=u -e MONGO_INITDB_ROOT_PASSWORD=p mongo:7.0
func TestMongoFeedbackLive(t *testing.T) {
	uri := os.Getenv("SHOPBRAIN_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("SHOPBRAIN_TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	m := NewMongoFeedback(uri, "shopbrain_test_"+time.Now().Format("150405"))
	f := Feedback{ShopID: "S1", Mode: "quiz", JournalGUID: "j2", DocNo: "PV2", Verdict: VerdictCorrect,
		Prediction: PredictionRecord{Known: true, Level: LevelTaxID, Accounts: "11111C,51212D"}, CreatedAt: time.Now().UTC()}
	if err := m.Save(ctx, f); err != nil {
		t.Fatal(err)
	}
	f.ShopID, f.JournalGUID = "S2", "x"
	if err := m.Save(ctx, f); err != nil {
		t.Fatal(err)
	}
	list, err := m.List(ctx, "S1")
	if err != nil || len(list) != 1 || list[0].JournalGUID != "j2" || list[0].Prediction.Level != LevelTaxID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	idx, err := m.coll.Indexes().ListSpecifications(ctx)
	if err != nil || len(idx) != 3 { // _id + 2
		t.Errorf("indexes = %d, %v", len(idx), err)
	}
	_ = m.coll.Database().Drop(ctx)

	// wrong password → unavailable, and no retry storm for 30 s
	bad := NewMongoFeedback("mongodb://u:wrong@127.0.0.1:27099/?authSource=admin", "x")
	start := time.Now()
	if err := bad.Save(ctx, f); err == nil {
		t.Fatal("wrong password must fail")
	}
	if err := bad.Save(ctx, f); err != ErrStoreUnavailable || time.Since(start) > 15*time.Second {
		t.Errorf("second call should fail fast with ErrStoreUnavailable, got %v after %s", err, time.Since(start))
	}
}
