// store.go - MongoDB-backed store for batch OCR runs.
//
// This is a separate collection (BATCH_OCR_COLLECTION, default
// "batch_ocr_runs") that this service owns outright — unlike
// documentImageGroups, it is safe to index and safe to shape however this
// package needs. Do not confuse this package with internal/jobs: that one
// is an in-memory store for short-lived (minutes), single-instance jobs and
// is intentionally left alone (see plan-clever-lemon.md) — a batch run can
// span hours, must survive a process restart, and must be resumable from
// any instance, none of which internal/jobs was built for.
package batchocr

import (
	"context"
	"fmt"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Run status values.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

// Item status values.
const (
	ItemPending    = "pending"
	ItemProcessing = "processing"
	ItemDone       = "done"
	ItemFailed     = "failed"
	ItemSkipped    = "skipped"
)

// maxItemsHardCap is an absolute ceiling on documents per run, independent
// of BATCH_OCR_MAX_ITEMS. A run document embeds all of its items in one
// array (see BatchRun.Items) — MongoDB's 16MB document limit and the cost of
// `$set items.$` positional updates both degrade well before that limit as
// the array grows, so this stays fixed regardless of how BATCH_OCR_MAX_ITEMS
// is configured in the environment.
const maxItemsHardCap = 1000

// BatchItem tracks one document within a batch run.
type BatchItem struct {
	GuidFixed    string     `bson:"guidfixed"`
	Title        string     `bson:"title"`
	Status       string     `bson:"status"`
	Attempts     int        `bson:"attempts"`
	ErrorCode    string     `bson:"errorcode,omitempty"`
	ErrorMessage string     `bson:"errormessage,omitempty"`
	StartedAt    *time.Time `bson:"startedat,omitempty"`
	FinishedAt   *time.Time `bson:"finishedat,omitempty"`
	CostTHB      float64    `bson:"costthb"`
}

// BatchRun tracks one batch OCR run for one task.
type BatchRun struct {
	BatchID          string      `bson:"batchid"`
	ShopID           string      `bson:"shopid"`
	TaskGuid         string      `bson:"taskguid"`
	Model            string      `bson:"model"`
	Status           string      `bson:"status"`
	OwnerInstance    string      `bson:"ownerinstance"`
	LeaseExpiresAt   time.Time   `bson:"leaseexpiresat"`
	Total            int         `bson:"total"`
	EstimatedCostTHB float64     `bson:"estimatedcostthb"`
	TotalCostTHB     float64     `bson:"totalcostthb"`
	CreatedAt        time.Time   `bson:"createdat"`
	CreatedBy        string      `bson:"createdby"`
	UpdatedAt        time.Time   `bson:"updatedat"`
	FinishedAt       *time.Time  `bson:"finishedat,omitempty"`
	CancelRequested  bool        `bson:"cancelrequested"`
	CancelReason     string      `bson:"cancelreason,omitempty"`
	Items            []BatchItem `bson:"items"`
}

func collection() *mongo.Collection {
	return storage.GetMongoDB().Collection(configs.BATCH_OCR_COLLECTION)
}

// EnsureIndexes creates the indexes this package relies on. Safe to call on
// every startup — CreateMany is idempotent for indexes that already exist
// with the same keys/options. Must never be called against
// DOCUMENT_IMAGE_GROUP_COLLECTION; this collection is the one collection in
// this whole feature that we actually own.
func EnsureIndexes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection().Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			// Finds the active run for a task (FindActiveByTask).
			Keys: bson.D{{Key: "shopid", Value: 1}, {Key: "taskguid", Value: 1}, {Key: "status", Value: 1}},
		},
		{
			Keys:    bson.D{{Key: "batchid", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			// Scanned by ClaimOrphanedRuns during resume.
			Keys: bson.D{{Key: "status", Value: 1}, {Key: "leaseexpiresat", Value: 1}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create batch OCR indexes: %w", err)
	}
	return nil
}

// Create inserts a new batch run in "queued" status with all items
// "pending". Rejects more than maxItemsHardCap items regardless of what
// BATCH_OCR_MAX_ITEMS is configured to — see that constant's comment.
func Create(batchID, shopID, taskGuid, model, createdBy string, items []BatchItem, estimatedCostTHB float64) (*BatchRun, error) {
	if len(items) > maxItemsHardCap {
		return nil, fmt.Errorf("batch of %d items exceeds hard cap of %d", len(items), maxItemsHardCap)
	}

	now := time.Now()
	run := &BatchRun{
		BatchID:          batchID,
		ShopID:           shopID,
		TaskGuid:         taskGuid,
		Model:            model,
		Status:           StatusQueued,
		Total:            len(items),
		EstimatedCostTHB: estimatedCostTHB,
		CreatedAt:        now,
		CreatedBy:        createdBy,
		UpdatedAt:        now,
		Items:            items,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := collection().InsertOne(ctx, run); err != nil {
		return nil, fmt.Errorf("failed to create batch run: %w", err)
	}
	return run, nil
}

// GetByID fetches one run by batchid, additionally scoped to shopid so a
// caller can never fetch another shop's run by guessing/brute-forcing a
// batchid.
func GetByID(batchID, shopID string) (*BatchRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var run BatchRun
	err := collection().FindOne(ctx, bson.M{"batchid": batchID, "shopid": shopID}).Decode(&run)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get batch run: %w", err)
	}
	return &run, nil
}

// getByBatchIDOnly fetches a run by batchid without requiring the caller to
// already know its shopid — unlike the exported GetByID (which is
// shop-scoped specifically so an HTTP caller can't fetch another shop's run
// by guessing a batchid), this is for internal callers that only have a
// batchid to begin with: the worker (which reads it off the run it's
// already about to process) and the HTTP handlers immediately after
// resolving a run via FindActiveByTask/GetByID once, where shopid has
// already been checked.
func getByBatchIDOnly(batchID string) (*BatchRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var run BatchRun
	err := collection().FindOne(ctx, bson.M{"batchid": batchID}).Decode(&run)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get batch run %s: %w", batchID, err)
	}
	return &run, nil
}

// findOldestQueuedRun returns the oldest run still in "queued" status
// process-wide (across all shops/tasks), or nil if none — used by the
// dispatcher to decide what to work on next once the currently-running
// batch finishes, since only one run is actively worked at a time (the
// AI rate limit is the real bottleneck regardless of task).
func findOldestQueuedRun() (*BatchRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := options.FindOne().SetSort(bson.D{{Key: "createdat", Value: 1}})
	var run BatchRun
	err := collection().FindOne(ctx, bson.M{"status": StatusQueued}, opts).Decode(&run)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find oldest queued run: %w", err)
	}
	return &run, nil
}

