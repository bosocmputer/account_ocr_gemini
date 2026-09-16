// handlers.go - HTTP endpoints for batch background OCR.
//
// Lives in package batchocr, not internal/api, specifically to avoid an
// import cycle: internal/batchocr's worker (worker.go) already imports
// internal/api for RunAnalyzeForBatch (which must live in package api to
// call the unexported runAnalyzePipeline) — so internal/api cannot import
// internal/batchocr back. These handlers only need this package's own store
// functions plus internal/storage, so there's no reason they'd need to live
// in internal/api anyway.
package batchocr

import (
	"fmt"
	"math"
	"net/http"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// batchOcrRequest is the shared request body shape for POST /batch-ocr.
type batchOcrRequest struct {
	ShopID    string `json:"shopid"`
	TaskGuid  string `json:"taskguid"`
	Model     string `json:"model"`
	CreatedBy string `json:"createdby"`
}

func (r *batchOcrRequest) validate() (msg string, ok bool) {
	if r.ShopID == "" {
		return "shopid is required", false
	}
	if r.TaskGuid == "" {
		return "taskguid is required", false
	}
	if r.Model == "" {
		return "model is required", false
	}
	if r.Model != "gemini" && r.Model != "mistral" {
		return fmt.Sprintf("invalid model %q — must be 'gemini' or 'mistral'", r.Model), false
	}
	return "", true
}

// eligibilitySummary is the shared eligible/skipped breakdown used by both
// the preview and submit endpoints, so the numbers a user sees in the
// confirm dialog (from preview) match what actually gets queued (from
// submit) — computed by the exact same function against the exact same
// helpers (HasOcrResult/IsAlreadyRecorded) the worker itself uses to decide
// what to skip.
type eligibilitySummary struct {
	Eligible          []storage.DocumentImageGroupRef
	SkippedOcr        int // has an OCR result already
	SkippedReferenced int // already saved as a journal entry
}

func computeEligibility(shopID, taskGuid string) (*eligibilitySummary, error) {
	groups, err := storage.ListEligibleGroupsForTask(shopID, taskGuid)
	if err != nil {
		return nil, err
	}

	summary := &eligibilitySummary{}
	for _, g := range groups {
		hasOcr := g.HasOcrResult()
		hasRef := g.IsAlreadyRecorded()
		switch {
		case hasRef:
			summary.SkippedReferenced++
		case hasOcr:
			summary.SkippedOcr++
		default:
			summary.Eligible = append(summary.Eligible, g)
		}
	}
	return summary, nil
}

// estimateMinutes gives a deliberately generous (never-optimistic) time
// estimate — the actual throughput ceiling is internal/ratelimit's shared
// token bucket (~12 calls/min for the whole process, and each document costs
// 2-4 calls), so ~3 documents/minute is used as the planning assumption.
// Better to under-promise here than have a user think a batch is stuck when
// it's actually on schedule.
func estimateMinutes(totalDocs int) int {
	if totalDocs <= 0 {
		return 0
	}
	return int(math.Ceil(float64(totalDocs) / 3.0))
}

// GetBatchOcrPreviewHandler handles GET /batch-ocr/preview?shopid=&taskguid=.
// Computes the same eligible/skipped breakdown SubmitBatchOcrHandler would,
// without creating a run — this is what powers the confirm dialog showing
// real numbers ("อ่านจริง 12 · ข้าม 29 · ~4 นาที · ~฿3.60") before a user
// commits to a batch that can spend real money.
func GetBatchOcrPreviewHandler(c *gin.Context) {
	shopID := c.Query("shopid")
	taskGuid := c.Query("taskguid")
	if shopID == "" || taskGuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "shopid and taskguid are required"})
		return
	}

	summary, err := computeEligibility(shopID, taskGuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compute eligibility", "details": err.Error()})
		return
	}

	eligibleCount := len(summary.Eligible)
	c.JSON(http.StatusOK, gin.H{
		"eligible":           eligibleCount,
		"skipped_ocr":        summary.SkippedOcr,
		"skipped_referenced": summary.SkippedReferenced,
		"estimated_minutes":  estimateMinutes(eligibleCount),
	})
}

