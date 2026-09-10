package batchocr

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
)

const testShopID = "36xq3C3RKkSrkcCJNj6lnjfBl6Z"

func setupLiveMongoTest(t *testing.T) {
	t.Helper()
	godotenv.Load("../../.env")
	if os.Getenv("MONGO_URI") == "" {
		t.Skip("MONGO_URI not set — skipping live-DB integration test")
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")
	if configs.BATCH_OCR_COLLECTION == "" {
		configs.BATCH_OCR_COLLECTION = "batch_ocr_runs_test" // isolated from any real run data
	}
	if err := storage.InitMongoDB(); err != nil {
		t.Fatalf("mongo connect failed: %v", err)
	}
	t.Cleanup(storage.CloseMongoDB)

	if err := EnsureIndexes(); err != nil {
		t.Fatalf("EnsureIndexes failed: %v", err)
	}
}

// newTestBatchID returns a unique id so parallel/repeated test runs never
// collide on the unique batchid index, and deletes the run it creates
// afterward — this collection is ours, so tests clean up by deleting rather
// than restoring prior state (contrast with documentimagegroup_test.go,
// which must restore state because that collection belongs to another team).
func newTestBatchID(t *testing.T) string {
	t.Helper()
	id := "test-" + uuid.New().String()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = collection().DeleteOne(ctx, bson.M{"batchid": id})
	})
	return id
}

func TestCreate_GetByID_FullLifecycle(t *testing.T) {
	setupLiveMongoTest(t)
	batchID := newTestBatchID(t)

	items := []BatchItem{
		{GuidFixed: "g1", Title: "doc1", Status: ItemPending},
		{GuidFixed: "g2", Title: "doc2", Status: ItemPending},
	}

	run, err := Create(batchID, testShopID, "task-fixture", "gemini", "tester", items, 0.60)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if run.Status != StatusQueued {
		t.Errorf("expected new run status %q, got %q", StatusQueued, run.Status)
	}
	if run.Total != 2 {
		t.Errorf("expected Total=2, got %d", run.Total)
	}

	fetched, err := GetByID(batchID, testShopID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if fetched == nil {
		t.Fatal("expected to find the run just created")
	}
	if len(fetched.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(fetched.Items))
	}

	// GetByID must be shop-scoped: fetching with the wrong shopid must not
	// leak the run to a caller who merely guessed the batchid.
	wrongShop, err := GetByID(batchID, "some-other-shop")
	if err != nil {
		t.Fatalf("GetByID with wrong shop failed: %v", err)
	}
	if wrongShop != nil {
		t.Error("expected GetByID with wrong shopid to return nil, not the run")
	}

	// --- item transitions ---
	if err := MarkItemProcessing(batchID, "g1"); err != nil {
		t.Fatalf("MarkItemProcessing failed: %v", err)
	}
	if err := MarkItemDone(batchID, "g1"); err != nil {
		t.Fatalf("MarkItemDone failed: %v", err)
	}
	if err := AddCost(batchID, 0.35); err != nil {
		t.Fatalf("AddCost failed: %v", err)
	}
	if err := MarkItemFailed(batchID, "g2", "download_failed", "boom"); err != nil {
		t.Fatalf("MarkItemFailed failed: %v", err)
	}

	fetched, err = GetByID(batchID, testShopID)
	if err != nil {
		t.Fatalf("GetByID after transitions failed: %v", err)
	}
	byGuid := map[string]BatchItem{}
	for _, it := range fetched.Items {
		byGuid[it.GuidFixed] = it
	}
	if byGuid["g1"].Status != ItemDone {
		t.Errorf("expected g1 status %q, got %q", ItemDone, byGuid["g1"].Status)
	}
	if byGuid["g2"].Status != ItemFailed {
		t.Errorf("expected g2 status %q, got %q", ItemFailed, byGuid["g2"].Status)
	}
	if byGuid["g2"].ErrorCode != "download_failed" {
		t.Errorf("expected g2 errorcode %q, got %q", "download_failed", byGuid["g2"].ErrorCode)
	}
	if fetched.TotalCostTHB != 0.35 {
		t.Errorf("expected TotalCostTHB=0.35, got %v", fetched.TotalCostTHB)
	}

	// --- reset for retry ---
	n, err := ResetItemsForRetry(batchID)
	if err != nil {
		t.Fatalf("ResetItemsForRetry failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected ResetItemsForRetry to reset 1 item, got %d", n)
	}
	fetched, _ = GetByID(batchID, testShopID)
	for _, it := range fetched.Items {
		if it.GuidFixed == "g2" && it.Status != ItemPending {
			t.Errorf("expected g2 status %q after retry reset, got %q", ItemPending, it.Status)
		}
		if it.GuidFixed == "g1" && it.Status != ItemDone {
			t.Errorf("expected g1 (done) to be untouched by ResetItemsForRetry, got %q", it.Status)
		}
	}

	// --- cancel flag ---
	cancelled, err := IsCancelRequested(batchID)
	if err != nil {
		t.Fatalf("IsCancelRequested failed: %v", err)
	}
	if cancelled {
		t.Error("expected cancelrequested to start false")
	}
	if err := RequestCancel(batchID); err != nil {
		t.Fatalf("RequestCancel failed: %v", err)
	}
	cancelled, err = IsCancelRequested(batchID)
	if err != nil {
		t.Fatalf("IsCancelRequested after RequestCancel failed: %v", err)
	}
	if !cancelled {
		t.Error("expected cancelrequested true after RequestCancel")
	}

	// --- finish ---
	if err := FinishRun(batchID, StatusCancelled, "user_requested"); err != nil {
		t.Fatalf("FinishRun failed: %v", err)
	}
	fetched, _ = GetByID(batchID, testShopID)
	if fetched.Status != StatusCancelled {
		t.Errorf("expected final status %q, got %q", StatusCancelled, fetched.Status)
	}
	if fetched.CancelReason != "user_requested" {
		t.Errorf("expected cancelreason %q, got %q", "user_requested", fetched.CancelReason)
	}
	if fetched.FinishedAt == nil {
		t.Error("expected FinishedAt to be set after FinishRun")
	}
}

