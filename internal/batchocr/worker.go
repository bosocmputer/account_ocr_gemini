// worker.go - Runs one batch OCR run to completion.
//
// Throughput here is bounded by internal/ratelimit's global Gemini/Mistral
// token bucket (~12 calls/min for the whole process), not by
// BATCH_OCR_CONCURRENCY. Raising BATCH_OCR_CONCURRENCY does not make a batch
// finish faster — every worker still queues on the same shared limiter — it
// only means more goroutines competing to burn through that same shared
// budget sooner, which takes it away from interactive users faster. Keep
// concurrency low; BATCH_OCR_ITEM_DELAY_MS is what actually protects
// interactive users, not concurrency.
package batchocr

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/api"
	"github.com/bosocmputer/account_ocr_gemini/internal/jobs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// taskStatusChecker abstracts looking up a task's status so this package
// doesn't hard-depend on exactly how/where that's stored. See
// checkTaskOpen's doc comment — this returns (isOpen, checked, err): checked
// is false when the caller should treat the check as inconclusive (task
// collection/field not confirmed) rather than either open or closed.
type taskStatusChecker func(shopID, taskGuid string) (isOpen bool, checked bool, err error)

// taskClosedStatus is the task.status value that means "ปิดงาน" (closed),
// confirmed against src/views/pages/accounting/JournalFromImageDetail.vue's
// `isJobClosed = taskData.status === 4`, and cross-checked directly against
// MongoDB's "tasks" collection (confirmed collection name — status 4 exists
// there for 5 real tasks in the fixture shop, so this is a real, reachable
// value, not a guess).
const taskClosedStatus = 4