// SubmitBatchOcrHandler handles POST /batch-ocr. See plan-clever-lemon.md
// TODO-8 for the full step-by-step this follows.
func SubmitBatchOcrHandler(c *gin.Context) {
	var req batchOcrRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request format", "details": err.Error()})
		return
	}
	if msg, ok := req.validate(); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg, "allowed_values": []string{"gemini", "mistral"}})
		return
	}

	if !configs.BATCH_OCR_ENABLED {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "batch_ocr_disabled", "message": "Batch OCR is currently disabled"})
		return
	}

	// Idempotent submit: a double-click or a second tab must not create a
	// second run for the same task — return the existing one's batchid
	// instead, with 200 rather than 202/409, so the frontend can treat this
	// exactly like a fresh submit that happened to already be in progress.
	if active, err := FindActiveByTask(req.ShopID, req.TaskGuid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check for an active run", "details": err.Error()})
		return
	} else if active != nil {
		c.JSON(http.StatusOK, gin.H{
			"batchid":         active.BatchID,
			"total":           active.Total,
			"already_running": true,
		})
		return
	}

	// Task must not already be closed — starting a batch against a closed
	// task would just get auto-cancelled on the worker's first recheck
	// (wasting the one document it does read before that check fires), so
	// reject it up front instead.
	if isOpen, checked, err := checkTaskIsOpen(req.ShopID, req.TaskGuid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check task status", "details": err.Error()})
		return
	} else if checked && !isOpen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_closed", "message": "งานนี้ถูกปิดแล้ว ไม่สามารถเริ่ม batch ได้"})
		return
	}

	summary, err := computeEligibility(req.ShopID, req.TaskGuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compute eligibility", "details": err.Error()})
		return
	}

	if len(summary.Eligible) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":              "nothing_to_process",
			"message":            "เอกสารทั้งหมดอ่านหรือบันทึกไปแล้ว",
			"skipped_ocr":        summary.SkippedOcr,
			"skipped_referenced": summary.SkippedReferenced,
		})
		return
	}

	if len(summary.Eligible) > configs.BATCH_OCR_MAX_ITEMS {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":     "too_many_documents",
			"message":   fmt.Sprintf("มีเอกสารที่ต้องอ่าน %d รายการ เกินขีดจำกัด %d รายการต่อ batch", len(summary.Eligible), configs.BATCH_OCR_MAX_ITEMS),
			"eligible":  len(summary.Eligible),
			"max_items": configs.BATCH_OCR_MAX_ITEMS,
		})
		return
	}

	// Master data must exist before we accept the run at all — same
	// fail-fast reasoning as SubmitAnalyzeReceiptHandler (handlers.go).
	masterCache, err := storage.GetOrLoadMasterData(req.ShopID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load master data", "details": err.Error()})
		return
	}
	if len(masterCache.Accounts) == 0 || len(masterCache.JournalBooks) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "master_data_not_found",
			"message": "ไม่พบข้อมูล Master Data สำหรับ Shop นี้ กรุณาตั้งค่าผังบัญชี (Chart of Accounts) และสมุดรายวัน (Journal Books) ใน MongoDB ก่อนใช้งาน",
		})
		return
	}

	items := make([]BatchItem, len(summary.Eligible))
	for i, g := range summary.Eligible {
		items[i] = BatchItem{GuidFixed: g.GuidFixed, Title: g.Title, Status: ItemPending}
	}

	batchID := uuid.New().String()
	run, err := Create(batchID, req.ShopID, req.TaskGuid, req.Model, req.CreatedBy, items)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create batch run", "details": err.Error()})
		return
	}

	go StartBatchRun(run.BatchID)

	c.JSON(http.StatusAccepted, gin.H{
		"batchid":            run.BatchID,
		"total":              run.Total,
		"skipped_ocr":        summary.SkippedOcr,
		"skipped_referenced": summary.SkippedReferenced,
		"estimated_minutes":  estimateMinutes(run.Total),
	})
}

// GetActiveBatchOcrHandler handles GET /batch-ocr/active?shopid=&taskguid=.
// This is the endpoint that lets a user close their browser mid-batch and,
// on reopening the task page later, see the progress bar reappear — the
// frontend calls this on mount and starts polling GetBatchOcrStatusHandler
// if it finds an active run.
func GetActiveBatchOcrHandler(c *gin.Context) {
	shopID := c.Query("shopid")
	taskGuid := c.Query("taskguid")
	if shopID == "" || taskGuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "shopid and taskguid are required"})
		return
	}

	active, err := FindActiveByTask(shopID, taskGuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check for an active run", "details": err.Error()})
		return
	}
	if active == nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{"active": true, "batchid": active.BatchID})
}

// maxFailedItemsInResponse caps how many failed-item details
// GetBatchOcrStatusHandler returns per poll — a run can have up to
// BATCH_OCR_MAX_ITEMS (default 100) items, and returning full detail for
// all of them on every ~5-second poll would be wasted bandwidth for what's
// meant to be a lightweight progress check.
const maxFailedItemsInResponse = 50