// FindActiveByTask returns the queued/running run for a task, if any. Used
// both to resume polling ("is there already a run for this task?") and to
// make submitting a batch idempotent — see SubmitBatchOcrHandler, which
// returns this run's batchid instead of creating a duplicate when the user
// double-clicks or has two tabs open.
func FindActiveByTask(shopID, taskGuid string) (*BatchRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"shopid":   shopID,
		"taskguid": taskGuid,
		"status":   bson.M{"$in": []string{StatusQueued, StatusRunning}},
	}

	var run BatchRun
	err := collection().FindOne(ctx, filter).Decode(&run)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find active batch run: %w", err)
	}
	return &run, nil
}

// updateItemFields applies a positional $set to the item matching guidfixed
// within a run, plus bumping the run's updatedat. All the MarkItem* helpers
// below are thin wrappers around this.
func updateItemFields(batchID, guidfixed string, fields bson.M) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fields["updatedat"] = time.Now()
	setFields := bson.M{}
	for k, v := range fields {
		if k == "updatedat" {
			setFields[k] = v
			continue
		}
		setFields["items.$."+k] = v
	}

	filter := bson.M{"batchid": batchID, "items.guidfixed": guidfixed}
	_, err := collection().UpdateOne(ctx, filter, bson.M{"$set": setFields})
	if err != nil {
		return fmt.Errorf("failed to update batch item %s: %w", guidfixed, err)
	}
	return nil
}