// TestMarkRunning_TransitionsFromQueued exercises the exact gap that made a
// freshly-submitted run show status "queued" for its entire duration: a run
// created via Create() starts "queued" and previously only ever became
// "running" via ClaimOrphanedRuns — which the direct
// SubmitBatchOcrHandler → StartBatchRun path never goes through. markRunning
// is what the worker now calls at the start of runBatch to fix that.
func TestMarkRunning_TransitionsFromQueued(t *testing.T) {
	setupLiveMongoTest(t)
	batchID := newTestBatchID(t)

	run, err := Create(batchID, testShopID, "task-fixture", "gemini", "tester",
		[]BatchItem{{GuidFixed: "g1", Status: ItemPending}}, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if run.Status != StatusQueued {
		t.Fatalf("expected new run status %q, got %q", StatusQueued, run.Status)
	}

	if err := markRunning(batchID, "test-instance", 5*time.Minute); err != nil {
		t.Fatalf("markRunning failed: %v", err)
	}

	fetched, err := GetByID(batchID, testShopID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if fetched.Status != StatusRunning {
		t.Errorf("expected status %q after markRunning, got %q", StatusRunning, fetched.Status)
	}
	if fetched.OwnerInstance != "test-instance" {
		t.Errorf("expected ownerinstance %q, got %q", "test-instance", fetched.OwnerInstance)
	}
	if !fetched.LeaseExpiresAt.After(time.Now()) {
		t.Errorf("expected leaseexpiresat to be in the future after markRunning, got %v", fetched.LeaseExpiresAt)
	}
}

func TestFindActiveByTask(t *testing.T) {
	setupLiveMongoTest(t)
	batchID := newTestBatchID(t)
	taskGuid := "task-" + uuid.New().String()

	active, err := FindActiveByTask(testShopID, taskGuid)
	if err != nil {
		t.Fatalf("FindActiveByTask failed: %v", err)
	}
	if active != nil {
		t.Fatal("expected no active run before one is created")
	}

	if _, err := Create(batchID, testShopID, taskGuid, "gemini", "tester",
		[]BatchItem{{GuidFixed: "g1", Status: ItemPending}}, 0.30); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	active, err = FindActiveByTask(testShopID, taskGuid)
	if err != nil {
		t.Fatalf("FindActiveByTask failed: %v", err)
	}
	if active == nil || active.BatchID != batchID {
		t.Fatalf("expected to find the just-created run as active, got %+v", active)
	}

	if err := FinishRun(batchID, StatusCompleted, ""); err != nil {
		t.Fatalf("FinishRun failed: %v", err)
	}

	active, err = FindActiveByTask(testShopID, taskGuid)
	if err != nil {
		t.Fatalf("FindActiveByTask after finish failed: %v", err)
	}
	if active != nil {
		t.Error("expected no active run once the only run has completed")
	}
}

func TestCreate_RejectsOverHardCap(t *testing.T) {
	setupLiveMongoTest(t)
	batchID := newTestBatchID(t)

	items := make([]BatchItem, maxItemsHardCap+1)
	for i := range items {
		items[i] = BatchItem{GuidFixed: fmt.Sprintf("g%d", i), Status: ItemPending}
	}

	_, err := Create(batchID, testShopID, "task-fixture", "gemini", "tester", items, 0)
	if err == nil {
		t.Fatal("expected Create to reject a batch over the hard cap")
	}
}

// TestClaimOrphanedRuns_ConcurrentCallersGetDisjointRuns is the plan's
// specifically-required check: if two instances both scan for orphaned runs
// at the same moment, each claimed run must go to exactly one caller. A
// find-then-update implementation would let both callers see the same
// candidate and both "win" it, meaning the same documents get sent to the AI
// provider twice — this is the whole reason ClaimOrphanedRuns must use
// FindOneAndUpdate.
func TestClaimOrphanedRuns_ConcurrentCallersGetDisjointRuns(t *testing.T) {
	setupLiveMongoTest(t)

	const numRuns = 10
	batchIDs := make([]string, numRuns)
	for i := 0; i < numRuns; i++ {
		batchIDs[i] = newTestBatchID(t)
		run, err := Create(batchIDs[i], testShopID, "task-fixture", "gemini", "tester",
			[]BatchItem{{GuidFixed: "g1", Status: ItemPending}}, 0)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		// Force these into the "orphaned" window: queued/running with an
		// expired (or absent) lease is exactly what ClaimOrphanedRuns scans
		// for. Create() leaves LeaseExpiresAt at its zero value, which is
		// already "before now", so no extra setup is needed here — but make
		// that explicit so this test doesn't silently stop testing anything
		// if Create's zero-value behavior ever changes.
		if !run.LeaseExpiresAt.Before(time.Now()) {
			t.Fatalf("test assumption violated: new run's lease is not already expired (%v)", run.LeaseExpiresAt)
		}
	}

	const numCallers = 4
	var wg sync.WaitGroup
	claimedBy := make([][]BatchRun, numCallers)
	errs := make([]error, numCallers)

	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			claimed, err := ClaimOrphanedRuns(fmt.Sprintf("instance-%d", idx), 5*time.Minute)
			claimedBy[idx] = claimed
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	seen := map[string]int{}
	totalClaimed := 0
	for i, claimed := range claimedBy {
		if errs[i] != nil {
			t.Fatalf("caller %d: ClaimOrphanedRuns failed: %v", i, errs[i])
		}
		for _, run := range claimed {
			seen[run.BatchID]++
			totalClaimed++
		}
	}

	if totalClaimed != numRuns {
		t.Errorf("expected %d total runs claimed across all callers, got %d", numRuns, totalClaimed)
	}
	for batchID, count := range seen {
		if count != 1 {
			t.Errorf("run %s was claimed %d times (expected exactly 1) — two instances would both process it and pay for AI twice", batchID, count)
		}
	}
	for _, id := range batchIDs {
		if seen[id] != 1 {
			t.Errorf("run %s was never claimed by any caller", id)
		}
	}
}

func TestClaimOrphanedRuns_DoesNotClaimFreshLease(t *testing.T) {
	setupLiveMongoTest(t)
	batchID := newTestBatchID(t)

	if _, err := Create(batchID, testShopID, "task-fixture", "gemini", "tester",
		[]BatchItem{{GuidFixed: "g1", Status: ItemPending}}, 0); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// Give it a healthy, far-future lease as if an active worker just sent a
	// heartbeat for it.
	if err := Heartbeat(batchID, 15*time.Minute); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	claimed, err := ClaimOrphanedRuns("some-other-instance", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimOrphanedRuns failed: %v", err)
	}
	for _, run := range claimed {
		if run.BatchID == batchID {
			t.Error("expected a run with a fresh, unexpired lease to NOT be claimable as orphaned")
		}
	}
}
