// handlers.go - Contains the HTTP handler function for file upload and validation logic.

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/ai"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/jobs"
	"github.com/bosocmputer/account_ocr_gemini/internal/processor"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
)

// --- Image Quality Validation Constants ---
const (
	// Minimum confidence thresholds for accepting image quality
	MIN_TEXT_CLARITY_SCORE     = 70.0 // Text must be at least 70% clear
	MIN_HANDWRITING_CONFIDENCE = 85.0 // Handwritten text needs 85%+ confidence
	MIN_OVERALL_CONFIDENCE     = 70.0 // Overall extraction confidence threshold (lowered for diverse document types)
	// MAX_NA_PERCENTAGE removed - not all documents have items (e.g., tax receipts, utility bills)
)

// ImageQualityIssue represents a single quality issue found
type ImageQualityIssue struct {
	Field        string `json:"field"`
	Issue        string `json:"issue"`
	CurrentValue string `json:"current_value,omitempty"`
	MinRequired  string `json:"min_required,omitempty"`
}

// FailedImageInfo contains details about an image that failed quality checks
type FailedImageInfo struct {
	DocumentImageGUID string              `json:"documentimageguid"`
	ImageIndex        int                 `json:"image_index"`
	ImageURI          string              `json:"imageuri"`
	Issues            []ImageQualityIssue `json:"issues"`
}

// extractNameFromNamesArray extracts name from names array (for creditors/debtors)
// Same logic as ShopProfile.GetCompanyName() - prioritize Thai name, fallback to first active name
func extractNameFromNamesArray(doc bson.M) string {
	namesField, exists := doc["names"]
	if !exists {
		return ""
	}

	// Try multiple type assertions for MongoDB compatibility
	var names []interface{}

	// Try []interface{} (standard)
	if n, ok := namesField.([]interface{}); ok {
		names = n
	} else if n, ok := namesField.(bson.A); ok {
		// MongoDB sometimes returns bson.A instead of []interface{}
		names = []interface{}(n)
	} else {
		return ""
	}

	if len(names) == 0 {
		return ""
	}

	// Try to find Thai name first
	for _, nameInterface := range names {
		nameMap, ok := nameInterface.(bson.M)
		if !ok {
			continue
		}
		code, _ := nameMap["code"].(string)
		isDelete, _ := nameMap["isdelete"].(bool)
		name, _ := nameMap["name"].(string)

		if code == "th" && !isDelete && name != "" {
			return name
		}
	}

	// Fallback to first non-deleted name
	for _, nameInterface := range names {
		nameMap, ok := nameInterface.(bson.M)
		if !ok {
			continue
		}
		isDelete, _ := nameMap["isdelete"].(bool)
		name, _ := nameMap["name"].(string)

		if !isDelete && name != "" {
			return name
		}
	}

	return ""
}

// PassedImageInfo contains details about an image that passed quality checks
type PassedImageInfo struct {
	DocumentImageGUID string `json:"documentimageguid"`
	ImageIndex        int    `json:"image_index"`
	ImageURI          string `json:"imageuri"`
	Note              string `json:"note"`
}

// RejectionResponse represents the response when image quality is insufficient
type RejectionResponse struct {
	Status       string            `json:"status"`        // "rejected"
	Reason       string            `json:"reason"`        // "image_quality_insufficient"
	Message      string            `json:"message"`       // Human-readable message
	FailedImages []FailedImageInfo `json:"failed_images"` // Images that failed quality checks
	PassedImages []PassedImageInfo `json:"passed_images"` // Images that passed but can't be processed
	Suggestions  []string          `json:"suggestions"`   // How to improve
	RequestID    string            `json:"request_id"`
	TotalImages  int               `json:"total_images"` // Total number of images submitted
	FailedCount  int               `json:"failed_count"` // Number of images that failed
}

// ImageReference represents an image reference from Azure Blob Storage
type ImageReference struct {
	DocumentImageGUID string `json:"documentimageguid"`
	ImageURI          string `json:"imageuri"`
}

// ExtractRequest represents the new JSON request format
type ExtractRequest struct {
	ShopID          string           `json:"shopid"`
	ImageReferences []ImageReference `json:"imagereferences"`
	Model           string           `json:"model"` // Required: "gemini" or "mistral"
}

// JournalEntry represents an accounting entry
type JournalEntry struct {
	AccountCode     string  `json:"account_code"`
	AccountName     string  `json:"account_name"`
	Debit           float64 `json:"debit"`
	Credit          float64 `json:"credit"`
	Description     string  `json:"description"`
	SelectionReason string  `json:"selection_reason"` // เหตุผลในการเลือกผังบัญชีนี้
	SideReason      string  `json:"side_reason"`      // เหตุผลในการลงฝั่ง debit หรือ credit
}

// ValidateDoubleEntry checks if debits equal credits
func ValidateDoubleEntry(entries []JournalEntry) (bool, float64, float64) {
	var totalDebit, totalCredit float64
	for _, entry := range entries {
		totalDebit += entry.Debit
		totalCredit += entry.Credit
	}

	// Allow small floating point differences (0.01 baht tolerance)
	const tolerance = 0.01
	balanced := (totalDebit-totalCredit) >= -tolerance && (totalDebit-totalCredit) <= tolerance
	return balanced, totalDebit, totalCredit
}

// Helper functions for custom prompts extraction
func extractShopContextForResponse(shopProfile interface{}) string {
	if shopProfile == nil {
		return ""
	}

	// Try multiple type assertions (same as gemini.go)
	switch profile := shopProfile.(type) {
	case bson.M:
		if promptInfo, exists := profile["promptshopinfo"]; exists {
			if promptStr, ok := promptInfo.(string); ok {
				return promptStr
			}
		}
	case map[string]interface{}:
		if promptInfo, exists := profile["promptshopinfo"]; exists {
			if promptStr, ok := promptInfo.(string); ok {
				return promptStr
			}
		}
	case *bson.M:
		if promptInfo, exists := (*profile)["promptshopinfo"]; exists {
			if promptStr, ok := promptInfo.(string); ok {
				return promptStr
			}
		}
	default:
		// Try JSON marshal/unmarshal as fallback
		jsonBytes, err := json.Marshal(shopProfile)
		if err == nil {
			var tempMap map[string]interface{}
			if err := json.Unmarshal(jsonBytes, &tempMap); err == nil {
				if promptInfo, exists := tempMap["promptshopinfo"]; exists {
					if promptStr, ok := promptInfo.(string); ok {
						return promptStr
					}
				}
			}
		}
	}

	return ""
}

func extractTemplateGuidanceForResponse(matchedTemplate *bson.M) string {
	if matchedTemplate == nil {
		return ""
	}

	if promptDesc, exists := (*matchedTemplate)["promptdescription"]; exists {
		if promptStr, ok := promptDesc.(string); ok {
			return promptStr
		}
	}

	return ""
}

// Helper functions for type conversion
func getStringValue(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getFloatValue(m map[string]interface{}, key string) float64 {
	if val, ok := m[key].(float64); ok {
		return val
	}
	return 0.0
}

// downloadImageFromURL downloads an image or PDF from a URL and saves it to a local file
// Returns the detected file extension based on Content-Type
func downloadImageFromURL(imageURL, filename string) (string, error) {
	// Send GET request to download the file
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	// Check if response is successful
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download file: HTTP %d", resp.StatusCode)
	}

	// Detect file type from Content-Type header
	contentType := resp.Header.Get("Content-Type")
	var fileExt string
	switch contentType {
	case "application/pdf":
		fileExt = ".pdf"
	case "image/jpeg", "image/jpg":
		fileExt = ".jpg"
	case "image/png":
		fileExt = ".png"
	default:
		// Fallback: try to detect from URL
		if strings.HasSuffix(strings.ToLower(imageURL), ".pdf") {
			fileExt = ".pdf"
		} else if strings.HasSuffix(strings.ToLower(imageURL), ".png") {
			fileExt = ".png"
		} else {
			fileExt = ".jpg" // default
		}
	}

	// Create the output file
	out, err := os.Create(filename)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Copy the downloaded content to the file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to save file: %w", err)
	}

	return fileExt, nil
}

// --- New Analyze Receipt Handler (Phase 1 Complete Flow, async job) ---

// ImageData is the plain-value shape passed into the background pipeline —
// deliberately independent of gin.Context so runAnalyzePipeline never needs
// to touch anything request-scoped.
type ImageData struct {
	Filename string
	Index    int
	GUID     string
	URI      string
}

// PureOCRImageResult mirrors one image's OCR outcome — used both inside the
// pipeline and by the debug-data assembly at the end.
type PureOCRImageResult struct {
	ImageIndex int
	Result     *ai.SimpleOCRResult
	Tokens     *common.TokenUsage
	Error      error
}