// MarkItemProcessing marks one item as currently being worked on.
func MarkItemProcessing(batchID, guidfixed string) error {
	now := time.Now()
	return updateItemFields(batchID, guidfixed, bson.M{
		"status":    ItemProcessing,
		"startedat": now,
	})
}

// MarkItemDone marks one item as successfully analyzed and written back to
// documentImageGroups.
func MarkItemDone(batchID, guidfixed string) error {
	now := time.Now()
	return updateItemFields(batchID, guidfixed, bson.M{
		"status":     ItemDone,
		"finishedat": now,
	})
}

// MarkItemSkipped marks one item as skipped — the document already had an
// OCR result or a saved reference by the time the worker got to it (someone
// else got there first, or it was analyzed/saved manually mid-run). This is
// not a failure: the document's data is correct, the batch simply didn't
// need to (and in the write-race case, must not) touch it.
func MarkItemSkipped(batchID, guidfixed string) error {
	now := time.Now()
	return updateItemFields(batchID, guidfixed, bson.M{
		"status":     ItemSkipped,
		"finishedat": now,
	})
}

// MarkItemFailed marks one item as failed after retries are exhausted.
// Must never be called for an item that was actually written successfully —
// see internal/storage.SetGroupOcrAnalyzeAI's caller in the worker, which is
// the only place that decides between MarkItemDone and MarkItemFailed.
func MarkItemFailed(batchID, guidfixed, errorCode, errorMessage string) error {
	now := time.Now()
	return updateItemFields(batchID, guidfixed, bson.M{
		"status":       ItemFailed,
		"errorcode":    errorCode,
		"errormessage": errorMessage,
		"finishedat":   now,
	})
}

// IncrementItemAttempts bumps one item's attempt counter and resets it to
// pending for a retry — used both for the worker's own automatic retry on a
// transient failure and for RetryBatchOcrFailedHandler's manual "ลองใหม่".
func IncrementItemAttempts(batchID, guidfixed string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"batchid": batchID, "items.guidfixed": guidfixed}
	update := bson.M{
		"$inc": bson.M{"items.$.attempts": 1},
		"$set": bson.M{
			"items.$.status":     ItemPending,
			"items.$.errorcode":  "",
			"items.$.finishedat": nil,
			"updatedat":          time.Now(),
		},
	}
	_, err := collection().UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("failed to increment attempts for batch item %s: %w", guidfixed, err)
	}
	return nil
}

// ResetItemsForRetry resets every "failed" item in a run back to "pending"
// (without touching "done"/"skipped" items) so the worker picks them up
// again. Used by RetryBatchOcrFailedHandler.
func ResetItemsForRetry(batchID string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"batchid": batchID, "items.status": ItemFailed}
	update := bson.M{
		"$set": bson.M{
			"items.$[failedItem].status":     ItemPending,
			"items.$[failedItem].errorcode":  "",
			"items.$[failedItem].finishedat": nil,
			"updatedat":                      time.Now(),
		},
	}
	arrayFilters := options.Update().SetArrayFilters(options.ArrayFilters{
		Filters: []interface{}{bson.M{"failedItem.status": ItemFailed}},
	})

	res, err := collection().UpdateOne(ctx, filter, update, arrayFilters)
	if err != nil {
		return 0, fmt.Errorf("failed to reset failed items for retry: %w", err)
	}
	return int(res.ModifiedCount), nil
}