// checkTaskIsOpen looks up a task's status directly against the "tasks"
// collection this service does not otherwise touch. Returns checked=false
// (never an error) if the collection or the expected field isn't there —
// callers must treat that as "can't tell, don't block on it", per the plan:
// this feature must degrade to a no-op rather than fail the whole batch if
// the main API ever changes this collection's shape, since the user can
// always cancel a batch manually regardless.
func checkTaskIsOpen(shopID, taskGuid string) (isOpen bool, checked bool, err error) {
	db := storage.GetMongoDB()
	if db == nil {
		return true, false, nil
	}

	var doc struct {
		Status int32 `bson:"status"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	findErr := db.Collection("tasks").FindOne(ctx, bson.M{"shopid": shopID, "guidfixed": taskGuid}).Decode(&doc)
	if findErr != nil {
		// Not found, wrong field name, or a transient Mongo error — any of
		// these means "we can't tell", not "the task is closed". Log once so
		// this isn't silently invisible if the collection shape does change,
		// but don't fail the run over it.
		log.Printf("[batch] task status check inconclusive for shopid=%s taskguid=%s: %v", shopID, taskGuid, findErr)
		return true, false, nil
	}

	return doc.Status != taskClosedStatus, true, nil
}

// activeChecker is swappable in tests; production always uses checkTaskIsOpen.
var activeChecker taskStatusChecker = checkTaskIsOpen

// runningBatches serializes actual work to at most one batch at a time
// process-wide — additional runs sit in "queued" until the current one
// finishes, because the AI rate limiter is the real bottleneck regardless of
// how many runs claim to be "running" simultaneously. dispatchMu guards
// against two goroutines both deciding they're the one to start the next
// queued run.
var (
	dispatchMu     sync.Mutex
	runningBatchID string
)

// RecheckIntervalItems controls how often (in items processed) the worker
// re-reads task status and document eligibility from MongoDB, per
// plan-clever-lemon.md TODO-6 step 2/4: both checks are folded into the same
// periodic query rather than done per-item, because documentImageGroups has
// no index beyond the default _id_ (confirmed against the dev database) and
// every query against it is a full collection scan. Checking every item
// would mean one collection scan per document; checking every 10 means one
// per 10.
const RecheckIntervalItems = 10

// StartBatchRun begins processing a batch run's items until completion,
// cancellation, or the task closing. Intended to be launched with `go
// StartBatchRun(...)` by whatever kicks off (or resumes) a run —
// SubmitBatchOcrHandler and StartResume both call this the same way.
//
// Only one run is actually worked at a time (see runningBatches above); if
// another run is already in progress, this returns immediately and the
// caller is expected to leave the new run in "queued" for a later call to
// pick up (see maybeDispatchNext, called whenever a run finishes).
func StartBatchRun(batchID string) {
	dispatchMu.Lock()
	if runningBatchID != "" {
		dispatchMu.Unlock()
		return // something else is already running; this one stays queued
	}
	runningBatchID = batchID
	dispatchMu.Unlock()

	defer func() {
		dispatchMu.Lock()
		runningBatchID = ""
		dispatchMu.Unlock()
		maybeDispatchNext()
	}()

	runBatch(batchID)
}

// maybeDispatchNext looks for the oldest queued run process-wide and starts
// it, if nothing is currently running. Called after every run finishes, and
// from StartResume at startup.
func maybeDispatchNext() {
	dispatchMu.Lock()
	busy := runningBatchID != ""
	dispatchMu.Unlock()
	if busy {
		return
	}

	next, err := findOldestQueuedRun()
	if err != nil {
		log.Printf("[batch] failed to look for next queued run: %v", err)
		return
	}
	if next == nil {
		return
	}
	go StartBatchRun(next.BatchID)
}

func runBatch(batchID string) {
	run, err := getByBatchIDOnly(batchID)
	if err != nil || run == nil {
		log.Printf("[batch %s] could not load run to start: %v", batchID, err)
		return
	}

	log.Printf("[batch %s] starting — shopid=%s taskguid=%s model=%s total=%d", batchID, run.ShopID, run.TaskGuid, run.Model, run.Total)

	// Mark running explicitly: a run reaches this function either freshly
	// created (still "queued" — ClaimOrphanedRuns never touched it) or via
	// resume (already "running" from ClaimOrphanedRuns, but this is a cheap,
	// idempotent no-op either way). Without this, a run started directly by
	// SubmitBatchOcrHandler would show "queued" to every poller for its
	// entire duration.
	leaseTTL := time.Duration(configs.BATCH_OCR_STALE_LEASE_SEC) * time.Second
	if err := markRunning(batchID, instanceID, leaseTTL); err != nil {
		log.Printf("[batch %s] failed to mark running: %v", batchID, err)
	}

	masterCache, err := storage.GetOrLoadMasterData(run.ShopID)
	if err != nil {
		log.Printf("[batch %s] failed to load master data, aborting run: %v", batchID, err)
		_ = FinishRun(batchID, StatusCancelled, "master_data_load_failed")
		return
	}
	documentTemplates := masterCache.DocumentTemplates

	heartbeatStop := make(chan struct{})
	go runHeartbeat(batchID, heartbeatStop)
	defer close(heartbeatStop)

	concurrency := configs.BATCH_OCR_CONCURRENCY
	if concurrency < 1 {
		concurrency = 1
	}

	itemGuids := make([]string, len(run.Items))
	for i, it := range run.Items {
		itemGuids[i] = it.GuidFixed
	}
	imageRefsByGuid, err := loadImageReferences(run.ShopID, run.TaskGuid, itemGuids)
	if err != nil {
		log.Printf("[batch %s] failed to load image references, aborting run: %v", batchID, err)
		_ = FinishRun(batchID, StatusCancelled, "load_failed")
		return
	}

	itemsChan := make(chan string, len(itemGuids))
	var wg sync.WaitGroup
	var processedCount int64
	var countMu sync.Mutex
	stopEarly := make(chan struct{})
	var stopReason string // "user_requested" or "task_closed" — set exactly once, guarded by stopOnce
	var stopOnce sync.Once

	triggerStop := func(reason string) {
		stopOnce.Do(func() {
			stopReason = reason
			close(stopEarly)
		})
	}

	// Shared snapshot of which documents have since been analyzed or saved
	// by someone else, refreshed on the RecheckIntervalItems cadence rather
	// than per item (documentImageGroups has no index beyond _id_, so each
	// refresh is a full collection scan — one per 10 documents instead of
	// one per document).
	//
	// This is purely a cost optimization, NOT the race guard: the real,
	// atomic guard is the $or filter inside storage.SetGroupOcrAnalyzeAI,
	// which cannot write over a result someone else already produced. What
	// this buys is not paying Gemini/Mistral to analyze a document we could
	// have known was already done — without it, a user manually analyzing a
	// document that's queued behind a running batch means the batch pays
	// full price for that document and then throws the answer away.
	var eligibilityMu sync.RWMutex
	eligibility := make(map[string]storage.DocumentImageGroupRef)

	refreshEligibility := func() {
		states, err := storage.RefreshGroupStates(run.ShopID, run.TaskGuid, itemGuids)
		if err != nil {
			// Non-fatal: on failure we simply don't skip anything this
			// round and fall back to SetGroupOcrAnalyzeAI's own guard.
			log.Printf("[batch %s] eligibility refresh failed (continuing without it): %v", batchID, err)
			return
		}
		eligibilityMu.Lock()
		eligibility = states
		eligibilityMu.Unlock()
	}

	alreadyHandled := func(guidfixed string) bool {
		eligibilityMu.RLock()
		defer eligibilityMu.RUnlock()
		g, ok := eligibility[guidfixed]
		if !ok {
			return false // not in the snapshot (or refresh failed) — let it through
		}
		return g.HasOcrResult() || g.IsAlreadyRecorded()
	}

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for guidfixed := range itemsChan {
				select {
				case <-stopEarly:
					return
				default:
				}

				countMu.Lock()
				processedCount++
				n := processedCount
				countMu.Unlock()

				// This process is shutting down: stop taking new items and
				// leave the rest for whichever instance resumes this run.
				// Distinct from a user cancel — nothing durable is written,
				// so the run stays resumable (see Drain).
				if IsDraining() {
					triggerStop("draining")
					return
				}

				// Cancel is checked before EVERY item, not on the 10-item
				// cadence below: it's an indexed point lookup on our own
				// batch_ocr_runs collection (unique batchid index), so it
				// costs nothing worth batching — and a user who presses
				// "ยกเลิก" and then watches up to 10 more documents get
				// read anyway (~2-3 minutes, and real money per document)
				// would reasonably conclude the button is broken.
				if cancelled, _ := IsCancelRequested(batchID); cancelled {
					triggerStop("user_requested")
					return
				}

				// Task-status and eligibility both stay on the 10-item
				// cadence: unlike the cancel flag, these query collections
				// owned by another team (and documentImageGroups has no
				// usable index), so they're deliberately batched.
				if n%RecheckIntervalItems == 1 { // check on the 1st, 11th, 21st... item of this run
					if isOpen, checked, err := activeChecker(run.ShopID, run.TaskGuid); checked && err == nil && !isOpen {
						log.Printf("[batch %s] task closed mid-run — stopping", batchID)
						triggerStop("task_closed")
						return
					}
					refreshEligibility()
				}

				// Skip without paying for AI if someone analyzed or saved
				// this document while it sat in our queue.
				if alreadyHandled(guidfixed) {
					_ = MarkItemSkipped(batchID, guidfixed)
					log.Printf("[batch %s] item %s → skipped (pre-check) already analyzed or saved by someone else", batchID, guidfixed)
					continue
				}

				processItem(batchID, run.ShopID, run.Model, guidfixed, imageRefsByGuid[guidfixed], masterCache, documentTemplates)

				time.Sleep(time.Duration(configs.BATCH_OCR_ITEM_DELAY_MS) * time.Millisecond)
			}
		}()
	}

	// Only enqueue items that are still pending — resume can call this with
	// a run whose earlier items are already done/skipped/failed.
	for _, it := range run.Items {
		if it.Status == ItemPending {
			itemsChan <- it.GuidFixed
		}
	}
	close(itemsChan)

	wg.Wait()

	select {
	case <-stopEarly:
		if stopReason == "draining" {
			// Shutdown, not cancellation: leave the run in a resumable
			// state (Drain has already requeued it) rather than writing a
			// terminal status. Calling FinishRun here would set finishedat
			// and a terminal status, and the run would never be picked up
			// again — which is the whole failure this path exists to avoid.
			log.Printf("[batch %s] paused for shutdown — %d item(s) left for the next instance", batchID, remainingPendingCount(batchID))
			return
		}
		_ = FinishRun(batchID, StatusCancelled, stopReason)
		log.Printf("[batch %s] stopped early: %s", batchID, stopReason)
		return
	default:
	}

	// No worker observed the stop signal — but a cancel can still have been
	// requested after the last item was already picked up (with N items and
	// N workers, every item can be in flight before the cancel lands, so
	// nothing ever re-checks between items). Consult the flag itself rather
	// than only the channel, so a run the user cancelled never reports back
	// as "completed" — the frontend distinguishes these two outcomes and
	// showing "เสร็จสิ้น" for a cancelled run would be plainly wrong.
	if cancelled, err := IsCancelRequested(batchID); err == nil && cancelled {
		_ = FinishRun(batchID, StatusCancelled, "user_requested")
		log.Printf("[batch %s] cancelled (requested while final items were already in flight)", batchID)
		return
	}

	_ = FinishRun(batchID, StatusCompleted, "")
	log.Printf("[batch %s] completed", batchID)
}