// SubmitAnalyzeReceiptHandler handles POST requests to /api/v1/analyze-receipt.
// It validates the request and required master data synchronously (so bad
// input still gets a clean 400 immediately), then hands the expensive OCR +
// accounting-analysis pipeline to a background goroutine and returns a job
// id right away. Poll GetJobStatusHandler for progress and the final result.
func SubmitAnalyzeReceiptHandler(c *gin.Context) {
	// Step 1: Parse JSON request body
	var req ExtractRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "Invalid request format",
			"details":  err.Error(),
			"expected": "JSON with shopid and imagereferences array",
		})
		return
	}

	// Check for debug mode from query parameter
	debugMode := c.Query("debug") == "true"

	// Validate shopid
	if req.ShopID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "shopid is required",
		})
		return
	}

	// Validate imagereferences
	if len(req.ImageReferences) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "imagereferences array cannot be empty",
		})
		return
	}

	// Validate model (required field)
	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":          "model is required",
			"message":        "กรุณาระบุ OCR provider ที่ต้องการใช้",
			"allowed_values": []string{"gemini", "mistral"},
			"example": map[string]interface{}{
				"shopid": "your_shop_id",
				"model":  "mistral",
				"imagereferences": []map[string]string{
					{"documentimageguid": "guid", "imageuri": "https://..."},
				},
			},
		})
		return
	}

	// Validate model value
	if req.Model != "gemini" && req.Model != "mistral" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":          "invalid model",
			"message":        fmt.Sprintf("Model '%s' ไม่ถูกต้อง กรุณาเลือก 'gemini' หรือ 'mistral'", req.Model),
			"provided_value": req.Model,
			"allowed_values": []string{"gemini", "mistral"},
		})
		return
	}

	// Create request context for tracking
	reqCtx := common.NewRequestContext(req.ShopID)
	reqCtx.LogInfo("🔷 OCR Provider: %s (from request)", req.Model)

	// Log request received with ID for tracking
	reqCtx.LogInfo("🚀 เริ่มรับคำขอใหม่ | ShopID: %s | เวลา: %s", req.ShopID, time.Now().Format("15:04:05"))

	// ⚡ VALIDATE MASTER DATA FIRST (before any AI processing)
	// This saves tokens and processing time if master data is missing
	masterCache, err := storage.GetOrLoadMasterData(req.ShopID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":      "Failed to load master data",
			"details":    err.Error(),
			"request_id": reqCtx.RequestID,
		})
		return
	}

	// Check if master data exists
	if len(masterCache.Accounts) == 0 || len(masterCache.JournalBooks) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"error":   "master_data_not_found",
			"message": "ไม่พบข้อมูล Master Data สำหรับ Shop นี้ กรุณาตั้งค่าผังบัญชี (Chart of Accounts) และสมุดรายวัน (Journal Books) ใน MongoDB ก่อนใช้งาน",
			"details": map[string]interface{}{
				"shopid":              req.ShopID,
				"accounts_found":      len(masterCache.Accounts),
				"journal_books_found": len(masterCache.JournalBooks),
				"creditors_found":     len(masterCache.Creditors),
			},
			"required": map[string]interface{}{
				"chart_of_accounts": "ต้องมีอย่างน้อย 1 รายการ",
				"journal_books":     "ต้องมีอย่างน้อย 1 รายการ",
				"creditors":         "ไม่บังคับ (optional)",
			},
			"request_id": reqCtx.RequestID,
		})
		return
	}

	reqCtx.LogInfo("✓ Master data validated: %d accounts, %d journal books, %d creditors, %d debtors",
		len(masterCache.Accounts), len(masterCache.JournalBooks), len(masterCache.Creditors), len(masterCache.Debtors))

	// Document templates now come from masterCache (5-min TTL cache, same as
	// accounts/journal books/creditors/debtors) instead of a fresh Mongo
	// query on every request — see storage.GetDocumentFormate.
	documentTemplates := masterCache.DocumentTemplates
	reqCtx.LogInfo("✓ Document templates loaded: %d templates found", len(documentTemplates))

	// Everything validated and loaded synchronously above — from here on,
	// the pipeline runs detached in a background goroutine. job.Complete()
	// stores the exact same response shape this handler used to write
	// directly via c.JSON(200, ...); job.Fail() stores the same error shapes
	// that used to be written via c.JSON(4xx/5xx, ...).
	job, token, err := jobs.Create(reqCtx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "too_many_active_jobs",
			"message": "ระบบกำลังประมวลผลงานจำนวนมาก กรุณาลองใหม่อีกครั้งในอีกสักครู่",
		})
		return
	}

	go runAnalyzePipeline(job, req, debugMode, masterCache, documentTemplates)

	c.JSON(http.StatusAccepted, gin.H{
		"status": "processing",
		"job": gin.H{
			"id":            job.ID,
			"token":         token,
			"progress":      job.Snapshot().Progress,
			"poll_after_ms": 2000,
		},
	})
}

// GetJobStatusHandler handles GET requests to /api/v1/jobs/:id. While the
// job is still processing it returns the same 202+progress envelope as the
// submit endpoint; once finished it returns the exact response body the
// synchronous handler used to send directly (200), or a structured error
// (422/408) if the pipeline failed.
func GetJobStatusHandler(c *gin.Context) {
	id := c.Param("id")
	token := c.GetHeader("X-Job-Token")

	job, ok := jobs.Get(id, token)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "job_not_found",
			"message": "ไม่พบงานหรือสิทธิ์เข้าถึงหมดอายุ",
		})
		return
	}

	snap := job.Snapshot()

	switch snap.Status {
	case jobs.StatusCompleted:
		c.JSON(http.StatusOK, snap.Result)
	case jobs.StatusFailed:
		status := http.StatusUnprocessableEntity
		if snap.Error != nil && snap.Error.Code == "PROCESSING_TIMEOUT" {
			status = http.StatusRequestTimeout
		}
		c.JSON(status, gin.H{
			"error":      snap.Error.Code,
			"message":    snap.Error.Message,
			"request_id": job.ID,
		})
	default:
		c.JSON(http.StatusAccepted, gin.H{
			"status": "processing",
			"job": gin.H{
				"id":            job.ID,
				"state":         string(snap.Status),
				"progress":      snap.Progress,
				"poll_after_ms": 2000,
			},
		})
	}
}

