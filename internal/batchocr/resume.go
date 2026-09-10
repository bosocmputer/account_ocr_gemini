// resume.go - Picks batch runs back up after a process restart (deploy,
// crash, container recycle). This is the whole point of storing batch state
// in MongoDB instead of in-memory: a run that was mid-flight when the
// process died must not be silently abandoned (money was already spent on
// whatever items finished before the crash) or double-processed (money
// spent twice on the ones that hadn't).
package batchocr

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
)

// draining is set during graceful shutdown to tell the in-process worker to
// stop picking up new items. Deliberately process-local rather than a field
// on the run in MongoDB: a shutdown must not leave any durable "stop" mark
// behind, or the next instance would resume the run and immediately stop it
// again. See Drain.
var draining atomic.Bool

func setDraining(v bool) { draining.Store(v) }

// IsDraining reports whether this process is shutting down and should stop
// picking up new batch items.
func IsDraining() bool { return draining.Load() }

// instanceID identifies this process for ClaimOrphanedRuns' ownerinstance
// field. A random-ish value is enough — it only needs to be distinct enough
// across instances for logging/debugging, not cryptographically unique;
// nothing keys off it except log lines and the lease record itself.
var instanceID = "instance-" + time.Now().Format("20060102-150405.000")

// StartResume kicks off a background goroutine that claims and resumes any
// orphaned batch runs (queued/running with an expired or missing lease —
// i.e. from a previous process that died or was redeployed mid-run), then
// keeps rescanning periodically to catch runs orphaned later (e.g. this
// instance itself dies after claiming one, or a sibling instance does).
//
// Must be called after storage.InitMongoDB() and EnsureIndexes(), and must
// never panic or block startup — a batch subsystem failure must not prevent
// the whole API from serving interactive requests, which is why main.go
// only logs (never log.Fatal) if this or EnsureIndexes fails.
func StartResume() {
	go func() {
		resumeOnce()

		leaseTTL := time.Duration(configs.BATCH_OCR_STALE_LEASE_SEC) * time.Second
		interval := leaseTTL / 2
		if interval < time.Minute {
			interval = time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			resumeOnce()
		}
	}()
}

func resumeOnce() {
	leaseTTL := time.Duration(configs.BATCH_OCR_STALE_LEASE_SEC) * time.Second

	claimed, err := ClaimOrphanedRuns(instanceID, leaseTTL)
	if err != nil {
		log.Printf("[batch] resume scan failed: %v", err)
		return
	}
	if len(claimed) == 0 {
		return
	}

	log.Printf("[batch] resume: claimed %d orphaned run(s)", len(claimed))

	for _, run := range claimed {
		resetStuckProcessingItems(run.BatchID)
		go StartBatchRun(run.BatchID)
	}
}

// resetStuckProcessingItems resets any item left in "processing" back to
// "pending" before a resumed run starts working again. An item stuck in
// "processing" when its run was orphaned means the previous process died
// mid-call — the AI cost for that attempt, if any, is already spent and
// unrecoverable, but the document itself was never confirmed written, so it
// must be read again rather than left stranded forever in "processing".
func resetStuckProcessingItems(batchID string) {
	run, err := getByBatchIDOnly(batchID)
	if err != nil || run == nil {
		log.Printf("[batch %s] resume: could not load run to reset stuck items: %v", batchID, err)
		return
	}

	for _, item := range run.Items {
		if item.Status == ItemProcessing {
			if err := IncrementItemAttempts(batchID, item.GuidFixed); err != nil {
				log.Printf("[batch %s] resume: failed to reset stuck item %s: %v", batchID, item.GuidFixed, err)
				continue
			}
			log.Printf("[batch %s] resume: item %s was stuck in processing, reset to pending", batchID, item.GuidFixed)
		}
	}
}

// Drain signals the currently-running batch (if any) to stop picking up new
// items and release its lease, without waiting for any in-flight document
// to finish — used during graceful shutdown so a redeploy doesn't need to
// wait out however long the current document takes, and so the lease is
// freed immediately rather than waiting BATCH_OCR_STALE_LEASE_SEC for
// another instance to notice this one is gone.
//
// This does not (and cannot) interrupt a document actively mid-analysis —
// see RunAnalyzeForBatch/runAnalyzePipeline's own doc comments on why that
// pipeline has no cancellation path, only a wall-clock deadline it checks
// itself. The item that's in flight when shutdown begins will either finish
// and get marked done/failed as normal (if it completes before the process
// actually exits), or be picked up as a stuck "processing" item by the next
// instance's resume scan (if the process exits first).
//
// Takes a context to match main.go's other shutdown steps (srv.Shutdown(ctx))
// even though this function itself never blocks long enough to need one —
// both Mongo calls it makes have their own short internal timeouts.
func Drain(ctx context.Context) {
	dispatchMu.Lock()
	batchID := runningBatchID
	dispatchMu.Unlock()

	if batchID == "" {
		return
	}

	log.Printf("[batch %s] shutdown: pausing run and releasing lease for another instance to resume", batchID)

	// Signal the in-process worker to stop picking up new items via a
	// process-local flag, NOT by setting the run's cancelrequested field in
	// MongoDB. That field means "the user pressed ยกเลิก" and is durable —
	// writing it here would make every graceful shutdown permanently kill
	// the batch it was running: on restart, resume would claim the run,
	// immediately see the leftover flag, and stop again ("stopped early:
	// user_requested" one second after starting). A redeploy must pause a
	// batch, not cancel it.
	setDraining(true)

	// Put the run back to "queued" and expire its lease, so the next
	// instance's resume scan picks it up right away instead of waiting out
	// the full stale-lease window — and so it isn't left in "running" with
	// nothing actually running it.
	if err := requeueForResume(batchID); err != nil {
		log.Printf("[batch %s] shutdown: failed to requeue for resume: %v", batchID, err)
	}
}
