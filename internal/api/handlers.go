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
	"go.mongodb.org/mongo-driver/bson/primitive"
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

	// Create request context for tracking
	reqCtx := common.NewRequestContext(req.ShopID)

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

	// 🔍 DEBUG: Show Creditors details
	reqCtx.LogInfo("📋 Creditors List:")
	for i, creditor := range masterCache.Creditors {
		code := ""
		name := ""
		if c, ok := creditor["code"].(string); ok {
			code = c
		}
		if n := extractNameFromNamesArray(creditor); n != "" {
			name = n
		}
		reqCtx.LogInfo("  %d. Code: %s | Name: %s", i+1, code, name)
	}

	// ⚡ FETCH DOCUMENT FORMATE TEMPLATES (accounting patterns)
	// This provides AI with predefined accounting entry templates for consistency
	documentTemplates, err := FetchDocumentFormate(req.ShopID)
	if err != nil {
		reqCtx.LogWarning("Failed to fetch documentFormate templates: %v", err)
		// Continue without templates - AI will work without them
		documentTemplates = []bson.M{}
	}
	reqCtx.LogInfo("✓ Document templates loaded: %d templates found", len(documentTemplates))

	// 🔍 DEBUG: Show Templates details
	reqCtx.LogInfo("📋 Document Templates List:")
	for i, tmpl := range documentTemplates {
		id := ""
		name := ""
		desc := ""
		if objID, ok := tmpl["_id"].(primitive.ObjectID); ok {
			id = objID.Hex()
		}
		if n, ok := tmpl["name"].(string); ok {
			name = n
		}
		if d, ok := tmpl["description"].(string); ok {
			desc = d
		}
		reqCtx.LogInfo("  %d. ID: %s | Name: %s | Desc: %s", i+1, id, name, desc)
	}

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

	// Step 3: Process PURE OCR for ALL images (NEW OPTIMIZED VERSION)
	// Changed from full structured extraction to raw text only - saves ~25,000 tokens per image!
	reqCtx.StartStep("pure_ocr_extraction_all")
	reqCtx.LogInfo("Pure OCR extraction (raw text only) for %d image(s)", len(downloadedImages))

	// Check if we should continue (not timed out)
	select {
	case <-timeout:
		reqCtx.EndStep("cancelled", nil, fmt.Errorf("timeout before pure OCR"))
		return
	default:
		// Continue
	}

	type PureOCRImageResult struct {
		ImageIndex int
		Result     *ai.SimpleOCRResult
		Tokens     *common.TokenUsage
		Error      error
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

	// Start worker goroutines
	// Changed to sequential processing (1 worker) to prevent 429 Rate Limit errors
	// Gemini Free Tier: 15 RPM = must wait ~4 seconds between requests
	// Parallel processing (3 workers) causes burst traffic → 429 errors
	numWorkers := 1 // Sequential processing - safe for Tier 1 (15 RPM limit)

	for w := 0; w < numWorkers; w++ {
		go func() {
			for job := range jobsChan {
				result, pureOCRTokens, err := ai.ProcessPureOCR(job.img.Filename, reqCtx)
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
	// If match found (≥85% confidence) → Use template-only mode (saves another ~20,000 tokens in Phase 3!)
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

	if templateMatchResult.Confidence >= 85 && templateMatchResult.Template != nil {
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
		reqCtx.LogInfo("❌ No template match (Confidence: %.1f%% < 85%%) - Using full master data mode",
			templateMatchResult.Confidence)
	}

	reqCtx.EndStep("success", nil, nil)

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

	// Step 6: Phase 3 - AI Multi-Image Accounting Analysis (with conditional master data loading)
	reqCtx.StartStep("phase3_multi_image_accounting")
	reqCtx.LogInfo("Analyzing relationships between %d image(s) - Mode: %s", len(pureOCRResults), masterDataMode)

	// Check if we should continue (not timed out)
	select {
	case <-timeout:
		reqCtx.EndStep("cancelled", &totalPureOCRTokens, fmt.Errorf("timeout before accounting analysis"))
		return
	default:
		// Continue
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
		reqCtx,
	)
	if err != nil {
		reqCtx.EndStep("failed", phase3Tokens, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":      "Accounting analysis failed",
			"details":    err.Error(),
			"request_id": reqCtx.RequestID,
		})
		return
	}
	reqCtx.EndStep("success", phase3Tokens, nil)

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

	var validationData map[string]interface{}
	if vd, ok := accountingResponse["validation"].(map[string]interface{}); ok {
		validationData = vd
	} else {
		// Fallback validation (Pure OCR doesn't have validation data)
		validationData = map[string]interface{}{
			"overall_confidence": map[string]interface{}{"level": "medium", "score": 75},
			"requires_review":    true,
		}
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

		// Note: IMPORTANT - Always verify request_id matches your request log!
		// If IDs don't match, this might be a cached/wrong response.

		// Debug data (only included if debug=true query parameter)
		"debug_data": debugData,
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