// remainingPendingCount is a best-effort count for the shutdown log line —
// returns 0 rather than an error if the run can't be read, since this is
// only used for logging during shutdown.
func remainingPendingCount(batchID string) int {
	run, err := getByBatchIDOnly(batchID)
	if err != nil || run == nil {
		return 0
	}
	n := 0
	for _, it := range run.Items {
		if it.Status == ItemPending || it.Status == ItemProcessing {
			n++
		}
	}
	return n
}

func runHeartbeat(batchID string, stop <-chan struct{}) {
	leaseTTL := time.Duration(configs.BATCH_OCR_STALE_LEASE_SEC) * time.Second
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := Heartbeat(batchID, leaseTTL); err != nil {
				log.Printf("[batch %s] heartbeat failed: %v", batchID, err)
			}
		}
	}
}

// loadImageReferences reads the image references for a batch of documents
// in one query (same $in-batching reasoning as
// storage.RefreshGroupStates — documentImageGroups has no index, so this
// must not become one findOne per document) and converts them into the
// []api.ImageReference shape RunAnalyzeForBatch expects.
func loadImageReferences(shopID, taskGuid string, guids []string) (map[string][]api.ImageReference, error) {
	result := make(map[string][]api.ImageReference, len(guids))
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
	projection := bson.M{"guidfixed": 1, "imagereferences": 1}
	findOpts := options.Find().SetProjection(projection)

	cursor, err := storage.GetMongoDB().Collection(configs.DOCUMENT_IMAGE_GROUP_COLLECTION).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to load image references: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []storage.DocumentImageGroupRef
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("failed to decode image references: %w", err)
	}

	for _, d := range docs {
		refs := make([]api.ImageReference, 0, len(d.ImageReferences))
		for _, ir := range d.ImageReferences {
			refs = append(refs, api.ImageReference{
				DocumentImageGUID: ir.DocumentImageGUID,
				ImageURI:          ir.ImageURI,
			})
		}
		result[d.GuidFixed] = refs
	}
	return result, nil
}