// runAnalyzePipeline runs the full OCR + accounting-analysis pipeline for
// one job. It must never touch *gin.Context — every input it needs was
// already extracted into plain Go values by SubmitAnalyzeReceiptHandler
// before this was spawned. Every early-return that used to write an HTTP
// error response now calls job.Fail(...) instead; the final success path
// calls job.Complete(...) with the same response body shape as before.
func runAnalyzePipeline(job *jobs.Job, req ExtractRequest, debugMode bool, masterCache *storage.MasterDataCache, documentTemplates []bson.M) {
	reqCtx := job.ReqCtx

	// 5 minutes max for very complex receipts — this is now a deadline
	// checked from inside the goroutine itself, not an HTTP-server-level
	// timeout, since no client connection is being held open anymore.
	deadline := reqCtx.StartTime.Add(5 * time.Minute)

	// Step 2: Download ALL images from Azure Blob Storage
	job.UpdateProgress(5, "download", "กำลังดาวน์โหลดรูปภาพ")
	reqCtx.StartStep("download_images")
	reqCtx.LogInfo("Downloading %d image(s)", len(req.ImageReferences))

	var downloadedImages []ImageData

	for i, imgRef := range req.ImageReferences {
		if imgRef.ImageURI == "" {
			reqCtx.EndStep("failed", nil, fmt.Errorf("imageuri is required in imagereferences[%d]", i))
			job.Fail("invalid_request", fmt.Sprintf("imageuri is required in imagereferences[%d]", i))
			return
		}

		// Generate temporary filename (extension will be set after download)
		uniqueID := uuid.New().String()
		tempFilename := filepath.Join(configs.UPLOAD_DIR, fmt.Sprintf("%s_%d.tmp", uniqueID, i))

		// Download file from Azure Blob Storage (supports images and PDFs)
		fileExt, err := downloadImageFromURL(imgRef.ImageURI, tempFilename)
		if err != nil {
			reqCtx.EndStep("failed", nil, err)
			job.Fail("download_failed", fmt.Sprintf("Failed to download file from Azure Blob Storage: %s", err.Error()))
			return
		}

		// Rename file with correct extension
		finalFilename := filepath.Join(configs.UPLOAD_DIR, fmt.Sprintf("%s_%d%s", uniqueID, i, fileExt))
		if err := os.Rename(tempFilename, finalFilename); err != nil {
			os.Remove(tempFilename) // cleanup
			reqCtx.EndStep("failed", nil, err)
			job.Fail("save_failed", fmt.Sprintf("Failed to save downloaded file: %s", err.Error()))
			return
		}

		reqCtx.LogInfo("Downloaded file %d: %s (type: %s)", i, filepath.Base(finalFilename), fileExt)

		downloadedImages = append(downloadedImages, ImageData{
			Filename: finalFilename,
			Index:    i,
			GUID:     imgRef.DocumentImageGUID,
			URI:      imgRef.ImageURI,
		})
	}

	reqCtx.LogInfo("✓ Downloaded %d image(s) successfully", len(downloadedImages))
	reqCtx.EndStep("success", nil, nil)

	// Auto-cleanup all downloaded files
	defer func() {
		for _, img := range downloadedImages {
			if err := os.Remove(img.Filename); err != nil {
				reqCtx.LogWarning("Failed to delete temporary file %s: %v", img.Filename, err)
			}
		}
	}()

	// Step 3: Process PURE OCR for ALL images (NEW OPTIMIZED VERSION)
	// Changed from full structured extraction to raw text only - saves ~25,000 tokens per image!
	job.UpdateProgress(10, "ocr", "กำลังอ่านข้อความจากเอกสาร")
	reqCtx.StartStep("pure_ocr_extraction_all")
	reqCtx.LogInfo("Pure OCR extraction (raw text only) for %d image(s)", len(downloadedImages))

	if time.Now().After(deadline) {
		reqCtx.EndStep("cancelled", nil, fmt.Errorf("timeout before pure OCR"))
		job.Fail("PROCESSING_TIMEOUT", "Receipt is too complex and processing exceeded 5 minutes. Please try with a clearer or simpler receipt image.")
		return
	}

	var pureOCRResults []PureOCRImageResult
	var totalPureOCRTokens common.TokenUsage

	// ⚡ PARALLEL PROCESSING: Process all images concurrently
	type ocrJob struct {
		img   ImageData
		index int
	}

	resultsChan := make(chan PureOCRImageResult, len(downloadedImages))
	jobsChan := make(chan ocrJob, len(downloadedImages))

	// Start worker goroutines. The Gemini rate limiter (internal/ratelimit)
	// already caps total API request rate process-wide, independent of how
	// many of these workers run concurrently — so this only controls how
	// many images' network+inference latency can overlap within a single
	// request, not how fast Gemini calls actually go out. Configurable via
	// OCR_WORKER_COUNT (default 3); never spawn more workers than images.
	numWorkers := configs.OCR_WORKER_COUNT
	if numWorkers > len(downloadedImages) {
		numWorkers = len(downloadedImages)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	// Create OCR provider based on request model (gemini or mistral)
	ocrProvider, err := ai.CreateOCRProvider(req.Model)
	if err != nil {
		reqCtx.LogError("Failed to create OCR provider: %v", err)
		job.Fail("ocr_provider_init_failed", fmt.Sprintf("OCR provider initialization failed: %s", err.Error()))
		return
	}

	for w := 0; w < numWorkers; w++ {
		go func() {
			for job := range jobsChan {
				// For Mistral: use original URL if available, otherwise use local file
				// For Gemini: always use local file
				imagePath := job.img.Filename
				if ocrProvider.GetProviderName() == "mistral" && job.img.URI != "" {
					imagePath = job.img.URI
				}

				result, pureOCRTokens, err := ocrProvider.ProcessPureOCR(imagePath, reqCtx)
				resultsChan <- PureOCRImageResult{
					ImageIndex: job.img.Index,
					Result:     result,
					Tokens:     pureOCRTokens,
					Error:      err,
				}
			}
		}()
	}

	// Send jobs
	for _, img := range downloadedImages {
		jobsChan <- ocrJob{img: img, index: img.Index}
	}
	close(jobsChan)

	// Collect results
	resultsMap := make(map[int]PureOCRImageResult)
	for i := 0; i < len(downloadedImages); i++ {
		res := <-resultsChan
		resultsMap[res.ImageIndex] = res
	}
	close(resultsChan)

	// Process results in original order
	for _, img := range downloadedImages {
		res := resultsMap[img.Index]
		result := res.Result
		pureOCRTokens := res.Tokens
		err := res.Error

		if err != nil {
			reqCtx.LogWarning("⚠️  Image %d Pure OCR failed: %v", img.Index, err)
			// Note: Enhanced fixJSONEscaping() should handle most complex documents now
			// Continue with other images even if one fails
		}

		// Basic validation: check if we got text
		if result != nil && result.RawDocumentText == "" {
			reqCtx.LogWarning("⚠️  Image %d - No text extracted (blank or unreadable image)", img.Index)
		}

		pureOCRResults = append(pureOCRResults, PureOCRImageResult{
			ImageIndex: img.Index,
			Result:     result,
			Tokens:     pureOCRTokens,
			Error:      err,
		})

		if pureOCRTokens != nil {
			totalPureOCRTokens.InputTokens += pureOCRTokens.InputTokens
			totalPureOCRTokens.OutputTokens += pureOCRTokens.OutputTokens
			totalPureOCRTokens.TotalTokens += pureOCRTokens.TotalTokens
			totalPureOCRTokens.CostUSD += pureOCRTokens.CostUSD
			totalPureOCRTokens.CostTHB += pureOCRTokens.CostTHB
		}
	}

	reqCtx.LogInfo("✓ Pure OCR completed for %d image(s) - Token savings: ~82%% vs old method", len(pureOCRResults))

	// 🔍 DEBUG: Log pure OCR results (only when debug=true)
	if debugMode {
		reqCtx.LogInfo("📋 DEBUG: Pure OCR Results Overview:")
		for i, ocrResult := range pureOCRResults {
			if ocrResult.Result != nil {
				// Show first 500 chars of raw text
				rawText := ocrResult.Result.RawDocumentText
				if len(rawText) > 500 {
					rawText = rawText[:500] + "..."
				}
				reqCtx.LogInfo("Image %d Raw Text:\n%s", i, rawText)
			}
		}
	}

	reqCtx.EndStep("success", &totalPureOCRTokens, nil)

	// Step 3.5: Template Matching Analysis (NEW SMART OPTIMIZATION)
	// Analyze raw text to see if it matches any predefined accounting template
	// If match found (≥TEMPLATE_CONFIDENCE_THRESHOLD) → Use template-only mode (saves another ~20,000 tokens in Phase 3!)
	job.UpdateProgress(45, "template_match", "กำลังจับคู่รูปแบบบัญชี")
	reqCtx.StartStep("template_matching_analysis")
	reqCtx.LogInfo("Analyzing text to find matching accounting templates...")

	// Combine all raw text from all images for comprehensive matching
	var combinedText string
	for _, ocrResult := range pureOCRResults {
		if ocrResult.Result != nil {
			combinedText += ocrResult.Result.RawDocumentText + "\n\n"
		}
	}

	// Run template matching
	templateMatchResult := processor.AnalyzeTemplateMatch(combinedText, documentTemplates, reqCtx)

	var masterDataMode ai.MasterDataMode
	var matchedTemplate *bson.M

	if templateMatchResult.Confidence >= configs.TEMPLATE_CONFIDENCE_THRESHOLD && templateMatchResult.Template != nil {
		// 🎯 TEMPLATE MATCHED - Use optimized path
		masterDataMode = ai.TemplateOnlyMode
		matchedTemplate = &templateMatchResult.Template
		reqCtx.LogInfo("✅ Template matched: %s (ID: %v, Confidence: %.1f%%) - Using template-only mode",
			templateMatchResult.Description,
			templateMatchResult.TemplateID,
			templateMatchResult.Confidence)
	} else {
		// ❌ NO TEMPLATE MATCH - Use full master data
		masterDataMode = ai.FullMode
		matchedTemplate = nil
		reqCtx.LogInfo("❌ No template match (Confidence: %.1f%% < %.0f%%) - Using full master data mode",
			templateMatchResult.Confidence,
			configs.TEMPLATE_CONFIDENCE_THRESHOLD)
	}

	// 🔥 Track template matching token usage
	reqCtx.EndStep("success", templateMatchResult.TokenUsage, nil)

	// Step 5: Prepare master data (already validated and loaded at the beginning)
	job.UpdateProgress(55, "prepare_master_data", "กำลังเตรียมข้อมูลผังบัญชี")
	reqCtx.StartStep("prepare_master_data")

	// Filter accounts: Send only Level 3-5 (exclude Level 1-2 headers)
	// Level 1-2 = top-level categories (สินทรัพย์, หนี้สิน)
	// Level 3-5 = actual accounts used in journal entries
	var filteredAccounts []bson.M
	for _, acc := range masterCache.Accounts {
		if accountLevel, ok := acc["accountlevel"].(int32); ok {
			if accountLevel >= 3 {
				filteredAccounts = append(filteredAccounts, acc)
			}
		} else if accountLevel, ok := acc["accountlevel"].(int64); ok {
			if accountLevel >= 3 {
				filteredAccounts = append(filteredAccounts, acc)
			}
		} else if accountLevel, ok := acc["accountlevel"].(float64); ok {
			if accountLevel >= 3 {
				filteredAccounts = append(filteredAccounts, acc)
			}
		}
	}

	// Compress JSON: Send only essential fields to reduce tokens
	var compressedAccounts []bson.M
	for _, acc := range filteredAccounts {
		compressedAccounts = append(compressedAccounts, bson.M{
			"accountcode": acc["accountcode"],
			"accountname": acc["accountname"],
		})
	}

	var compressedJournalBooks []bson.M
	for _, jb := range masterCache.JournalBooks {
		compressedJournalBooks = append(compressedJournalBooks, bson.M{
			"code":  jb["code"],
			"name1": jb["name1"],
		})
	}

	var compressedCreditors []bson.M
	for _, cr := range masterCache.Creditors {
		compressedCreditors = append(compressedCreditors, bson.M{
			"code": cr["code"],
			"name": extractNameFromNamesArray(cr),
		})
	}

	var compressedDebtors []bson.M
	for _, db := range masterCache.Debtors {
		compressedDebtors = append(compressedDebtors, bson.M{
			"code": db["code"],
			"name": extractNameFromNamesArray(db),
		})
	}

	accounts := compressedAccounts
	journalBooks := compressedJournalBooks
	creditors := compressedCreditors
	debtors := compressedDebtors

	reqCtx.LogInfo("✓ Master data ready: %d accounts (filtered from %d), %d journal books, %d creditors, %d debtors",
		len(accounts), len(masterCache.Accounts), len(journalBooks), len(creditors), len(debtors))
	reqCtx.EndStep("success", nil, nil)

	// Step 5.5: Pre-match vendors using fuzzy matching (before sending to AI)
	job.UpdateProgress(60, "vendor_match", "กำลังค้นหาคู่ค้า")
	reqCtx.LogInfo("\n┌── vendor_pre_matching")
	var suggestedVendorCode string
	var suggestedVendorName string
	var matchMethod string
	var matchSimilarity float64

	// Initialize vendorMatchResult with empty values
	vendorMatchResult := processor.VendorMatchResult{
		Found:      false,
		Code:       "",
		Name:       "",
		Similarity: 0,
		Method:     "not_found",
	}

	// Try to extract vendor info from first OCR result
	if len(pureOCRResults) > 0 && pureOCRResults[0].Result != nil {
		ocrResult := pureOCRResults[0].Result
		vendorNameFromOCR := ""
		taxIDFromOCR := ""

		// Extract vendor info from raw text (simple heuristic)
		// First non-empty line is usually the vendor name
		rawText := ocrResult.RawDocumentText
		lines := strings.Split(rawText, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && len(trimmed) > 5 {
				vendorNameFromOCR = trimmed
				break
			}
		}

		// Perform fuzzy matching
		if vendorNameFromOCR != "" || taxIDFromOCR != "" {
			vendorMatchResult = processor.MatchVendor(vendorNameFromOCR, masterCache.Creditors, taxIDFromOCR)
			if vendorMatchResult.Found {
				suggestedVendorCode = vendorMatchResult.Code
				suggestedVendorName = vendorMatchResult.Name
				matchMethod = vendorMatchResult.Method
				matchSimilarity = vendorMatchResult.Similarity

				reqCtx.LogInfo("✅ Vendor matched: '%s' → '%s' (code: %s, method: %s, %.1f%%)",
					vendorNameFromOCR, suggestedVendorName, suggestedVendorCode, matchMethod, matchSimilarity)
			} else {
				reqCtx.LogInfo("⚠️  No vendor match found for: '%s'", vendorNameFromOCR)
			}
		}
	}
	reqCtx.LogInfo("└── ✅ สำเร็จ")

	// Step 6: Phase 3 - AI Multi-Image Accounting Analysis (with conditional master data loading)
	job.UpdateProgress(65, "accounting_analysis", "กำลังวิเคราะห์รายการบัญชี")
	reqCtx.StartStep("phase3_multi_image_accounting")
	reqCtx.LogInfo("Analyzing relationships between %d image(s) - Mode: %s", len(pureOCRResults), masterDataMode)

	if time.Now().After(deadline) {
		reqCtx.EndStep("cancelled", &totalPureOCRTokens, fmt.Errorf("timeout before accounting analysis"))
		job.Fail("PROCESSING_TIMEOUT", "Receipt is too complex and processing exceeded 5 minutes. Please try with a clearer or simpler receipt image.")
		return
	}

	// Process multi-image accounting analysis with conditional master data
	accountingJSON, phase3Tokens, err := ai.ProcessMultiImageAccountingAnalysis(
		downloadedImages,
		pureOCRResults,
		masterDataMode,
		matchedTemplate,
		accounts,
		journalBooks,
		creditors,
		debtors,
		masterCache.ShopProfile,
		documentTemplates,
		&vendorMatchResult,
		reqCtx,
	)
	if err != nil {
		reqCtx.EndStep("failed", phase3Tokens, err)
		job.Fail("accounting_analysis_failed", fmt.Sprintf("Accounting analysis failed: %s", err.Error()))
		return
	}
	reqCtx.EndStep("success", phase3Tokens, nil)

	// Parse accounting JSON
	var accountingResponse map[string]interface{}
	if err := json.Unmarshal([]byte(accountingJSON), &accountingResponse); err != nil {
		job.Fail("parse_failed", fmt.Sprintf("Failed to parse accounting response: %s", err.Error()))
		return
	}

	// Step 7: Validate double-entry balance
	if accountingEntry, ok := accountingResponse["accounting_entry"].(map[string]interface{}); ok {
		if entriesRaw, ok := accountingEntry["entries"].([]interface{}); ok {
			// Convert to JournalEntry slice for validation
			entries := []JournalEntry{}
			for _, e := range entriesRaw {
				if entryMap, ok := e.(map[string]interface{}); ok {
					entry := JournalEntry{
						AccountCode:     getStringValue(entryMap, "account_code"),
						AccountName:     getStringValue(entryMap, "account_name"),
						Debit:           getFloatValue(entryMap, "debit"),
						Credit:          getFloatValue(entryMap, "credit"),
						Description:     getStringValue(entryMap, "description"),
						SelectionReason: getStringValue(entryMap, "selection_reason"),
						SideReason:      getStringValue(entryMap, "side_reason"),
					}
					entries = append(entries, entry)
				}
			}

			// Validate and add balance check
			balanced, totalDebit, totalCredit := ValidateDoubleEntry(entries)
			accountingEntry["balance_check"] = map[string]interface{}{
				"balanced":     balanced,
				"total_debit":  totalDebit,
				"total_credit": totalCredit,
			}

			// Step 7.4: If unbalanced, try a narrow self-verification pass before
			// confidence scoring runs (Step 7.6 reads accounting_entry["balance_check"]
			// fresh, so correcting it here propagates automatically with no other
			// changes needed downstream). Best-effort only — never fails the request.
			if !balanced {
				job.UpdateProgress(80, "verifying_balance", "กำลังตรวจสอบยอดดุลบัญชี")
				reqCtx.StartStep("verify_balance")

				verifyInput := make([]ai.VerifyEntry, len(entries))
				for i, e := range entries {
					verifyInput[i] = ai.VerifyEntry{
						AccountCode:     e.AccountCode,
						AccountName:     e.AccountName,
						Debit:           e.Debit,
						Credit:          e.Credit,
						SelectionReason: e.SelectionReason,
						SideReason:      e.SideReason,
					}
				}

				correctedVerify, verifyTokens, verifyErr := ai.VerifyAndCorrectBalance(verifyInput, reqCtx)
				if verifyErr != nil {
					// Existing validation/frontend guard still catches the imbalance downstream.
					reqCtx.LogWarning("⚠️ Balance verification failed, keeping original entries: %v", verifyErr)
					reqCtx.EndStep("failed", verifyTokens, verifyErr)
				} else if !ai.EntriesShapeMatches(verifyInput, correctedVerify) {
					// Model rewrote structure (different count, different accounts, or a
					// Dr/Cr side flip) instead of just fixing a number — untrustworthy,
					// reject the whole correction rather than accept a partial/guessed fix.
					reqCtx.LogWarning("⚠️ Balance verification returned a different entry shape — discarding, keeping original entries")
					reqCtx.EndStep("rejected_shape_mismatch", verifyTokens, nil)
				} else {
					reqCtx.EndStep("success", verifyTokens, nil)

					correctedEntries := make([]JournalEntry, len(entries))
					for i, e := range entries {
						correctedEntries[i] = e
						correctedEntries[i].Debit = correctedVerify[i].Debit
						correctedEntries[i].Credit = correctedVerify[i].Credit
					}

					newBalanced, newTotalDebit, newTotalCredit := ValidateDoubleEntry(correctedEntries)
					if newBalanced {
						reqCtx.LogInfo("✅ Balance verification corrected the imbalance")
					} else {
						reqCtx.LogWarning("⚠️ Balance verification ran but entries still unbalanced — keeping its output for review")
					}

					// Write corrected debit/credit back into the original entriesRaw maps
					// (mutate in place — entriesRaw elements are accountingEntry["entries"]'s
					// own []interface{} maps, same objects the final response serializes).
					for i, corrected := range correctedVerify {
						if entryMap, ok := entriesRaw[i].(map[string]interface{}); ok {
							entryMap["debit"] = corrected.Debit
							entryMap["credit"] = corrected.Credit
						}
					}
					accountingEntry["balance_check"] = map[string]interface{}{
						"balanced":     newBalanced,
						"total_debit":  newTotalDebit,
						"total_credit": newTotalCredit,
					}
					accountingEntry["balance_verification"] = map[string]interface{}{
						"attempted": true,
						"corrected": newBalanced,
					}
				}
			}
		}
	}

	// Step 7.5: Fill creditor/debtor info from multiple sources
	var accountingEntry map[string]interface{}
	if ae, ok := accountingResponse["accounting_entry"].(map[string]interface{}); ok {
		accountingEntry = ae
	} else {
		accountingEntry = map[string]interface{}{}
	}

	// Priority 1: Pre-matched vendor from Backend (vendor_pre_matching)
	if vendorMatchResult.Found {
		accountingEntry["creditor_code"] = vendorMatchResult.Code
		accountingEntry["creditor_name"] = vendorMatchResult.Name
		reqCtx.LogInfo("✅ Auto-filled creditor from vendor_pre_matching: %s (code: %s)",
			vendorMatchResult.Name, vendorMatchResult.Code)
	} else {
		// Priority 2: AI-matched creditor from Phase 3 (from creditor/debtor objects)
		if creditorObj, ok := accountingResponse["creditor"].(map[string]interface{}); ok {
			if code := getStringValue(creditorObj, "creditor_code"); code != "" {
				accountingEntry["creditor_code"] = code
				accountingEntry["creditor_name"] = getStringValue(creditorObj, "creditor_name")
				reqCtx.LogInfo("✅ Auto-filled creditor from AI Phase 3: %s (code: %s)",
					accountingEntry["creditor_name"], code)
			}
		}

		if debtorObj, ok := accountingResponse["debtor"].(map[string]interface{}); ok {
			if code := getStringValue(debtorObj, "debtor_code"); code != "" {
				accountingEntry["debtor_code"] = code
				accountingEntry["debtor_name"] = getStringValue(debtorObj, "debtor_name")
				reqCtx.LogInfo("✅ Auto-filled debtor from AI Phase 3: %s (code: %s)",
					accountingEntry["debtor_name"], code)
			}
		}
	}

	// Step 7.6: Calculate weighted confidence score
	job.UpdateProgress(85, "confidence", "กำลังตรวจสอบความถูกต้อง")
	reqCtx.StartStep("calculate_confidence")
	confidenceResult := processor.CalculateWeightedConfidence(
		&templateMatchResult,
		&vendorMatchResult,
		accountingEntry,
		reqCtx,
	)

	// Replace AI's confidence with calculated weighted confidence
	validationData := map[string]interface{}{
		"confidence": map[string]interface{}{
			"level": confidenceResult.OverallLevel,
			"score": confidenceResult.OverallScore,
		},
		"requires_review": confidenceResult.RequiresReview,
		"confidence_breakdown": map[string]interface{}{
			"factors": map[string]interface{}{
				"template_match":     confidenceResult.Factors.TemplateMatch,
				"party_match":        confidenceResult.Factors.PartyMatch,
				"data_completeness":  confidenceResult.Factors.DataCompleteness,
				"field_validation":   confidenceResult.Factors.FieldValidation,
				"balance_validation": confidenceResult.Factors.BalanceValidation,
			},
			"explanations": confidenceResult.Breakdown,
			"weights": map[string]interface{}{
				"template_match":     processor.DefaultWeights.TemplateMatch * 100,
				"party_match":        processor.DefaultWeights.PartyMatch * 100,
				"data_completeness":  processor.DefaultWeights.DataCompleteness * 100,
				"field_validation":   processor.DefaultWeights.FieldValidation * 100,
				"balance_validation": processor.DefaultWeights.BalanceValidation * 100,
			},
			"calculation": map[string]interface{}{
				"formula": "(เทมเพลต×30%) + (คู่ค้า×25%) + (ข้อมูล×20%) + (ฟิลด์×15%) + (ยอดเงิน×10%)",
				"steps": []string{
					fmt.Sprintf("เทมเพลต: %.0f × 30%% = %.1f", confidenceResult.Factors.TemplateMatch, confidenceResult.Factors.TemplateMatch*0.3),
					fmt.Sprintf("คู่ค้า: %.0f × 25%% = %.1f", confidenceResult.Factors.PartyMatch, confidenceResult.Factors.PartyMatch*0.25),
					fmt.Sprintf("ข้อมูล: %.0f × 20%% = %.1f", confidenceResult.Factors.DataCompleteness, confidenceResult.Factors.DataCompleteness*0.2),
					fmt.Sprintf("ฟิลด์: %.0f × 15%% = %.1f", confidenceResult.Factors.FieldValidation, confidenceResult.Factors.FieldValidation*0.15),
					fmt.Sprintf("ยอดเงิน: %.0f × 10%% = %.1f", confidenceResult.Factors.BalanceValidation, confidenceResult.Factors.BalanceValidation*0.1),
				},
				"total": confidenceResult.OverallScore,
			},
		},
		"review_requirements": generateReviewRequirements(confidenceResult, accountingEntry),
	}

	// Merge with existing validation data from AI (keep ai_explanation, etc.)
	if existingValidation, ok := accountingResponse["validation"].(map[string]interface{}); ok {
		// Keep AI's explanation but override confidence and requires_review
		validationData["ai_explanation"] = existingValidation["ai_explanation"]
		validationData["processing_notes"] = existingValidation["processing_notes"]
		validationData["fields_requiring_review"] = existingValidation["fields_requiring_review"]

		// Override AI's vendor_matching with Backend's result
		if aiExplanation, ok := existingValidation["ai_explanation"].(map[string]interface{}); ok {
			if vendorMatchResult.Found {
				aiExplanation["vendor_matching"] = map[string]interface{}{
					"found_in_document": vendorMatchResult.Name,
					"matched_with":      vendorMatchResult.Code + " - " + vendorMatchResult.Name,
					"matching_method":   vendorMatchResult.Method,
					"confidence":        vendorMatchResult.Similarity,
					"reason":            fmt.Sprintf("ระบบจับคู่ vendor สำเร็จด้วยวิธี %s (ความแม่นยำ %.1f%%)", vendorMatchResult.Method, vendorMatchResult.Similarity),
				}
			} else {
				// Keep AI's not_found explanation
			}
			validationData["ai_explanation"] = aiExplanation
		}
	}

	accountingResponse["validation"] = validationData
	reqCtx.EndStep("success", nil, nil)

	// Step 8: Extract data safely (no draft saving)
	// Re-extract accountingEntry after confidence calculation
	if ae, ok := accountingResponse["accounting_entry"].(map[string]interface{}); ok {
		accountingEntry = ae

		// 🔥 CRITICAL: Validate creditor/debtor codes against master data
		creditorCode := getStringValue(accountingEntry, "creditor_code")
		debtorCode := getStringValue(accountingEntry, "debtor_code")

		if creditorCode != "" {
			found := false
			for _, creditor := range masterCache.Creditors {
				if code, ok := creditor["code"].(string); ok && code == creditorCode {
					found = true
					break
				}
			}
			if !found {
				reqCtx.LogWarning("⚠️  AI ส่ง creditor_code '%s' ที่ไม่มีในฐานข้อมูล → เปลี่ยนเป็น Unknown", creditorCode)
				accountingEntry["creditor_code"] = ""
				accountingEntry["creditor_name"] = ""
			}
		}

		if debtorCode != "" {
			found := false
			for _, debtor := range masterCache.Debtors {
				if code, ok := debtor["code"].(string); ok && code == debtorCode {
					found = true
					break
				}
			}
			if !found {
				reqCtx.LogWarning("⚠️  AI ส่ง debtor_code '%s' ที่ไม่มีในฐานข้อมูล → เปลี่ยนเป็น Unknown", debtorCode)
				accountingEntry["debtor_code"] = ""
				accountingEntry["debtor_name"] = ""
			}
		}

		// 🔥 CRITICAL: Validate template usage - check if all accounts are used
		if matchedTemplate != nil {
			if details, ok := (*matchedTemplate)["details"].(bson.A); ok && len(details) > 0 {
				entriesRaw, _ := accountingEntry["entries"].([]interface{})
				if len(entriesRaw) < len(details) {
					reqCtx.LogWarning("⚠️  Template has %d accounts but AI only used %d → Missing accounts!", len(details), len(entriesRaw))
				}
			}
		}
	} else {
		accountingEntry = map[string]interface{}{}
	}

	// Step 9: Prepare debug data if requested
	var debugData map[string]interface{}
	if debugMode {
		// Include pure OCR results in response for debugging
		ocrDebugData := []map[string]interface{}{}
		for i, ocrResult := range pureOCRResults {
			if ocrResult.Result != nil {
				ocrDebugData = append(ocrDebugData, map[string]interface{}{
					"image_index": i,
					"ocr_result":  ocrResult.Result,
				})
			}
		}
		debugData = map[string]interface{}{
			"pure_ocr_results": ocrDebugData,
			"note":             "Debug mode enabled - showing pure OCR extraction data (raw text only)",
			"template_match":   templateMatchResult,
		}
	}

	// Step 10: Check if we timed out during processing
	if time.Now().After(deadline) {
		// Timeout occurred, but we finished anyway - continue and store the
		// result normally; the client's poll loop already gave up waiting
		// past its own budget if it was going to, but a late-but-complete
		// result is still better than none.
		reqCtx.LogWarning("⚠️  Processing completed after the 5-minute soft deadline")
	}

	// Step 9: Build multi-image response with document analysis
	summary := reqCtx.GetSummary()

	// Extract document analysis if available
	var documentAnalysis map[string]interface{}
	if da, ok := accountingResponse["document_analysis"].(map[string]interface{}); ok {
		documentAnalysis = da
	} else {
		// Default analysis for single image
		documentAnalysis = map[string]interface{}{
			"total_images": len(downloadedImages),
			"relationship": "single_document",
			"confidence":   95,
		}
	}

	// Extract source images info if available
	var sourceImages []interface{}
	if si, ok := accountingResponse["source_images"].([]interface{}); ok {
		sourceImages = si
	}

	// Extract template information (which template AI used and why)
	templateInfo := processor.ExtractTemplateInfo(accountingResponse, documentTemplates, matchedTemplate, reqCtx)

	// Get primary receipt data from accounting response (Pure OCR doesn't extract structured data)
	var receiptData map[string]interface{}
	if rd, ok := accountingResponse["receipt"].(map[string]interface{}); ok {
		receiptData = rd
	} else {
		// Pure OCR only has raw text, so accounting response should provide structured data
		// If missing, use minimal fallback
		receiptData = gin.H{
			"number":        "N/A",
			"date":          "N/A",
			"vendor_name":   "N/A", // All info comes from Phase 3 accounting analysis
			"vendor_tax_id": "N/A",
			"total":         0,
			"vat":           0,
		}
	}

	// Priority 1: Add fields_requiring_review array
	fieldsRequiringReview := []string{}
	if receiptData != nil {
		if vendorName, ok := receiptData["vendor_name"].(string); ok && (vendorName == "Unknown Vendor" || vendorName == "N/A" || vendorName == "") {
			fieldsRequiringReview = append(fieldsRequiringReview, "vendor_name")
		}
		if vendorTaxID, ok := receiptData["vendor_tax_id"].(string); ok && (vendorTaxID == "Unknown Vendor" || vendorTaxID == "N/A" || vendorTaxID == "") {
			fieldsRequiringReview = append(fieldsRequiringReview, "vendor_tax_id")
		}
	}
	if len(fieldsRequiringReview) > 0 {
		validationData["fields_requiring_review"] = fieldsRequiringReview
		if requiresReview, ok := validationData["requires_review"].(bool); !ok || !requiresReview {
			validationData["requires_review"] = true
		}
	}

	// Collect OCR warnings from all processed images
	var ocrWarnings []gin.H
	for i, ocrResult := range pureOCRResults {
		// Case 1: OCR succeeded with warnings
		if ocrResult.Result != nil && (ocrResult.Result.IsPartial || ocrResult.Result.FallbackUsed || ocrResult.Result.Warning != "") {
			warning := gin.H{
				"image_index": i,
			}
			if ocrResult.Result.IsPartial {
				warning["is_partial"] = true
			}
			if ocrResult.Result.FallbackUsed {
				warning["fallback_used"] = true
			}
			if ocrResult.Result.Warning != "" {
				warning["warning"] = ocrResult.Result.Warning
			}
			if ocrResult.Result.TextLength > 0 {
				warning["text_length"] = ocrResult.Result.TextLength
			}
			ocrWarnings = append(ocrWarnings, warning)
		} else if ocrResult.Error != nil {
			// Case 2: OCR failed completely
			warning := gin.H{
				"image_index": i,
				"error":       "OCR extraction failed",
				"details":     ocrResult.Error.Error(),
			}
			ocrWarnings = append(ocrWarnings, warning)
		}
	}

	// Build metadata with OCR warnings if any
	// Separate Mistral OCR usage from Gemini AI processing
	metadata := gin.H{
		"request_id":       reqCtx.RequestID,
		"processed_at":     time.Now().Format(time.RFC3339),
		"duration_sec":     summary["total_duration_sec"],
		"images_processed": len(downloadedImages),
	}

	// Add OCR provider info and breakdown
	ocrProviderName := "gemini" // default
	if ocrProvider != nil {
		ocrProviderName = ocrProvider.GetProviderName()
	}

	if ocrProviderName == "mistral" {
		// Mistral: Show separate OCR and AI processing costs
		metadata["ocr_provider"] = "mistral"
		metadata["token_usage"] = gin.H{
			"ocr_usage": gin.H{
				"provider":        "mistral",
				"pages_processed": totalPureOCRTokens.InputTokens, // pages stored as input_tokens
				"cost_thb":        fmt.Sprintf("฿%.2f", totalPureOCRTokens.CostTHB),
				"cost_usd":        fmt.Sprintf("$%.6f", totalPureOCRTokens.CostUSD),
			},
			"ai_processing": gin.H{
				"provider":      "gemini",
				"input_tokens":  summary["token_usage"].(map[string]interface{})["input_tokens"].(int) - totalPureOCRTokens.InputTokens,
				"output_tokens": summary["token_usage"].(map[string]interface{})["output_tokens"],
				"total_tokens":  summary["token_usage"].(map[string]interface{})["total_tokens"],
				"cost_thb":      fmt.Sprintf("฿%.2f", reqCtx.TotalTokens.CostTHB-totalPureOCRTokens.CostTHB),
			},
			"total": gin.H{
				"cost_thb": summary["token_usage"].(map[string]interface{})["cost_thb"],
				"cost_usd": summary["token_usage"].(map[string]interface{})["cost_usd"],
			},
		}
	} else {
		// Gemini: Show combined usage (traditional format)
		metadata["ocr_provider"] = "gemini"
		metadata["token_usage"] = gin.H{
			"input_tokens":  summary["token_usage"].(map[string]interface{})["input_tokens"],
			"output_tokens": summary["token_usage"].(map[string]interface{})["output_tokens"],
			"total_tokens":  summary["token_usage"].(map[string]interface{})["total_tokens"],
			"cost_thb":      summary["token_usage"].(map[string]interface{})["cost_thb"],
		}
	}
	// Add OCR warnings if any issues were detected
	if len(ocrWarnings) > 0 {
		metadata["ocr_warnings"] = ocrWarnings
	}

	response := gin.H{
		"shopid": req.ShopID,
		"status": "success",

		// NEW: Document analysis showing relationship between images
		"document_analysis": documentAnalysis,

		// Essential: Receipt information (merged/primary)
		"receipt": receiptData,

		// Essential: Accounting entry (merged from all images)
		"accounting_entry": accountingEntry,

		// Essential: Validation summary
		"validation": validationData,

		// NEW: Template information - shows which template AI selected and why
		"template_info": templateInfo,

		// NEW: Custom prompts used for AI analysis
		"custom_prompts": gin.H{
			"shop_context":      extractShopContextForResponse(masterCache.ShopProfile),
			"template_guidance": extractTemplateGuidanceForResponse(matchedTemplate),
		},

		// NEW: Source images metadata
		"source_images": sourceImages,

		// Metadata: For tracking and debugging (includes OCR warnings if any)
		"metadata": metadata,

		// Note: IMPORTANT - Always verify request_id matches your request log!
		// If IDs don't match, this might be a cached/wrong response.
	}

	// Add debug data only if debug mode is enabled
	if debugData != nil {
		response["debug_data"] = debugData
	}

	// Filter out internal fields from ai_explanation before sending response
	if validationData != nil {
		if aiExplanation, ok := validationData["ai_explanation"].(map[string]interface{}); ok {
			// Remove evidence_from_receipt (ซ้ำกับ receipt{})
			delete(aiExplanation, "evidence_from_receipt")

			// Keep account_selection_logic but remove redundant fields
			if accountSelectionLogic, ok := aiExplanation["account_selection_logic"].(map[string]interface{}); ok {
				// Keep only template_used and template_details for user reference
				// Remove debit_accounts/credit_accounts (ซ้ำกับ entries[] 100%)
				delete(accountSelectionLogic, "debit_accounts")
				delete(accountSelectionLogic, "credit_accounts")
				delete(accountSelectionLogic, "verification")
			}
		}
	}

	job.Complete(response)
}

// SubmitTestTemplateHandler - Test a template with an uploaded image (async job).
// The multipart form/file must be read on the request goroutine (gin.Context
// requires it), so that part stays synchronous here; the OCR + accounting
// analysis pipeline — the slow part — runs in a background goroutine exactly
// like SubmitAnalyzeReceiptHandler, polled via the same GetJobStatusHandler.
func SubmitTestTemplateHandler(c *gin.Context) {
	// Step 1: Parse multipart form data
	shopID := c.PostForm("shopid")
	templateJSON := c.PostForm("template")
	model := c.PostForm("model")

	// Validate required fields
	if shopID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "shopid is required",
		})
		return
	}

	if templateJSON == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "template is required (JSON string)",
		})
		return
	}

	// Validate model field
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "กรุณาระบุ model ที่ต้องการใช้งาน (gemini หรือ mistral) ในฟิลด์ 'model'",
			"example": gin.H{
				"model": "gemini",
			},
		})
		return
	}

	if model != "gemini" && model != "mistral" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "model ที่ระบุไม่ถูกต้อง กรุณาเลือก 'gemini' หรือ 'mistral' เท่านั้น",
		})
		return
	}

	// Parse template JSON
	var template bson.M
	if err := json.Unmarshal([]byte(templateJSON), &template); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid template JSON",
			"details": err.Error(),
		})
		return
	}

	// Validate required template fields
	if _, ok := template["doccode"].(string); !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "template must contain 'doccode' field (string)",
		})
		return
	}
	if _, ok := template["description"].(string); !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "template must contain 'description' field (string)",
		})
		return
	}
	if _, ok := template["promptdescription"].(string); !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "template must contain 'promptdescription' field (string)",
		})
		return
	}

	// Step 2: Get uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "file is required",
			"details": err.Error(),
		})
		return
	}
	defer file.Close()

	// Validate file type (support both images and PDF)
	contentType := header.Header.Get("Content-Type")
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/jpg" && contentType != "application/pdf" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid file type. Only JPG/PNG images and PDF files are allowed",
			"details": fmt.Sprintf("Received: %s", contentType),
		})
		return
	}

	// Create request context
	reqCtx := common.NewRequestContext(shopID)

	templateDocCode := "unknown"
	if doccode, ok := template["doccode"].(string); ok {
		templateDocCode = doccode
	}

	reqCtx.LogInfo("🧪 เริ่มทดสอบ Template | ShopID: %s | Template Code: %s | File: %s", shopID, templateDocCode, header.Filename)

	// Step 3: Save file temporarily — must happen here, before returning,
	// since the multipart file stream is only valid for this request.
	tempFilename := fmt.Sprintf("%s_%s", uuid.New().String(), filepath.Ext(header.Filename))
	tempFilePath := filepath.Join(configs.UPLOAD_DIR, tempFilename)

	out, err := os.Create(tempFilePath)
	if err != nil {
		reqCtx.LogError("Failed to create temp file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":      "Failed to save uploaded file",
			"request_id": reqCtx.RequestID,
		})
		return
	}

	_, err = io.Copy(out, file)
	out.Close()
	if err != nil {
		os.Remove(tempFilePath)
		reqCtx.LogError("Failed to write temp file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":      "Failed to save uploaded file",
			"request_id": reqCtx.RequestID,
		})
		return
	}

	reqCtx.LogInfo("✅ File saved temporarily: %s (%.2f KB)", tempFilename, float64(header.Size)/1024)

	job, token, err := jobs.Create(reqCtx)
	if err != nil {
		os.Remove(tempFilePath) // job never got a chance to run, so nothing else will clean this up
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "too_many_active_jobs",
			"message": "ระบบกำลังประมวลผลงานจำนวนมาก กรุณาลองใหม่อีกครั้งในอีกสักครู่",
		})
		return
	}
	go runTestTemplatePipeline(job, shopID, model, template, tempFilePath, tempFilename)

	c.JSON(http.StatusAccepted, gin.H{
		"status": "processing",
		"job": gin.H{
			"id":            job.ID,
			"token":         token,
			"progress":      job.Snapshot().Progress,
			"poll_after_ms": 2000,
		},
	})
}

