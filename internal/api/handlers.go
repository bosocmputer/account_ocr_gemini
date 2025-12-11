// handlers.go - Contains the HTTP handler function for file upload and validation logic.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/ai"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/processor"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
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
}

// JournalEntry represents an accounting entry
type JournalEntry struct {
	AccountCode string  `json:"account_code"`
	AccountName string  `json:"account_name"`
	Debit       float64 `json:"debit"`
	Credit      float64 `json:"credit"`
	Description string  `json:"description"`
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

// FetchDocumentFormate retrieves accounting templates from documentFormate collection
// Returns only templates that have details (not empty templates)
func FetchDocumentFormate(shopID string) ([]bson.M, error) {
	collection := storage.GetMongoDB().Collection("documentFormate")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Query by shopid and filter out empty templates
	filter := bson.M{
		"shopid":  shopID,
		"details": bson.M{"$exists": true, "$ne": []interface{}{}},
	}

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return []bson.M{}, nil // No templates found is OK
		}
		return nil, fmt.Errorf("failed to query documentFormate: %w", err)
	}
	defer cursor.Close(ctx)

	var templates []bson.M
	if err = cursor.All(ctx, &templates); err != nil {
		return nil, fmt.Errorf("failed to decode documentFormate: %w", err)
	}

	return templates, nil
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

// downloadImageFromURL downloads an image from a URL and saves it to a local file
func downloadImageFromURL(imageURL, filename string) error {
	// Send GET request to download the image
	resp, err := http.Get(imageURL)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	// Check if response is successful
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download image: HTTP %d", resp.StatusCode)
	}

	// Create the output file
	out, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Copy the downloaded content to the file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to save image: %w", err)
	}

	return nil
}

// --- New Analyze Receipt Handler (Phase 1 Complete Flow) ---