// processItem handles exactly one document: recheck-in-bulk was already
// done by the caller's periodic check, so here we do the final,
// authoritative check via SetGroupOcrAnalyzeAI's own atomic filter — see
// that function's doc comment for why the earlier bulk recheck is a cost
// optimization, not the actual race guard.
func processItem(batchID, shopID, model, guidfixed string, imgRefs []api.ImageReference, masterCache *storage.MasterDataCache, documentTemplates []bson.M) {
	if len(imgRefs) == 0 {
		_ = MarkItemFailed(batchID, guidfixed, "no_image_references", "no image references found for this document")
		log.Printf("[batch %s] item %s → failed (attempt 1) no_image_references", batchID, guidfixed)
		return
	}

	if err := MarkItemProcessing(batchID, guidfixed); err != nil {
		log.Printf("[batch %s] item %s: failed to mark processing: %v", batchID, guidfixed, err)
	}

	maxAttempts := configs.BATCH_OCR_MAX_ATTEMPTS
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	// One watchdog per attempt, not one for the whole retry sequence — each
	// call to RunAnalyzeForBatch gets its own budget of
	// BATCH_OCR_ITEM_TIMEOUT_SEC*1.5. A single watchdog spanning every
	// attempt would let a document with maxAttempts=2 legitimately run twice
	// as long as the timeout implies before anything notices, or (worse)
	// fire mid-way through a second, otherwise-healthy attempt because the
	// clock started on the first one.
	runAttemptWithWatchdog := func(attempt int) (map[string]interface{}, *jobs.JobError) {
		watchdogTimeout := time.Duration(float64(configs.BATCH_OCR_ITEM_TIMEOUT_SEC)*1.5) * time.Second
		done := make(chan struct{})
		defer close(done)

		go func() {
			select {
			case <-done:
			case <-time.After(watchdogTimeout):
				// Intentionally does not cancel the underlying pipeline call
				// — runAnalyzePipeline uses a wall-clock deadline it checks
				// itself, not a context, so there is nothing here to cancel.
				// This just stops the batch from waiting on it forever; the
				// goroutine actually running the pipeline is left to finish
				// (or not) on its own. A leaked goroutine here is the
				// accepted trade-off over a batch run hanging indefinitely —
				// do not "fix" this into trying to hang until the pipeline
				// call returns.
				log.Printf("[batch %s] item %s → WATCHDOG: attempt %d exceeded %v, marking stuck and moving on", batchID, guidfixed, attempt, watchdogTimeout)
				_ = MarkItemFailed(batchID, guidfixed, "stuck", "exceeded watchdog timeout")
			}
		}()

		return api.RunAnalyzeForBatch(shopID, model, imgRefs, masterCache, documentTemplates)
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, jobErr := runAttemptWithWatchdog(attempt)

		if jobErr != nil && jobErr.Code == "backpressure" {
			// The in-memory job store is at capacity with other (mostly
			// interactive) work — not this document's fault, so this does
			// not count as an attempt. Wait and retry the same attempt
			// number, up to 5 times, then give up as failed.
			backpressureRetries := 0
			for jobErr != nil && jobErr.Code == "backpressure" && backpressureRetries < 5 {
				backpressureRetries++
				time.Sleep(10 * time.Second)
				result, jobErr = runAttemptWithWatchdog(attempt)
			}
		}

		if jobErr == nil {
			handleSuccess(batchID, shopID, guidfixed, attempt, result)
			return
		}

		retryable := jobErr.Code == "download_failed" || jobErr.Code == "PROCESSING_TIMEOUT" || jobErr.Code == "backpressure"
		if attempt < maxAttempts && retryable {
			log.Printf("[batch %s] item %s → retrying (attempt %d/%d) %s: %s", batchID, guidfixed, attempt, maxAttempts, jobErr.Code, jobErr.Message)
			continue
		}

		_ = MarkItemFailed(batchID, guidfixed, jobErr.Code, jobErr.Message)
		log.Printf("[batch %s] item %s → failed (attempt %d) %s: %s", batchID, guidfixed, attempt, jobErr.Code, jobErr.Message)
		return
	}
}