// runTestTemplatePipeline runs the OCR + accounting-analysis pipeline for one
// test-template job. Like runAnalyzePipeline, it must never touch *gin.Context.
func runTestTemplatePipeline(job *jobs.Job, shopID, model string, template bson.M, tempFilePath, tempFilename string) {
	reqCtx := job.ReqCtx

	templateDocCode := "unknown"
	if doccode, ok := template["doccode"].(string); ok {
		templateDocCode = doccode
	}

	// Delete temp file once the pipeline is done, success or failure —
	// mirrors the original handler's cleanup, but now covers the failure
	// paths too (the original only deleted it after a full success).
	defer func() {
		if err := os.Remove(tempFilePath); err != nil {
			reqCtx.LogWarning("⚠️  Failed to delete temp file: %v", err)
		} else {
			reqCtx.LogInfo("🗑️  Deleted temp file: %s", tempFilename)
		}
	}()

	// Step 4: Load master data
	job.UpdateProgress(10, "prepare_master_data", "กำลังโหลดข้อมูลผังบัญชี")
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		job.Fail("master_data_load_failed", fmt.Sprintf("Failed to load master data: %s", err.Error()))
		return
	}

	reqCtx.LogInfo("✓ Master data validated: %d accounts, %d journal books, %d creditors, %d debtors",
		len(masterCache.Accounts), len(masterCache.JournalBooks),
		len(masterCache.Creditors), len(masterCache.Debtors))

	// Step 5: Use provided template (no MongoDB query needed)
	templateName := "Unknown Template"
	if desc, ok := template["description"].(string); ok {
		templateName = desc
	}

	reqCtx.LogInfo("✅ Template received: %s (Code: %s)", templateName, templateDocCode)

	// Step 6: Process with OCR (Phase 1)
	job.UpdateProgress(20, "ocr", "กำลังอ่านข้อความจากเอกสาร")
	reqCtx.StartStep("pure_ocr_extraction_all")
	reqCtx.LogInfo("Pure OCR extraction (raw text only) for 1 image(s) using %s", model)

	// Create OCR provider using model from request
	ocrProvider, err := ai.CreateOCRProvider(model)
	if err != nil {
		reqCtx.LogError("Failed to create OCR provider: %v", err)
		reqCtx.EndStep("failed", nil, err)
		job.Fail("ocr_provider_init_failed", fmt.Sprintf("OCR provider initialization failed: %s", err.Error()))
		return
	}

	ocrResult, ocrTokens, err := ocrProvider.ProcessPureOCR(tempFilePath, reqCtx)
	if err != nil {
		reqCtx.LogError("OCR failed: %v", err)
		reqCtx.EndStep("failed", nil, err)
		job.Fail("ocr_failed", fmt.Sprintf("OCR processing failed: %s", err.Error()))
		return
	}

	reqCtx.LogInfo("✓ Pure OCR completed for 1 image(s) - Token savings: ~82%% vs old method")
	reqCtx.EndStep("success", ocrTokens, nil)

	// Extract text from OCR result
	ocrText := ocrResult.RawDocumentText
	if ocrText == "" {
		job.Fail("ocr_empty_text", "Failed to extract text from image")
		return
	}

	// Create pure OCR result map for AI processing
	fullResults := []map[string]interface{}{
		{
			"full_text": ocrText,
			"metadata":  ocrResult.Metadata,
		},
	}

	// Step 7: Force use the specified template (skip template matching)
	job.UpdateProgress(45, "template_match", "กำลังใช้เทมเพลตที่กำหนด")
	reqCtx.LogInfo("\n┌── template_matching_analysis")
	reqCtx.LogInfo("🧪 Force using template: %s (Test Mode)", templateName)

	matchedTemplate := &template
	templateMatchResult := map[string]interface{}{
		"template_name": templateName,
		"template_code": templateDocCode,
		"confidence":    100, // Force 100% since user explicitly provided it
		"mode":          "test",
		"note":          "Template provided by user for testing - no AI matching performed",
	}

	reqCtx.LogInfo("└── ✅ สำเร็จ")

	// Step 8: Process with accounting analysis (Phase 3)
	job.UpdateProgress(55, "prepare_master_data", "กำลังเตรียมข้อมูลผังบัญชี")
	reqCtx.LogInfo("\n┌── 📊 เตรียมข้อมูลหลัก (Master Data)")

	// Filter accounts for non-VAT shops (use all accounts for test mode)
	filteredAccounts := masterCache.Accounts

	reqCtx.LogInfo("✓ Master data ready: %d accounts (filtered from %d), %d journal books, %d creditors, %d debtors",
		len(filteredAccounts), len(masterCache.Accounts),
		len(masterCache.JournalBooks), len(masterCache.Creditors), len(masterCache.Debtors))

	reqCtx.LogInfo("└── ✅ สำเร็จ")

	// Prepare downloadedImages metadata for accounting
	downloadedImages := []map[string]interface{}{
		{
			"filename":    tempFilePath,
			"image_index": 0,
		},
	}

	// Convert ShopProfile to interface{} for AI processing
	var shopProfileInterface interface{}
	if masterCache.ShopProfile != nil {
		shopProfileInterface = masterCache.ShopProfile
	}

	// Prepare document templates array
	documentTemplates := []bson.M{template}

	// Process accounting with forced template (use full_mode since we're testing)
	job.UpdateProgress(65, "accounting_analysis", "กำลังวิเคราะห์รายการบัญชี")
	reqCtx.StartStep("phase3_multi_image_accounting")

	// Create empty vendor match result for test endpoint (no pre-matching)
	emptyVendorMatchResult := processor.VendorMatchResult{
		Found:      false,
		Code:       "",
		Name:       "",
		Similarity: 0,
		Method:     "not_found",
	}

	accountingResponseJSON, accountingTokens, err := ai.ProcessMultiImageAccountingAnalysis(
		downloadedImages,
		fullResults,
		ai.FullMode, // Use full mode for testing to get complete analysis
		matchedTemplate,
		filteredAccounts,
		masterCache.JournalBooks,
		masterCache.Creditors,
		masterCache.Debtors,
		shopProfileInterface,
		documentTemplates,
		&emptyVendorMatchResult,
		reqCtx,
	)
	reqCtx.EndStep("success", accountingTokens, nil)

	if err != nil {
		reqCtx.LogError("Accounting analysis failed: %v", err)
		reqCtx.EndStep("failed", nil, err)
		job.Fail("accounting_analysis_failed", fmt.Sprintf("Accounting analysis failed: %s", err.Error()))
		return
	}

	// Parse accounting response JSON
	var accountingResponse map[string]interface{}
	if err := json.Unmarshal([]byte(accountingResponseJSON), &accountingResponse); err != nil {
		reqCtx.LogError("Failed to parse accounting response: %v", err)
		job.Fail("parse_failed", fmt.Sprintf("Failed to parse accounting response: %s", err.Error()))
		return
	}

	// Step 9: Build response (same structure as analyze-receipt)
	job.UpdateProgress(90, "confidence", "กำลังจัดรูปแบบผลลัพธ์")
	summary := reqCtx.GetSummary()

	var documentAnalysis map[string]interface{}
	if da, ok := accountingResponse["document_analysis"].(map[string]interface{}); ok {
		documentAnalysis = da
	} else {
		documentAnalysis = map[string]interface{}{
			"total_images": 1,
			"relationship": "single_document",
			"confidence":   99,
		}
	}

	var sourceImages []interface{}
	if si, ok := accountingResponse["source_images"].([]interface{}); ok {
		sourceImages = si
	}

	// Extract template info with the forced template
	templateInfo := processor.ExtractTemplateInfo(accountingResponse, documentTemplates, matchedTemplate, reqCtx)

	var receiptData map[string]interface{}
	if rd, ok := accountingResponse["receipt"].(map[string]interface{}); ok {
		receiptData = rd
	} else {
		receiptData = gin.H{
			"number":        "N/A",
			"date":          "N/A",
			"vendor_name":   "N/A",
			"vendor_tax_id": "N/A",
			"total":         0,
			"vat":           0,
		}
	}

	accountingEntry := accountingResponse["accounting_entry"]
	validationData := accountingResponse["validation"]

	// Add fields_requiring_review
	fieldsRequiringReview := []string{}
	if receiptData != nil {
		if vendorName, ok := receiptData["vendor_name"].(string); ok && (vendorName == "Unknown Vendor" || vendorName == "N/A" || vendorName == "") {
			fieldsRequiringReview = append(fieldsRequiringReview, "vendor_name")
		}
		if vendorTaxID, ok := receiptData["vendor_tax_id"].(string); ok && (vendorTaxID == "Unknown Vendor" || vendorTaxID == "N/A" || vendorTaxID == "") {
			fieldsRequiringReview = append(fieldsRequiringReview, "vendor_tax_id")
		}
	}
	if len(fieldsRequiringReview) > 0 {
		if vd, ok := validationData.(map[string]interface{}); ok {
			vd["fields_requiring_review"] = fieldsRequiringReview
			if requiresReview, ok := vd["requires_review"].(bool); !ok || !requiresReview {
				vd["requires_review"] = true
			}
		}
	}

	response := gin.H{
		"shopid": shopID,
		"status": "success",
		"mode":   "test_template",

		"document_analysis": documentAnalysis,
		"receipt":           receiptData,
		"accounting_entry":  accountingEntry,
		"validation":        validationData,
		"template_info":     templateInfo,

		"custom_prompts": gin.H{
			"shop_context":      extractShopContextForResponse(shopProfileInterface),
			"template_guidance": extractTemplateGuidanceForResponse(matchedTemplate),
		},

		"source_images": sourceImages,

		"metadata": gin.H{
			"request_id":       reqCtx.RequestID,
			"processed_at":     time.Now().Format(time.RFC3339),
			"duration_sec":     summary["total_duration_sec"],
			"images_processed": 1,
			"test_mode":        true,
			"template_code":    templateDocCode,
			"token_usage": gin.H{
				"input_tokens":  summary["token_usage"].(map[string]interface{})["input_tokens"],
				"output_tokens": summary["token_usage"].(map[string]interface{})["output_tokens"],
				"total_tokens":  summary["token_usage"].(map[string]interface{})["total_tokens"],
				"cost_thb":      summary["token_usage"].(map[string]interface{})["cost_thb"],
			},
		},

		"template_match": templateMatchResult,
	}

	// Filter out internal fields from ai_explanation
	if validationData != nil {
		if vd, ok := validationData.(map[string]interface{}); ok {
			if aiExplanation, ok := vd["ai_explanation"].(map[string]interface{}); ok {
				delete(aiExplanation, "evidence_from_receipt")
				if accountSelectionLogic, ok := aiExplanation["account_selection_logic"].(map[string]interface{}); ok {
					delete(accountSelectionLogic, "debit_accounts")
					delete(accountSelectionLogic, "credit_accounts")
					delete(accountSelectionLogic, "verification")
				}
			}
		}
	}

	reqCtx.LogInfo("═══ 🎯 สรุปผล (Test Mode) ═══")
	reqCtx.LogInfo("⏱️  เวลารวม: %.2fวินาที | 🪙 Tokens: %s | 💰 ค่าใช้จ่าย: %s",
		summary["total_duration_sec"],
		formatTokenSummary(summary["token_usage"].(map[string]interface{})),
		summary["token_usage"].(map[string]interface{})["cost_thb"])
	reqCtx.LogInfo("✅ ทดสอบเทมเพลต: '%s' สำเร็จ", templateName)
	reqCtx.LogInfo("═══════════════════════════")

	job.Complete(response)
}

