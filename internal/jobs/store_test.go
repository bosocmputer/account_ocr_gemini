package jobs

import (
	"sync"
	"testing"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/internal/common"
)

func TestCreateAndGet(t *testing.T) {
	reqCtx := common.NewRequestContext("shop-1")
	job, token := Create(reqCtx)

	got, ok := Get(job.ID, token)
	if !ok {
		t.Fatalf("expected job to be found with correct token")
	}
	if got.ID != job.ID {
		t.Fatalf("expected job ID %q, got %q", job.ID, got.ID)
	}
}

func TestGetWrongTokenFails(t *testing.T) {
	reqCtx := common.NewRequestContext("shop-1")
	job, _ := Create(reqCtx)

	_, ok := Get(job.ID, "wrong-token")
	if ok {
		t.Fatalf("expected lookup with wrong token to fail")
	}
}

func TestGetUnknownIDFails(t *testing.T) {
	_, ok := Get("does-not-exist", "any-token")
	if ok {
		t.Fatalf("expected lookup of unknown job ID to fail")
	}
}

func TestUpdateProgressThenComplete(t *testing.T) {
	reqCtx := common.NewRequestContext("shop-1")
	job, _ := Create(reqCtx)

	job.UpdateProgress(45, "ocr", "กำลังอ่านข้อความจากเอกสาร")
	snap := job.Snapshot()
	if snap.Status != StatusProcessing {
		t.Fatalf("expected status processing, got %s", snap.Status)
	}
	if snap.Progress.Percent != 45 || snap.Progress.Stage != "ocr" {
		t.Fatalf("unexpected progress snapshot: %+v", snap.Progress)
	}

	job.Complete(map[string]string{"ok": "true"})
	snap = job.Snapshot()
	if snap.Status != StatusCompleted {
		t.Fatalf("expected status completed, got %s", snap.Status)
	}
	if snap.Progress.Percent != 100 {
		t.Fatalf("expected 100%% progress on completion, got %d", snap.Progress.Percent)
	}
	if snap.Result == nil {
		t.Fatalf("expected result to be set")
	}
}

func TestFail(t *testing.T) {
	reqCtx := common.NewRequestContext("shop-1")
	job, _ := Create(reqCtx)

	job.Fail("PROCESSING_TIMEOUT", "receipt too complex")
	snap := job.Snapshot()
	if snap.Status != StatusFailed {
		t.Fatalf("expected status failed, got %s", snap.Status)
	}
	if snap.Error == nil || snap.Error.Code != "PROCESSING_TIMEOUT" {
		t.Fatalf("unexpected error snapshot: %+v", snap.Error)
	}
}

// TestConcurrentAccess exercises the store and a single job under concurrent
// reads/writes — the scenario that matters in production is one background
// goroutine calling UpdateProgress repeatedly while an HTTP polling handler
// calls Snapshot concurrently. Run with -race to catch any lock gaps.
func TestConcurrentAccess(t *testing.T) {
	reqCtx := common.NewRequestContext("shop-1")
	job, token := Create(reqCtx)

	var wg sync.WaitGroup

	// Writer: simulates the background pipeline goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i <= 100; i += 10 {
			job.UpdateProgress(i, "phase", "working")
		}
		job.Complete("done")
	}()

	// Readers: simulate concurrent polling clients.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, ok := Get(job.ID, token); !ok {
					t.Errorf("expected job to remain found during concurrent access")
				}
			}
		}()
	}

	wg.Wait()

	snap := job.Snapshot()
	if snap.Status != StatusCompleted {
		t.Fatalf("expected job to end completed, got %s", snap.Status)
	}
}

func TestSweepEvictsOnlyExpiredFinishedJobs(t *testing.T) {
	reqCtx := common.NewRequestContext("shop-1")

	stillProcessing, _ := Create(reqCtx)

	finishedRecent, _ := Create(reqCtx)
	finishedRecent.Complete("recent")

	finishedOld, _ := Create(reqCtx)
	finishedOld.Complete("old")
	// Force it to look old without waiting JobTTL in a real-time test.
	finishedOld.mu.Lock()
	finishedOld.updatedAt = time.Now().Add(-JobTTL - time.Minute)
	finishedOld.mu.Unlock()

	sweep()

	storeMutex.RLock()
	_, stillThere := store[stillProcessing.ID]
	_, recentThere := store[finishedRecent.ID]
	_, oldThere := store[finishedOld.ID]
	storeMutex.RUnlock()

	if !stillThere {
		t.Fatalf("expected still-processing job to survive sweep")
	}
	if !recentThere {
		t.Fatalf("expected recently-finished job to survive sweep")
	}
	if oldThere {
		t.Fatalf("expected old finished job to be evicted by sweep")
	}
}