// AnalyzeReceiptHandler handles POST requests to /api/v1/analyze-receipt
// It performs full OCR + accounting analysis with master data integration
func AnalyzeReceiptHandler(c *gin.Context) {
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

	// Create request context for tracking
	reqCtx := common.NewRequestContext(req.ShopID)

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

	reqCtx.LogInfo("✓ Master data validated: %d accounts, %d journal books, %d creditors",
		len(masterCache.Accounts), len(masterCache.JournalBooks), len(masterCache.Creditors))

	// ⚡ FETCH DOCUMENT FORMATE TEMPLATES (accounting patterns)
	// This provides AI with predefined accounting entry templates for consistency
	documentTemplates, err := FetchDocumentFormate(req.ShopID)
	if err != nil {
		reqCtx.LogWarning("Failed to fetch documentFormate templates: %v", err)
		// Continue without templates - AI will work without them
		documentTemplates = []bson.M{}
	}
	reqCtx.LogInfo("✓ Document templates loaded: %d templates found", len(documentTemplates))

	// Setup timeout context (5 minutes max for very complex receipts)
	// Note: Complex receipts with many items can take 2-3 minutes
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	// Channel to signal completion
	done := make(chan bool, 1)
	timeout := make(chan bool, 1)

	// Monitor for timeout
	go func() {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				reqCtx.LogError("⚠️  Request timeout after 5 minutes - receipt too complex")

				// Send timeout response immediately
				c.JSON(http.StatusRequestTimeout, gin.H{
					"error":   "Processing timeout",
					"message": "Receipt is too complex and processing exceeded 5 minutes. Please try with a clearer or simpler receipt image.",
					"details": "This usually happens with very long receipts (50+ items) or low-quality images requiring extensive processing.",
					"suggestions": []string{
						"Try taking a clearer photo with better lighting",
						"Ensure the receipt is flat and fully visible",
						"Consider splitting very long receipts into sections",
						"Check if the receipt has unusually complex layout",
					},
					"request_id": reqCtx.RequestID,
					"processing_summary": map[string]interface{}{
						"timeout_at":      "5 minutes",
						"total_duration":  time.Since(reqCtx.StartTime).Seconds(),
						"completed_steps": reqCtx.GetPartialSummary(),
					},
				})

				timeout <- true
			}
		case <-done:
			return
		}
	}()

	// Step 2: Download ALL images from Azure Blob Storage
	reqCtx.StartStep("download_images")
	reqCtx.LogInfo("Downloading %d image(s)", len(req.ImageReferences))

	type ImageData struct {
		Filename string
		Index    int
		GUID     string
		URI      string
	}

	var downloadedImages []ImageData

	for i, imgRef := range req.ImageReferences {
		if imgRef.ImageURI == "" {
			reqCtx.EndStep("failed", nil, fmt.Errorf("imageuri is required in imagereferences[%d]", i))
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      fmt.Sprintf("imageuri is required in imagereferences[%d]", i),
				"request_id": reqCtx.RequestID,
			})
			return
		}

		// Generate unique filename for downloaded image
		uniqueID := uuid.New().String()
		filename := filepath.Join(configs.UPLOAD_DIR, fmt.Sprintf("%s_%d.jpg", uniqueID, i))

		// Download image from Azure Blob Storage
		if err := downloadImageFromURL(imgRef.ImageURI, filename); err != nil {
			reqCtx.EndStep("failed", nil, err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":       "Failed to download image from Azure Blob Storage",
				"details":     err.Error(),
				"image_uri":   imgRef.ImageURI,
				"image_index": i,
				"request_id":  reqCtx.RequestID,
			})
			return
		}

		downloadedImages = append(downloadedImages, ImageData{
			Filename: filename,
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

	// Step 3: Process full OCR for ALL images (Phase 1 Quick OCR removed for performance)
	reqCtx.StartStep("full_ocr_extraction_all")
	reqCtx.LogInfo("Full OCR extraction for %d image(s)", len(downloadedImages))

	// Check if we should continue (not timed out)
	select {
	case <-timeout:
		reqCtx.EndStep("cancelled", nil, fmt.Errorf("timeout before full OCR"))
		return
	default:
		// Continue
	}

	type FullOCRImageResult struct {
		ImageIndex int
		Result     *ai.ExtractionResult
		Tokens     *common.TokenUsage
		Error      error
	}

	var fullOCRResults []FullOCRImageResult
	var totalFullOCRTokens common.TokenUsage

	// Collect Phase 2 quality issues
	var phase2FailedImages []FailedImageInfo
	var phase2PassedImages []PassedImageInfo

	// ⚡ PARALLEL PROCESSING: Process all images concurrently
	type ocrJob struct {
		img   ImageData
		index int
	}

	resultsChan := make(chan FullOCRImageResult, len(downloadedImages))
	jobsChan := make(chan ocrJob, len(downloadedImages))

	// Start worker goroutines
	numWorkers := len(downloadedImages)
	if numWorkers > 3 {
		numWorkers = 3 // Limit to 3 concurrent requests to avoid API rate limits
	}

	for w := 0; w < numWorkers; w++ {
		go func() {
			for job := range jobsChan {
				result, fullOCRTokens, err := ai.ProcessOCRAndGemini(job.img.Filename, reqCtx)
				resultsChan <- FullOCRImageResult{
					ImageIndex: job.img.Index,
					Result:     result,
					Tokens:     fullOCRTokens,
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
	resultsMap := make(map[int]FullOCRImageResult)
	for i := 0; i < len(downloadedImages); i++ {
		res := <-resultsChan
		resultsMap[res.ImageIndex] = res
	}
	close(resultsChan)

	// Process results in original order
	for _, img := range downloadedImages {
		res := resultsMap[img.Index]
		result := res.Result
		fullOCRTokens := res.Tokens
		err := res.Error

		if err != nil {
			reqCtx.LogWarning("⚠️  Image %d Full OCR failed: %v", img.Index, err)
			// Continue with other images even if one fails
		}

		// ⚡ QUALITY VALIDATION - Phase 2 (Collect extraction quality issues)
		var issues []ImageQualityIssue

		if result != nil && err == nil {
			// Check: Overall confidence from validation metadata
			// Note: N/A item check removed - not all accounting documents have line items
			// (e.g., tax receipts, utility bills, government fees, payment slips)
			// Access OverallConfidence directly from Validation struct
			if result.Validation.OverallConfidence.Score > 0 {
				scoreFloat := float64(result.Validation.OverallConfidence.Score)
				if scoreFloat < MIN_OVERALL_CONFIDENCE {
					issues = append(issues, ImageQualityIssue{
						Field:        "overall_confidence",
						Issue:        fmt.Sprintf("ความมั่นใจในการสกัดข้อมูลต่ำเกินไป - %.1f%%", scoreFloat),
						CurrentValue: fmt.Sprintf("%.1f%%", scoreFloat),
						MinRequired:  fmt.Sprintf("%.1f%%", MIN_OVERALL_CONFIDENCE),
					})
				}
			}
		}

		// Store Phase 2 quality result
		if len(issues) > 0 {
			reqCtx.LogWarning("⚠️  Image %d (GUID: %s) - Phase 2 quality issues: %d", img.Index, img.GUID, len(issues))
			phase2FailedImages = append(phase2FailedImages, FailedImageInfo{
				DocumentImageGUID: img.GUID,
				ImageIndex:        img.Index,
				ImageURI:          img.URI,
				Issues:            issues,
			})
		} else {
			phase2PassedImages = append(phase2PassedImages, PassedImageInfo{
				DocumentImageGUID: img.GUID,
				ImageIndex:        img.Index,
				ImageURI:          img.URI,
				Note:              "รูปนี้ผ่านการตรวจสอบคุณภาพ แต่ไม่สามารถประมวลผลได้เพราะมีรูปอื่นไม่ผ่าน",
			})
		}

		fullOCRResults = append(fullOCRResults, FullOCRImageResult{
			ImageIndex: img.Index,
			Result:     result,
			Tokens:     fullOCRTokens,
			Error:      err,
		})

		if fullOCRTokens != nil {
			totalFullOCRTokens.InputTokens += fullOCRTokens.InputTokens
			totalFullOCRTokens.OutputTokens += fullOCRTokens.OutputTokens
			totalFullOCRTokens.TotalTokens += fullOCRTokens.TotalTokens
			totalFullOCRTokens.CostUSD += fullOCRTokens.CostUSD
			totalFullOCRTokens.CostTHB += fullOCRTokens.CostTHB
		}
	}

	reqCtx.LogInfo("✓ Full OCR completed for %d image(s)", len(fullOCRResults))

	// ⚡ CHECK PHASE 2 QUALITY RESULTS - If ANY image failed extraction quality, reject ALL
	if len(phase2FailedImages) > 0 {
		reqCtx.LogWarning("❌ REJECTING ALL - %d of %d image(s) failed Phase 2 extraction quality",
			len(phase2FailedImages), len(downloadedImages))
		reqCtx.EndStep("rejected", &totalFullOCRTokens, nil)

		message := fmt.Sprintf("สกัดข้อมูลจาก %d จาก %d รูปไม่สำเร็จ เนื่องจากคุณภาพรูปไม่เพียงพอ กรุณาอัพโหลดรูปใหม่",
			len(phase2FailedImages), len(downloadedImages))

		c.JSON(http.StatusBadRequest, RejectionResponse{
			Status:       "rejected",
			Reason:       "extraction_quality_insufficient",
			Message:      message,
			FailedImages: phase2FailedImages,
			PassedImages: phase2PassedImages,
			Suggestions: []string{
				"ถ่ายรูปใหม่ในที่มีแสงสว่างเพียงพอ",
				"ให้กล้องโฟกัสชัดก่อนถ่าย ตรวจสอบว่ารูปไม่เบลอ",
				"ตรวจสอบว่ารายการสินค้าทุกรายการเห็นชัดเจน",
				"หลีกเลี่ยงการถ่ายรูปที่มีลายมือเขียนไม่ชัด",
				"วางใบเสร็จให้เรียบ ไม่ยับ ไม่พับ",
			},
			RequestID:   reqCtx.RequestID,
			TotalImages: len(downloadedImages),
			FailedCount: len(phase2FailedImages),
		})
		return
	}

	reqCtx.LogInfo("✓ All %d image(s) passed Phase 2 quality checks", len(fullOCRResults))
	reqCtx.EndStep("success", &totalFullOCRTokens, nil)

	// Step 5: Prepare master data (already validated and loaded at the beginning)
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
			"name": cr["name"],
		})
	}

	accounts := compressedAccounts
	journalBooks := compressedJournalBooks
	creditors := compressedCreditors

	reqCtx.LogInfo("✓ Master data ready: %d accounts (filtered from %d), %d journal books, %d creditors",
		len(accounts), len(masterCache.Accounts), len(journalBooks), len(creditors))
	reqCtx.EndStep("success", nil, nil)

	// Step 6: Phase 2 - AI Multi-Image Accounting Analysis
	reqCtx.StartStep("phase2_multi_image_accounting")
	reqCtx.LogInfo("Analyzing relationships between %d image(s)", len(fullOCRResults))

	// Check if we should continue (not timed out)
	select {
	case <-timeout:
		reqCtx.EndStep("cancelled", &totalFullOCRTokens, fmt.Errorf("timeout before accounting analysis"))
		return
	default:
		// Continue
	}

	// Process multi-image accounting analysis
	accountingJSON, phase2Tokens, err := ai.ProcessMultiImageAccountingAnalysis(
		downloadedImages,
		fullOCRResults,
		accounts,
		journalBooks,
		creditors,
		documentTemplates,
		reqCtx,
	)
	if err != nil {
		reqCtx.EndStep("failed", phase2Tokens, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":      "Accounting analysis failed",
			"details":    err.Error(),
			"request_id": reqCtx.RequestID,
		})
		return
	}
	reqCtx.EndStep("success", phase2Tokens, nil)

	// Parse accounting JSON
	var accountingResponse map[string]interface{}
	if err := json.Unmarshal([]byte(accountingJSON), &accountingResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to parse accounting response",
			"details": err.Error(),
		})
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
						AccountCode: getStringValue(entryMap, "account_code"),
						AccountName: getStringValue(entryMap, "account_name"),
						Debit:       getFloatValue(entryMap, "debit"),
						Credit:      getFloatValue(entryMap, "credit"),
						Description: getStringValue(entryMap, "description"),
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
		}
	}

	// Step 8: Extract data safely (no draft saving)
	var accountingEntry map[string]interface{}
	if ae, ok := accountingResponse["accounting_entry"].(map[string]interface{}); ok {
		accountingEntry = ae
	} else {
		accountingEntry = map[string]interface{}{}
	}

	var validationData map[string]interface{}
	if vd, ok := accountingResponse["validation"].(map[string]interface{}); ok {
		validationData = vd
	} else {
		// Fallback validation from first successful OCR result
		validationData = map[string]interface{}{
			"overall_confidence": map[string]interface{}{"level": "medium", "score": 75},
			"requires_review":    true,
		}
		// Try to get from first fullOCRResult
		for _, ocrResult := range fullOCRResults {
			if ocrResult.Result != nil {
				validationData = map[string]interface{}{
					"overall_confidence": ocrResult.Result.Validation.OverallConfidence,
					"requires_review":    ocrResult.Result.Validation.RequiresReview,
				}
				break
			}
		}
	}

	// Step 9: Check if we timed out during processing
	select {
	case <-timeout:
		// Timeout occurred, but we finished anyway - return response with warning
		reqCtx.LogWarning("⚠️  Processing completed after timeout - response may not be delivered")
	default:
		// Normal completion
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
	templateInfo := processor.ExtractTemplateInfo(accountingResponse, documentTemplates, reqCtx)

	// Get primary receipt data from accounting response or first successful OCR result
	var receiptData map[string]interface{}
	if rd, ok := accountingResponse["receipt"].(map[string]interface{}); ok {
		receiptData = rd
	} else {
		// Fallback to first successful OCR result
		for _, ocrResult := range fullOCRResults {
			if ocrResult.Result != nil {
				receiptData = gin.H{
					"number":        ocrResult.Result.ReceiptNumber,
					"date":          ocrResult.Result.InvoiceDate,
					"vendor_name":   "Unknown Vendor", // Vendor info now comes from Phase 3 accounting analysis
					"vendor_tax_id": "Unknown Vendor",
					"total":         ocrResult.Result.TotalAmount.Value,
					"vat":           ocrResult.Result.VATAmount.Value,
				}
				break
			}
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

		// NEW: Source images metadata
		"source_images": sourceImages,

		// Metadata: For tracking and debugging
		"metadata": gin.H{
			"request_id":       reqCtx.RequestID,
			"processed_at":     time.Now().Format(time.RFC3339),
			"duration_sec":     summary["total_duration_sec"],
			"cost_thb":         summary["token_usage"].(map[string]interface{})["cost_thb"],
			"images_processed": len(downloadedImages),
		},
	}

	// Signal completion
	select {
	case done <- true:
		// Successfully signaled
	default:
		// Channel might be closed or blocked
	}

	// Try to send response (might fail if timeout already sent error)
	select {
	case <-timeout:
		reqCtx.LogError("❌ Cannot send response - timeout already occurred")
		// Response already sent by timeout handler
	default:
		c.JSON(http.StatusOK, response)
	}
}
