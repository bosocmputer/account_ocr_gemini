package jobs

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/internal/common"
)

func TestCreateAndGet(t *testing.T) {
	reqCtx := common.NewRequestContext("shop-1")
	job, token, err := Create(reqCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
	job, _, _ := Create(reqCtx)

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
	job, _, _ := Create(reqCtx)

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
	job, _, _ := Create(reqCtx)

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
	job, token, err := Create(reqCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	stillProcessing, _, _ := Create(reqCtx)

	finishedRecent, _, _ := Create(reqCtx)
	finishedRecent.Complete("recent")

	finishedOld, _, _ := Create(reqCtx)
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

// resetStoreForTest clears the package-level store so a test can reason
// about exact counts without leftover jobs from other tests in this file
// (store is shared package state, not per-test). Not exported — internal
// test file only.
func resetStoreForTest(t *testing.T) {
	t.Helper()
	storeMutex.Lock()
	store = make(map[string]*Job)
	storeMutex.Unlock()
}

func TestCreateEvictsOldestFinishedJobWhenAtCapacity(t *testing.T) {
	resetStoreForTest(t)
	reqCtx := common.NewRequestContext("shop-1")

	// Fill the store to MaxStoredJobs with already-finished jobs, oldest first.
	var oldestID string
	for i := 0; i < MaxStoredJobs; i++ {
		job, _, err := Create(reqCtx)
		if err != nil {
			t.Fatalf("unexpected error filling store: %v", err)
		}
		job.Complete("done")
		if i == 0 {
			oldestID = job.ID
			// Back-date it so it's unambiguously the oldest by updatedAt.
			job.mu.Lock()
			job.updatedAt = time.Now().Add(-time.Hour)
			job.mu.Unlock()
		}
	}

	storeMutex.RLock()
	countBefore := len(store)
	storeMutex.RUnlock()
	if countBefore != MaxStoredJobs {
		t.Fatalf("expected store to hold exactly %d jobs before the triggering Create, got %d", MaxStoredJobs, countBefore)
	}

	// One more Create should evict the oldest finished job to make room,
	// not reject the submission (there IS something safe to evict).
	newJob, _, err := Create(reqCtx)
	if err != nil {
		t.Fatalf("expected Create to succeed by evicting an old finished job, got error: %v", err)
	}

	storeMutex.RLock()
	_, oldestStillThere := store[oldestID]
	_, newJobThere := store[newJob.ID]
	countAfter := len(store)
	storeMutex.RUnlock()

	if oldestStillThere {
		t.Fatalf("expected the oldest finished job to have been evicted to make room")
	}
	if !newJobThere {
		t.Fatalf("expected the newly created job to be stored")
	}
	if countAfter != MaxStoredJobs {
		t.Fatalf("expected store to stay at cap (%d) after evict+insert, got %d", MaxStoredJobs, countAfter)
	}
}

func TestCreateRejectsWhenAtCapacityAndNothingEvictable(t *testing.T) {
	resetStoreForTest(t)
	reqCtx := common.NewRequestContext("shop-1")

	// Fill the store to MaxStoredJobs with jobs that are all still processing
	// — none of them are safe to evict (would orphan an in-flight AI call).
	for i := 0; i < MaxStoredJobs; i++ {
		if _, _, err := Create(reqCtx); err != nil {
			t.Fatalf("unexpected error filling store: %v", err)
		}
	}

	_, _, err := Create(reqCtx)
	if !errors.Is(err, ErrTooManyActiveJobs) {
		t.Fatalf("expected ErrTooManyActiveJobs when store is full of still-processing jobs, got: %v", err)
	}

	storeMutex.RLock()
	count := len(store)
	storeMutex.RUnlock()
	if count != MaxStoredJobs {
		t.Fatalf("expected store to remain at exactly %d jobs after a rejected Create, got %d", MaxStoredJobs, count)
	}
}