func handleSuccess(batchID, shopID, guidfixed string, attempt int, result map[string]interface{}) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		_ = MarkItemFailed(batchID, guidfixed, "marshal_failed", err.Error())
		log.Printf("[batch %s] item %s → failed (attempt %d) marshal_failed: %v", batchID, guidfixed, attempt, err)
		return
	}

	written, err := storage.SetGroupOcrAnalyzeAI(shopID, guidfixed, string(resultJSON))
	if err != nil {
		_ = MarkItemFailed(batchID, guidfixed, "write_failed", err.Error())
		log.Printf("[batch %s] item %s → failed (attempt %d) write_failed: %v", batchID, guidfixed, attempt, err)
		return
	}

	if !written {
		// Someone else (a manual click, or — in theory — another instance)
		// wrote an OCR result to this document between our bulk recheck and
		// now. The AI cost for this call was still paid, but the data on
		// the document is correct (whichever result got there first), so
		// this is a skip, not a failure.
		_ = MarkItemSkipped(batchID, guidfixed)
		log.Printf("[batch %s] item %s → skipped (attempt %d) already_analyzed_by_someone_else", batchID, guidfixed, attempt)
		return
	}

	_ = MarkItemDone(batchID, guidfixed)
	log.Printf("[batch %s] item %s → done (attempt %d)", batchID, guidfixed, attempt)
}