// GetBatchOcrStatusHandler handles GET /batch-ocr/:batchId?shopid=. Every
// caller must pass shopid, and a mismatch returns 404 (not 403) — a wrong
// shopid means "there is no such run visible to you", not "you're forbidden
// from a run that exists"; 403 would confirm to a caller guessing batchids
// that a real run exists there, 404 doesn't.
func GetBatchOcrStatusHandler(c *gin.Context) {
	batchID := c.Param("batchId")
	shopID := c.Query("shopid")
	if shopID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "shopid is required"})
		return
	}

	run, err := GetByID(batchID, shopID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load batch run", "details": err.Error()})
		return
	}
	if run == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "batch_not_found"})
		return
	}

	var done, failed, skipped, pending, processing int
	var failedItems []gin.H
	failedTruncated := false
	for _, item := range run.Items {
		switch item.Status {
		case ItemDone:
			done++
		case ItemFailed:
			failed++
			if len(failedItems) < maxFailedItemsInResponse {
				failedItems = append(failedItems, gin.H{
					"guidfixed":    item.GuidFixed,
					"title":        item.Title,
					"errorcode":    item.ErrorCode,
					"errormessage": item.ErrorMessage,
					"attempts":     item.Attempts,
				})
			} else {
				failedTruncated = true
			}
		case ItemSkipped:
			skipped++
		case ItemProcessing:
			processing++
		default:
			pending++
		}
	}

	percent := 0
	if run.Total > 0 {
		percent = int(math.Round(float64(done+failed+skipped) / float64(run.Total) * 100))
	}

	remainingDocs := pending + processing
	estimatedRemainingMinutes := estimateMinutes(remainingDocs)

	c.JSON(http.StatusOK, gin.H{
		"status":                      run.Status,
		"total":                       run.Total,
		"done":                        done,
		"failed":                      failed,
		"skipped":                     skipped,
		"pending":                     pending,
		"processing":                  processing,
		"percent":                     percent,
		"model":                       run.Model,
		"createdat":                   run.CreatedAt,
		"finishedat":                  run.FinishedAt,
		"cancelrequested":             run.CancelRequested,
		"cancelreason":                run.CancelReason,
		"failed_items":                failedItems,
		"failed_truncated":            failedTruncated,
		"estimated_remaining_minutes": estimatedRemainingMinutes,
	})
}

// CancelBatchOcrHandler handles POST /batch-ocr/:batchId/cancel. Sets the
// cancel flag and returns immediately — the document currently being
// analyzed (if any) will still finish; see RequestCancel/IsCancelRequested's
// doc comments and the worker's periodic check for why this can't be
// instantaneous.
func CancelBatchOcrHandler(c *gin.Context) {
	batchID := c.Param("batchId")
	shopID := c.PostForm("shopid")
	if shopID == "" {
		shopID = c.Query("shopid")
	}
	if shopID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "shopid is required"})
		return
	}

	run, err := GetByID(batchID, shopID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load batch run", "details": err.Error()})
		return
	}
	if run == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "batch_not_found"})
		return
	}

	if err := RequestCancel(batchID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to request cancel", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"cancelling": true,
		"message":    "กำลังยกเลิก — เอกสารที่กำลังอ่านอยู่จะทำจนจบก่อนหยุด",
	})
}

// RetryBatchOcrFailedHandler handles POST /batch-ocr/:batchId/retry. Resets
// every failed item back to pending and re-dispatches the run. Returns 409
// if the run is still actively working (queued/running) — retry only makes
// sense once a run has actually stopped.
func RetryBatchOcrFailedHandler(c *gin.Context) {
	batchID := c.Param("batchId")
	shopID := c.PostForm("shopid")
	if shopID == "" {
		shopID = c.Query("shopid")
	}
	if shopID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "shopid is required"})
		return
	}

	run, err := GetByID(batchID, shopID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load batch run", "details": err.Error()})
		return
	}
	if run == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "batch_not_found"})
		return
	}
	if run.Status == StatusQueued || run.Status == StatusRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "batch_still_running", "message": "batch นี้ยังทำงานอยู่ ลองใหม่ได้เมื่อจบแล้วเท่านั้น"})
		return
	}

	resetCount, err := ResetItemsForRetry(batchID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reset failed items", "details": err.Error()})
		return
	}
	if resetCount == 0 {
		c.JSON(http.StatusOK, gin.H{"retried": 0, "message": "ไม่มีรายการที่ล้มเหลวให้ลองใหม่"})
		return
	}

	// Put the run back into "queued" so the dispatcher (or this call itself,
	// if nothing else is running) picks it up again — FinishRun left it in
	// a terminal status.
	if err := reopenForRetry(batchID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to requeue batch run", "details": err.Error()})
		return
	}

	go maybeDispatchNext()

	c.JSON(http.StatusOK, gin.H{"retried": resetCount})
}
