// batch_handlers.go - HTTP endpoints and single-document runner for batch
// background OCR. Lives in package api (not internal/batchocr) specifically
// so RunAnalyzeForBatch can call runAnalyzePipeline directly — that function
// is unexported and this is the only way to reuse it without either
// exporting it (widening its surface for a 930-line, highest-risk function
// in this codebase) or duplicating its logic (guaranteed to drift).
//
// See plan-clever-lemon.md for the full design. internal/batchocr owns the
// batch run/item state and the worker loop that calls RunAnalyzeForBatch
// below, once per eligible document.

package api

import (
	"fmt"

	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/jobs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
)

// RunAnalyzeForBatch runs the exact same analyze pipeline a single
// interactive "AI วิเคราะห์" click would (runAnalyzePipeline), but
// synchronously and without ever going through the jobs HTTP endpoints —
// there's no HTTP client polling here, just a worker goroutine that needs
// the finished result or an error.
//
// masterCache and documentTemplates are passed in rather than loaded here so
// the batch worker can load them once per run and reuse them across every
// document, instead of paying a fresh Mongo round-trip per document (see
// internal/batchocr's worker, which loads these before starting its pool).
func RunAnalyzeForBatch(shopID, model string, imgRefs []ImageReference, masterCache *storage.MasterDataCache, documentTemplates []bson.M) (map[string]interface{}, *jobs.JobError) {
	reqCtx := common.NewRequestContext(shopID)

	job, _, err := jobs.Create(reqCtx)
	if err != nil {
		// jobs.ErrTooManyActiveJobs specifically — the in-memory job store is
		// at capacity with other (interactive) work. This is not this
		// document's fault, so the worker treats it as backpressure and
		// retries the same document rather than counting it as a failed
		// attempt (see internal/batchocr's worker).
		return nil, &jobs.JobError{Code: "backpressure", Message: err.Error()}
	}

	req := ExtractRequest{
		ShopID:          shopID,
		ImageReferences: imgRefs,
		Model:           model,
	}

	// Deliberately synchronous (no `go`) — the batch worker calls this one
	// document at a time and needs the result before deciding what to do
	// next (write it back, retry, or mark failed). runAnalyzePipeline itself
	// is unaware of and unaffected by whether it's called this way or via
	// `go runAnalyzePipeline(...)` from SubmitAnalyzeReceiptHandler.
	runAnalyzePipeline(job, req, false, masterCache, documentTemplates)

	snap := job.Snapshot()
	if snap.Status != jobs.StatusCompleted {
		if snap.Error != nil {
			return nil, snap.Error
		}
		// Should not happen — runAnalyzePipeline always calls either
		// job.Complete or job.Fail before returning — but fail closed with
		// an explicit error rather than returning a nil result silently.
		return nil, &jobs.JobError{Code: "unknown_pipeline_state", Message: fmt.Sprintf("pipeline ended in status %q with no result and no error", snap.Status)}
	}

	// runAnalyzePipeline calls job.Complete(response) where response is a
	// gin.H, not a plain map[string]interface{} — gin.H is a distinct named
	// type (`type H map[string]any`), so a type assertion against the
	// unnamed map type fails even though the underlying representation is
	// identical. Asserting against gin.H first and converting is the
	// correct way to unwrap this, not asserting against map[string]any.
	if h, ok := snap.Result.(gin.H); ok {
		return map[string]interface{}(h), nil
	}
	if m, ok := snap.Result.(map[string]interface{}); ok {
		return m, nil
	}

	// Should not happen — see above — but fail closed with an explicit
	// error rather than silently returning a nil/empty result.
	return nil, &jobs.JobError{Code: "unexpected_result_type", Message: fmt.Sprintf("expected gin.H or map[string]interface{} result, got %T", snap.Result)}
}

// AsStringMap unwraps a value that is expected to be either gin.H or
// map[string]interface{} into the latter, or returns ok=false for anything
// else.
//
// This exists because runAnalyzePipeline builds its response as nested
// gin.H values (gin.H{"metadata": gin.H{"token_usage": gin.H{...}}}), and
// gin.H is a distinct named type (`type H map[string]any`) — a type
// assertion against the unnamed map[string]interface{} fails for a gin.H
// value even though their underlying representation is identical. Every
// caller that walks into RunAnalyzeForBatch's result (e.g.
// internal/batchocr's worker reading the result metadata) hits
// this at every nesting level, not just the top one that RunAnalyzeForBatch
// itself already unwraps — so this helper is here rather than only inlined
// once.
func AsStringMap(v interface{}) (map[string]interface{}, bool) {
	switch m := v.(type) {
	case gin.H:
		return map[string]interface{}(m), true
	case map[string]interface{}:
		return m, true
	default:
		return nil, false
	}
}