// markRunning transitions a run from "queued" to "running" and sets its
// initial lease and owner, for the direct (non-resume) start path —
// SubmitBatchOcrHandler starts a freshly-created run with `go
// StartBatchRun(...)` directly rather than through ClaimOrphanedRuns, so
// without this call a run would sit showing status "queued" to every poller
// for its entire duration (ClaimOrphanedRuns is the only other place that
// sets StatusRunning, and it only ever looks at orphaned runs, never a
// brand-new one actively being worked by the process that just created it).
func markRunning(batchID, ownerInstance string, leaseTTL time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now()
	_, err := collection().UpdateOne(ctx,
		bson.M{"batchid": batchID},
		bson.M{"$set": bson.M{
			"status":         StatusRunning,
			"ownerinstance":  ownerInstance,
			"leaseexpiresat": now.Add(leaseTTL),
			"updatedat":      now,
		}},
	)
	if err != nil {
		return fmt.Errorf("failed to mark batch %s running: %w", batchID, err)
	}
	return nil
}

// Heartbeat extends a run's lease — called periodically by the worker while
// a run is actively being processed, so ClaimOrphanedRuns knows this run
// still has a live owner and doesn't try to steal it.
func Heartbeat(batchID string, leaseTTL time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now()
	update := bson.M{"$set": bson.M{
		"leaseexpiresat": now.Add(leaseTTL),
		"updatedat":      now,
	}}
	_, err := collection().UpdateOne(ctx, bson.M{"batchid": batchID}, update)
	if err != nil {
		return fmt.Errorf("failed to send heartbeat for batch %s: %w", batchID, err)
	}
	return nil
}

// reopenForRetry puts a finished run (completed/cancelled) back into
// "queued" with its lease cleared, so ClaimOrphanedRuns/the dispatcher will
// pick it up again — used by RetryBatchOcrFailedHandler after
// ResetItemsForRetry has put the actual failed items back to pending.
func reopenForRetry(batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection().UpdateOne(ctx,
		bson.M{"batchid": batchID},
		bson.M{
			"$set":   bson.M{"status": StatusQueued, "leaseexpiresat": time.Time{}, "updatedat": time.Now()},
			"$unset": bson.M{"finishedat": ""},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to reopen batch run %s for retry: %w", batchID, err)
	}
	return nil
}

// requeueForResume puts a run back to "queued" with an expired lease so the
// next instance's resume scan claims it immediately. Used by graceful
// shutdown (Drain): leaving the run in "running" with no process actually
// running it would mean waiting out the full BATCH_OCR_STALE_LEASE_SEC
// window before anything picked it back up, and marking it cancelled would
// mean it never gets picked back up at all.
//
// Deliberately leaves cancelrequested untouched — that flag is the user's
// decision, not the deployment's.
func requeueForResume(batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection().UpdateOne(ctx,
		bson.M{"batchid": batchID},
		bson.M{"$set": bson.M{
			"status":         StatusQueued,
			"leaseexpiresat": time.Time{},
			"updatedat":      time.Now(),
		}},
	)
	if err != nil {
		return fmt.Errorf("failed to requeue batch %s for resume: %w", batchID, err)
	}
	return nil
}

// releaseLease immediately expires a run's lease (sets it to the zero
// value, which is already "in the past") so another instance's next resume
// scan can claim it right away, instead of waiting out the full
// BATCH_OCR_STALE_LEASE_SEC window. Used by graceful shutdown (see Drain in
// resume.go) — it does not change the run's status, only how soon it
// becomes claimable again.
func releaseLease(batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection().UpdateOne(ctx,
		bson.M{"batchid": batchID},
		bson.M{"$set": bson.M{"leaseexpiresat": time.Time{}, "updatedat": time.Now()}},
	)
	if err != nil {
		return fmt.Errorf("failed to release lease for batch %s: %w", batchID, err)
	}
	return nil
}

// FinishRun marks a run as completed or cancelled. cancelReason is only
// meaningful when finalStatus is StatusCancelled (e.g. "task_closed",
// "user_requested").
func FinishRun(batchID, finalStatus, cancelReason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now()
	set := bson.M{
		"status":     finalStatus,
		"finishedat": now,
		"updatedat":  now,
	}
	if cancelReason != "" {
		set["cancelreason"] = cancelReason
	}

	_, err := collection().UpdateOne(ctx, bson.M{"batchid": batchID}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("failed to finish batch run %s: %w", batchID, err)
	}
	return nil
}

// RequestCancel sets the cancel flag on a run. The worker checks this flag
// between documents (see internal/batchocr's worker) — it cannot interrupt
// a document that's already being analyzed, only stop the run from picking
// up the next one.
func RequestCancel(batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection().UpdateOne(ctx,
		bson.M{"batchid": batchID},
		bson.M{"$set": bson.M{"cancelrequested": true, "updatedat": time.Now()}},
	)
	if err != nil {
		return fmt.Errorf("failed to request cancel for batch %s: %w", batchID, err)
	}
	return nil
}

// IsCancelRequested reports whether a run's cancel flag has been set.
func IsCancelRequested(batchID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var run struct {
		CancelRequested bool `bson:"cancelrequested"`
	}
	err := collection().FindOne(ctx, bson.M{"batchid": batchID}, options.FindOne().SetProjection(bson.M{"cancelrequested": 1})).Decode(&run)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return false, fmt.Errorf("batch run %s not found", batchID)
		}
		return false, fmt.Errorf("failed to check cancel flag for batch %s: %w", batchID, err)
	}
	return run.CancelRequested, nil
}