// formatTokenSummary formats token usage for logging
func formatTokenSummary(tokenUsage map[string]interface{}) string {
	input := tokenUsage["total_input_tokens"]
	output := tokenUsage["total_output_tokens"]
	total := tokenUsage["total_tokens"]
	return fmt.Sprintf("%vเข้า + %vออก = %vรวม", input, output, total)
}

// getStringFromInterface แปลง interface{} เป็น string
func getStringFromInterface(val interface{}) string {
	if val == nil {
		return ""
	}
	if str, ok := val.(string); ok {
		return str
	}
	return ""
}

// generateReviewRequirements สร้างรายละเอียดการตรวจสอบแบบเข้าใจง่าย
func generateReviewRequirements(confidenceResult processor.ConfidenceResult, accountingEntry map[string]interface{}) map[string]interface{} {
	if !confidenceResult.RequiresReview {
		return map[string]interface{}{
			"requires_review": false,
			"can_save":        true,
			"priority":        "none",
			"status":          "passed",
			"message":         "ข้อมูลครบถ้วนและถูกต้อง สามารถบันทึกบัญชีได้เลย",
			"issues":          []map[string]interface{}{},
			"missing_fields":  []string{},
			"recommendations": []string{},
		}
	}

	factors := confidenceResult.Factors
	score := confidenceResult.OverallScore

	// รายการที่ต้องตรวจสอบ
	reviewItems := []map[string]interface{}{}
	missingFields := []string{}
	recommendations := []string{}

	// ตรวจสอบแต่ละปัจจัย
	if factors.TemplateMatch < 80 {
		reviewItems = append(reviewItems, map[string]interface{}{
			"category": "template",
			"score":    factors.TemplateMatch,
			"status":   getStatusLevel(factors.TemplateMatch),
			"issue":    "เอกสารอาจไม่ตรงกับเทมเพลตที่เลือก",
			"action":   "ตรวจสอบว่าเลือกเทมเพลตถูกต้องหรือไม่",
		})
		recommendations = append(recommendations, "ตรวจสอบการเลือกเทมเพลต - อาจต้องสร้างเทมเพลตใหม่หรือปรับปรุงเทมเพลตที่มี")
	}

	if factors.PartyMatch < 80 {
		debtorCode := getStringFromInterface(accountingEntry["debtor_code"])
		creditorCode := getStringFromInterface(accountingEntry["creditor_code"])
		debtorName := getStringFromInterface(accountingEntry["debtor_name"])
		creditorName := getStringFromInterface(accountingEntry["creditor_name"])

		party := "คู่ค้า"
		problemDetail := "ไม่พบข้อมูลคู่ค้าในระบบหรือชื่อไม่ตรงกัน"
		actionRequired := "ตรวจสอบข้อมูลคู่ค้า"

		// กำหนดประเภทคู่ค้า
		if debtorCode != "" || debtorName != "" {
			party = "ลูกค้า (Debtor)"
		} else if creditorCode != "" || creditorName != "" {
			party = "เจ้าหนี้ (Creditor)"
		}

		// กรณีมีชื่อแต่ไม่มีรหัส = ไม่พบใน Master Data
		if (debtorCode == "" || debtorCode == "null") && debtorName != "" && debtorName != "null" {
			problemDetail = fmt.Sprintf("ไม่พบลูกค้า '%s' ใน Master Data", debtorName)
			actionRequired = fmt.Sprintf("เพิ่มข้อมูลลูกค้า '%s' เข้าสู่ระบบ Master Data", debtorName)
			missingFields = append(missingFields, fmt.Sprintf("ลูกค้า '%s' ไม่มีในระบบ Master Data", debtorName))
			recommendations = append(recommendations, fmt.Sprintf("⚠️ เพิ่มลูกค้า '%s' (หากเป็นลูกค้าประจำ) หรือใช้รหัส 'ลูกค้าทั่วไป' (หากเป็นลูกค้าชั่วคราว)", debtorName))
		} else if (creditorCode == "" || creditorCode == "null") && creditorName != "" && creditorName != "null" {
			problemDetail = fmt.Sprintf("ไม่พบเจ้าหนี้ '%s' ใน Master Data", creditorName)
			actionRequired = fmt.Sprintf("เพิ่มข้อมูลเจ้าหนี้ '%s' เข้าสู่ระบบ Master Data", creditorName)
			missingFields = append(missingFields, fmt.Sprintf("เจ้าหนี้ '%s' ไม่มีในระบบ Master Data", creditorName))
			recommendations = append(recommendations, fmt.Sprintf("⚠️ เพิ่มเจ้าหนี้ '%s' เข้าสู่ระบบ Master Data ก่อนบันทึกบัญชี", creditorName))
		} else if debtorCode == "" && creditorCode == "" && debtorName == "" && creditorName == "" {
			// ไม่มีข้อมูลคู่ค้าเลย
			problemDetail = "ไม่มีข้อมูลลูกค้าหรือเจ้าหนี้"
			actionRequired = "ระบุข้อมูลลูกค้าหรือเจ้าหนี้"
			missingFields = append(missingFields, "ข้อมูลลูกค้า (debtor) หรือเจ้าหนี้ (creditor)")
			recommendations = append(recommendations, "⚠️ เพิ่มข้อมูลลูกค้าหรือเจ้าหนี้ลงในเอกสาร")
		} else {
			// มีรหัสแต่ไม่ตรงกัน 100%
			actionRequired = "ตรวจสอบชื่อให้ตรงกับข้อมูลในระบบ"
			recommendations = append(recommendations, "⚠️ ตรวจสอบชื่อให้ตรงกับข้อมูลในระบบ หรืออัปเดตข้อมูลในระบบให้ตรงกับเอกสาร")
		}

		reviewItems = append(reviewItems, map[string]interface{}{
			"category":   "party",
			"party_type": party,
			"score":      factors.PartyMatch,
			"status":     getStatusLevel(factors.PartyMatch),
			"issue":      problemDetail,
			"action":     actionRequired,
		})
	}

	if factors.DataCompleteness < 80 {
		// ตรวจสอบฟิลด์หลักที่จำเป็น
		if accountingEntry["reference_number"] == nil || accountingEntry["reference_number"] == "" {
			missingFields = append(missingFields, "เลขที่เอกสาร (reference_number)")
		}
		if accountingEntry["document_date"] == nil || accountingEntry["document_date"] == "" {
			missingFields = append(missingFields, "วันที่เอกสาร (document_date)")
		}
		if accountingEntry["journal_book_code"] == nil || accountingEntry["journal_book_code"] == "" {
			missingFields = append(missingFields, "รหัสสมุดรายวัน (journal_book_code)")
		}

		// ตรวจสอบว่ามี debtor หรือ creditor
		debtorCode := getStringFromInterface(accountingEntry["debtor_code"])
		debtorName := getStringFromInterface(accountingEntry["debtor_name"])
		creditorCode := getStringFromInterface(accountingEntry["creditor_code"])
		creditorName := getStringFromInterface(accountingEntry["creditor_name"])

		hasDebtor := (debtorCode != "" && debtorCode != "null") || (debtorName != "" && debtorName != "null")
		hasCreditor := (creditorCode != "" && creditorCode != "null") || (creditorName != "" && creditorName != "null")

		if !hasDebtor && !hasCreditor {
			missingFields = append(missingFields, "ข้อมูลลูกค้า (debtor) หรือเจ้าหนี้ (creditor)")
		}

		// ตรวจสอบรายการบัญชี (entries)
		if entries, ok := accountingEntry["entries"].([]interface{}); ok && len(entries) > 0 {
			for i, entry := range entries {
				if entryMap, ok := entry.(map[string]interface{}); ok {
					entryIssues := []string{}

					// เช็ค account_code
					if entryMap["account_code"] == nil || entryMap["account_code"] == "" {
						entryIssues = append(entryIssues, "รหัสบัญชี")
					}

					// เช็ค description
					if entryMap["description"] == nil || entryMap["description"] == "" {
						entryIssues = append(entryIssues, "รายละเอียด")
					}

					// เช็ค selection_reason
					if entryMap["selection_reason"] == nil || entryMap["selection_reason"] == "" {
						entryIssues = append(entryIssues, "เหตุผลในการเลือกบัญชี")
					}

					// เช็ค side_reason
					if entryMap["side_reason"] == nil || entryMap["side_reason"] == "" {
						entryIssues = append(entryIssues, "เหตุผลในการบันทึกฝั่ง DR/CR")
					}

					if len(entryIssues) > 0 {
						missingFields = append(missingFields,
							fmt.Sprintf("รายการที่ %d: %s", i+1, strings.Join(entryIssues, ", ")))
					}
				}
			}
		} else {
			missingFields = append(missingFields, "รายการบัญชี (entries)")
		}

		// สร้างข้อความปัญหาที่ชัดเจน
		problemText := "ข้อมูลไม่ครบถ้วน"
		actionText := "เติมข้อมูลที่หายไปให้ครบถ้วน"

		if len(missingFields) > 0 {
			problemText = fmt.Sprintf("ขาดข้อมูล %d รายการ", len(missingFields))
			actionText = fmt.Sprintf("เติมข้อมูลที่ขาดหายไป: %s", strings.Join(missingFields, " | "))
		}

		reviewItems = append(reviewItems, map[string]interface{}{
			"category": "data_completeness",
			"score":    factors.DataCompleteness,
			"status":   getStatusLevel(factors.DataCompleteness),
			"issue":    problemText,
			"action":   actionText,
		})

		// คำแนะนำที่ชัดเจน
		if len(missingFields) > 0 {
			for _, field := range missingFields {
				recommendations = append(recommendations, "⚠️ "+field)
			}
		} else {
			recommendations = append(recommendations, "ตรวจสอบความครบถ้วนของข้อมูลในแต่ละรายการ")
		}
	}

	if factors.FieldValidation < 80 {
		reviewItems = append(reviewItems, map[string]interface{}{
			"category": "field_validation",
			"score":    factors.FieldValidation,
			"status":   getStatusLevel(factors.FieldValidation),
			"issue":    "รูปแบบข้อมูลบางส่วนไม่ถูกต้อง",
			"action":   "ตรวจสอบรูปแบบวันที่, ตัวเลข, รหัสบัญชี",
		})
		recommendations = append(recommendations, "ตรวจสอบรูปแบบข้อมูล เช่น วันที่ต้องเป็น YYYY-MM-DD, ตัวเลขต้องเป็นตัวเลขเท่านั้น")
	}

	if factors.BalanceValidation < 80 {
		reviewItems = append(reviewItems, map[string]interface{}{
			"category": "balance",
			"score":    factors.BalanceValidation,
			"status":   getStatusLevel(factors.BalanceValidation),
			"issue":    "ยอด Debit ไม่เท่ากับ Credit",
			"action":   "ตรวจสอบการคำนวณยอดเงินให้ถูกต้อง",
		})
		recommendations = append(recommendations, "ยอดไม่สมดุล - ต้องแก้ไขก่อนบันทึกบัญชี")
	}

	// กำหนดระดับความสำคัญ
	priority := "low"
	statusCode := "should_review"
	canProceed := score >= 70

	if score < 70 {
		priority = "high"
		statusCode = "must_fix"
		canProceed = false
	} else if score < 85 && (factors.DataCompleteness < 70 || factors.FieldValidation < 70 || factors.BalanceValidation < 80) {
		priority = "medium"
		statusCode = "recommended_review"
	}

	// สรุปคำแนะนำ
	mainRecommendation := "ตรวจสอบรายการที่มีปัญหาด้านล่าง"
	if !canProceed {
		mainRecommendation = "ต้องแก้ไขปัญหาทั้งหมดก่อนจึงจะบันทึกบัญชีได้"
	} else if priority == "low" {
		mainRecommendation = "สามารถบันทึกบัญชีได้ แต่แนะนำให้ตรวจสอบข้อมูลก่อน"
	}

	return map[string]interface{}{
		"requires_review": true,
		"can_save":        canProceed,
		"priority":        priority,
		"status":          statusCode,
		"score":           score,
		"message":         mainRecommendation,
		"issues":          reviewItems,
		"missing_fields":  missingFields,
		"recommendations": recommendations,
		"summary": map[string]interface{}{
			"total_issues":    len(reviewItems),
			"critical_issues": countCriticalIssues(confidenceResult),
			"minor_issues":    len(reviewItems) - countCriticalIssues(confidenceResult),
		},
	}
}

// getStatusLevel คืนค่าระดับสถานะตามคะแนน
func getStatusLevel(score float64) string {
	if score >= 90 {
		return "excellent"
	} else if score >= 80 {
		return "good"
	} else if score >= 70 {
		return "fair"
	} else if score >= 60 {
		return "poor"
	}
	return "very_poor"
}

// countCriticalIssues นับจำนวนปัญหาร้ายแรง
func countCriticalIssues(confidenceResult processor.ConfidenceResult) int {
	critical := 0
	factors := confidenceResult.Factors

	if factors.BalanceValidation < 80 {
		critical++
	}
	if factors.FieldValidation < 60 {
		critical++
	}
	if factors.DataCompleteness < 50 {
		critical++
	}

	return critical
}