// AddCost accumulates the real, logged AI cost onto a run's running total —
// separate from EstimatedCostTHB (the pre-run guess shown before the user
// confirms). $inc rather than read-then-write so concurrent item completions
// within the same run never lose an update to a race.
func AddCost(batchID string, costTHB float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection().UpdateOne(ctx,
		bson.M{"batchid": batchID},
		bson.M{
			"$inc": bson.M{"totalcostthb": costTHB},
			"$set": bson.M{"updatedat": time.Now()},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to add cost to batch %s: %w", batchID, err)
	}
	return nil
}

// ClaimOrphanedRuns atomically claims every run whose lease has expired (or
// was never set) for myInstance, one FindOneAndUpdate call per claimed run,
// and returns the claimed runs.
//
// This MUST use FindOneAndUpdate, not a Find() followed by a separate
// UpdateOne — if two instances both read the same set of orphaned runs
// before either writes, they'd both start working the same run, meaning the
// same documents get sent to the AI provider twice: real, avoidable money
// spent twice over a document that only needed to be read once. There is
// only one instance of this service running today, but that is exactly the
// kind of fact that stops being true without anyone deciding it should, and
// this filter+update is only three lines cheaper to get right the first
// time than to have someone reintroduce this bug rediscovering it under
// load.
func ClaimOrphanedRuns(myInstance string, leaseTTL time.Duration) ([]BatchRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var claimed []BatchRun
	now := time.Now()

	for {
		filter := bson.M{
			"status": bson.M{"$in": []string{StatusQueued, StatusRunning}},
			"$or": []bson.M{
				{"leaseexpiresat": bson.M{"$lt": now}},
				{"leaseexpiresat": bson.M{"$exists": false}},
			},
		}
		update := bson.M{"$set": bson.M{
			"ownerinstance":  myInstance,
			"leaseexpiresat": time.Now().Add(leaseTTL),
			"status":         StatusRunning,
			"updatedat":      time.Now(),
		}}

		var run BatchRun
		err := collection().FindOneAndUpdate(
			ctx, filter, update,
			options.FindOneAndUpdate().SetReturnDocument(options.After),
		).Decode(&run)

		if err == mongo.ErrNoDocuments {
			break // nothing left to claim
		}
		if err != nil {
			return claimed, fmt.Errorf("failed to claim orphaned batch runs: %w", err)
		}

		claimed = append(claimed, run)
	}

	return claimed, nil
}
