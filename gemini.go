// gemini.go - Contains data structs, Gemini API logic, and the OCR placeholder.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/api/option"
)

// --- Structs for Data Handling (JSON Schema) ---

// FlexibleValue stores any value with its raw text and confidence for UI display
type FlexibleValue struct {
	Value      interface{} `json:"value"`      // Parsed value (string, number, bool, etc.)
	RawText    string      `json:"raw_text"`   // Original text AI read
	Confidence float64     `json:"confidence"` // 0-100 score
}

// UnmarshalJSON handles both legacy format (direct value) and new format (object with confidence)
func (fv *FlexibleValue) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as object first (new format)
	type Alias FlexibleValue
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(fv),
	}

	if err := json.Unmarshal(data, &aux); err == nil {
		// Successfully parsed as object, check if it has the expected fields
		if fv.RawText != "" || fv.Confidence != 0 {
			return nil
		}
	}

	// Legacy format: direct value (string, number, bool, null)
	var rawValue interface{}
	if err := json.Unmarshal(data, &rawValue); err != nil {
		return err
	}

	// Convert to FlexibleValue
	fv.Value = rawValue
	fv.RawText = fmt.Sprintf("%v", rawValue)
	fv.Confidence = 95.0 // Default confidence for legacy format

	// Handle null
	if rawValue == nil {
		fv.RawText = ""
		fv.Value = ""
	}

	return nil
}

// GetString returns the string representation of the flexible value
func (fv *FlexibleValue) GetString() string {
	if fv == nil {
		return ""
	}
	if fv.RawText != "" {
		return fv.RawText
	}
	if fv.Value != nil {
		return fmt.Sprintf("%v", fv.Value)
	}
	return ""
}

// ReceiptItem represents a single item from the receipt
type ReceiptItem struct {
	ProductID   FlexibleValue `json:"product_id"`
	Description FlexibleValue `json:"description"`
	Quantity    FlexibleValue `json:"quantity"`
	UnitPrice   FlexibleValue `json:"unit_price"`
	TotalPrice  FlexibleValue `json:"total_price"`
}

// FieldConfidence represents confidence level for a specific field with hybrid scoring
type FieldConfidence struct {
	Level          string  `json:"level"`           // high, medium, low
	Score          int     `json:"score"`           // 0-100 percentage
	RequiresReview bool    `json:"requires_review"` // true if user should verify
	Note           *string `json:"note,omitempty"`  // optional explanation
}

// ItemConfidence represents confidence for all fields in an item
type ItemConfidence struct {
	ProductID   FieldConfidence `json:"product_id"`
	Description FieldConfidence `json:"description"`
	Quantity    FieldConfidence `json:"quantity"`
	UnitPrice   FieldConfidence `json:"unit_price"`
	TotalPrice  FieldConfidence `json:"total_price"`
}

// ValidationCheck represents a single validation check result
type ValidationCheck struct {
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

// ValidationChecks contains all validation results
type ValidationChecks struct {
	MathCheck     ValidationCheck `json:"math_check"`
	BarcodeFormat ValidationCheck `json:"barcode_format"`
	DateFormat    ValidationCheck `json:"date_format"`
}

// OverallConfidence represents overall confidence with hybrid scoring
type OverallConfidence struct {
	Level string `json:"level"` // high, medium, low
	Score int    `json:"score"` // 0-100 percentage
}

// Validation contains all confidence and validation information
type Validation struct {
	OverallConfidence OverallConfidence      `json:"overall_confidence"` // hybrid: level + score
	RequiresReview    bool                   `json:"requires_review"`    // true if any field needs review
	FieldConfidence   map[string]interface{} `json:"field_confidence"`   // confidence for each field
	ValidationChecks  ValidationChecks       `json:"validation_checks"`  // automated validation results
}

// AIMetadata contains information about the AI processing
type AIMetadata struct {
	ModelName        string `json:"model_name"`
	PromptTokens     int32  `json:"prompt_tokens"`
	CandidatesTokens int32  `json:"candidates_tokens"`
	TotalTokens      int32  `json:"total_tokens"`
}

// ExtractionResult represents the complete extraction result from the receipt
type ExtractionResult struct {
	Status        string        `json:"status"`
	ReceiptNumber FlexibleValue `json:"receipt_number"`
	InvoiceDate   FlexibleValue `json:"invoice_date"`
	TotalAmount   FlexibleValue `json:"total_amount"`
	VATAmount     FlexibleValue `json:"vat_amount"`
	Items         []ReceiptItem `json:"items"`
	Validation    Validation    `json:"validation"`
	Metadata      AIMetadata    `json:"metadata"`
	RawResponse   string        `json:"raw_response,omitempty"` // Full AI response for debugging
}

// --- Core Processing Function: OCR (Placeholder) + Gemini (Actual Call) ---

// processOCRAndGemini processes the receipt image and extracts structured data using Gemini API
func processOCRAndGemini(imagePath string, reqCtx *RequestContext) (*ExtractionResult, *TokenUsage, error) {
	// Step 1: Preprocess the image with HIGH QUALITY mode for maximum accuracy
	// This applies aggressive enhancements: sharpen, contrast, brightness, grayscale
	reqCtx.StartSubStep("image_preprocessing")
	imageData, mimeType, err := preprocessImageHighQuality(imagePath)
	reqCtx.EndSubStep("")
	if err != nil {
		// If preprocessing fails, fall back to original image
		fmt.Printf("Warning: High-quality image preprocessing failed, using original: %v\n", err)
		imageData, err = os.ReadFile(imagePath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read image file: %w", err)
		}

		// Detect MIME type from file extension
		mimeType = "jpeg" // default
		ext := strings.ToLower(filepath.Ext(imagePath))
		switch ext {
		case ".png":
			mimeType = "png"
		case ".jpg", ".jpeg":
			mimeType = "jpeg"
		case ".gif":
			mimeType = "gif"
		case ".webp":
			mimeType = "webp"
		}
	}

	// Step 2: Initialize the Gemini client
	reqCtx.StartSubStep("init_gemini_client")
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(GEMINI_API_KEY))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel(MODEL_NAME)
	reqCtx.EndSubStep("")

	// Step 3: Define the detailed JSON schema
	reqCtx.StartSubStep("create_json_schema")
	schema := createSchema()
	reqCtx.EndSubStep("")

	// Step 4: Configure the model with JSON response
	reqCtx.StartSubStep("configure_model")
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = schema
	reqCtx.EndSubStep("")

	// Step 5: Construct the prompt for image analysis with enhanced OCR instructions
	reqCtx.StartSubStep("build_prompt")
	// ใช้ prompt จากไฟล์ prompt_system.go (ภาษาไทย เพื่อให้อ่านและแก้ไขง่าย)
	prompt := GetOCRPrompt()
	reqCtx.EndSubStep("")

	// Step 6: Call the Gemini API with the actual image (with retry logic)
	reqCtx.StartSubStep("call_gemini_api")
	resp, err := callGeminiWithRetry(ctx, model,
		genai.Text(prompt),
		genai.Blob{
			MIMEType: mimeType,
			Data:     imageData,
		},
		reqCtx,
		DefaultRetryConfig,
	)
	if err != nil {
		reqCtx.EndSubStep("❌ FAILED")
		// Check if it's a GeminiError and build user-friendly message
		if gemErr, ok := err.(*GeminiError); ok {
			userMsg := buildUserFriendlyError(gemErr)
			return nil, nil, fmt.Errorf("%s (technical: %w)", userMsg, err)
		}
		return nil, nil, fmt.Errorf("failed to generate content: %w", err)
	}
	reqCtx.EndSubStep("")

	// Extract the JSON response
	reqCtx.StartSubStep("parse_json_response")
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, nil, fmt.Errorf("no response from Gemini API")
	}

	// Get the text response
	var jsonResponse string
	for _, part := range resp.Candidates[0].Content.Parts {
		if text, ok := part.(genai.Text); ok {
			jsonResponse = string(text)
			break
		}
	}

	if jsonResponse == "" {
		return nil, nil, fmt.Errorf("empty response from Gemini API")
	}

	// Step 7: Unmarshal the JSON into ExtractionResult struct
	var result ExtractionResult
	if err := json.Unmarshal([]byte(jsonResponse), &result); err != nil {
		reqCtx.EndSubStep("❌ FAILED")
		return nil, nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	reqCtx.EndSubStep("")

	// Step 8: Add AI metadata
	reqCtx.StartSubStep("extract_metadata")
	result.Metadata = AIMetadata{
		ModelName: MODEL_NAME,
	}

	// Extract token usage if available
	var tokenUsage *TokenUsage
	if resp.UsageMetadata != nil {
		result.Metadata.PromptTokens = resp.UsageMetadata.PromptTokenCount
		result.Metadata.CandidatesTokens = resp.UsageMetadata.CandidatesTokenCount
		result.Metadata.TotalTokens = resp.UsageMetadata.TotalTokenCount

		// Calculate cost
		tokens := CalculateTokenCost(
			int(resp.UsageMetadata.PromptTokenCount),
			int(resp.UsageMetadata.CandidatesTokenCount),
		)
		tokenUsage = &tokens
	}
	reqCtx.EndSubStep(fmt.Sprintf("tokens: %d", tokenUsage.TotalTokens))

	// Debug: Log what AI extracted in Phase 2
	log.Printf("[%s] 📄 PHASE 2 - Full OCR Extraction:", reqCtx.RequestID)
	log.Printf("[%s]   - Receipt #: %v | Date: %v", reqCtx.RequestID, result.ReceiptNumber.Value, result.InvoiceDate.Value)
	log.Printf("[%s]   - Items: %d | Total: %v | VAT: %v", reqCtx.RequestID, len(result.Items), result.TotalAmount.Value, result.VATAmount.Value)

	// Store raw response for debugging
	result.RawResponse = jsonResponse

	return &result, tokenUsage, nil
}

// createSchema creates the JSON schema for the ExtractionResult with confidence tracking
func createSchema() *genai.Schema {
	// Schema for field confidence (hybrid: level + score)
	fieldConfidenceSchema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"level": {
				Type:        genai.TypeString,
				Description: "Confidence level: high, medium, or low",
				Enum:        []string{"high", "medium", "low"},
			},
			"score": {
				Type:        genai.TypeInteger,
				Description: "Confidence score from 0-100 percentage. high=95-100, medium=80-94, low=0-79",
			},
			"requires_review": {
				Type:        genai.TypeBoolean,
				Description: "True if human should verify this field (score < 95)",
			},
			"note": {
				Type:        genai.TypeString,
				Description: "Optional explanation of uncertainty",
			},
		},
		Required: []string{"level", "score", "requires_review"},
	}

	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"status": {
				Type:        genai.TypeString,
				Description: "Status of the extraction (success or error)",
			},
			"document_type_header": {
				Type:        genai.TypeString,
				Description: "CRITICAL: Exact document type header from receipt (e.g., 'ใบเสร็จรับเงิน/ใบกำกับภาษี', 'ใบกำกับภาษี', 'Receipt/Tax Invoice'). This determines payment method.",
			},
			"receipt_number": {
				Type:        genai.TypeString,
				Description: "Receipt number or invoice ID from the receipt",
			},
			"invoice_date": {
				Type:        genai.TypeString,
				Description: "Date of the invoice in DD/MM/YYYY format",
			},
			"total_amount": {
				Type:        genai.TypeNumber,
				Description: "Total amount before VAT",
			},
			"vat_amount": {
				Type:        genai.TypeNumber,
				Description: "VAT amount",
			},
			"items": {
				Type:        genai.TypeArray,
				Description: "Array of receipt items",
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"product_id": {
							Type:        genai.TypeString,
							Description: "Product ID or code",
						},
						"description": {
							Type:        genai.TypeString,
							Description: "Product description",
						},
						"quantity": {
							Type:        genai.TypeNumber,
							Description: "Quantity purchased",
						},
						"unit_price": {
							Type:        genai.TypeNumber,
							Description: "Price per unit",
						},
						"total_price": {
							Type:        genai.TypeNumber,
							Description: "Total price for this item",
						},
					},
					Required: []string{"product_id", "description", "quantity", "unit_price", "total_price"},
				},
			},
			"validation": {
				Type:        genai.TypeObject,
				Description: "Validation and confidence information",
				Properties: map[string]*genai.Schema{
					"overall_confidence": {
						Type:        genai.TypeObject,
						Description: "Overall confidence level for entire extraction (hybrid: level + score)",
						Properties: map[string]*genai.Schema{
							"level": {
								Type:        genai.TypeString,
								Description: "Overall level: high, medium, or low",
								Enum:        []string{"high", "medium", "low"},
							},
							"score": {
								Type:        genai.TypeInteger,
								Description: "Average confidence score across all fields (0-100)",
							},
						},
						Required: []string{"level", "score"},
					},
					"requires_review": {
						Type:        genai.TypeBoolean,
						Description: "True if any field requires human review",
					},
					"field_confidence": {
						Type:        genai.TypeObject,
						Description: "Confidence assessment for each field",
						Properties: map[string]*genai.Schema{
							"receipt_number": fieldConfidenceSchema,
							"invoice_date":   fieldConfidenceSchema,
							"total_amount":   fieldConfidenceSchema,
							"vat_amount":     fieldConfidenceSchema,
							"items": {
								Type:        genai.TypeArray,
								Description: "Confidence for each item's fields",
								Items: &genai.Schema{
									Type: genai.TypeObject,
									Properties: map[string]*genai.Schema{
										"product_id":  fieldConfidenceSchema,
										"description": fieldConfidenceSchema,
										"quantity":    fieldConfidenceSchema,
										"unit_price":  fieldConfidenceSchema,
										"total_price": fieldConfidenceSchema,
									},
								},
							},
						},
					},
					"validation_checks": {
						Type:        genai.TypeObject,
						Description: "Automated validation check results - these will be computed by backend",
						Properties: map[string]*genai.Schema{
							"math_check": {
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"passed":  {Type: genai.TypeBoolean},
									"message": {Type: genai.TypeString},
								},
							},
							"barcode_format": {
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"passed":  {Type: genai.TypeBoolean},
									"message": {Type: genai.TypeString},
								},
							},
							"date_format": {
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"passed":  {Type: genai.TypeBoolean},
									"message": {Type: genai.TypeString},
								},
							},
						},
					},
				},
			},
		},
		Required: []string{"status", "document_type_header", "receipt_number", "invoice_date", "total_amount", "vat_amount", "items", "validation"},
	}
}

// --- Phase 1: Quick OCR (REMOVED) ---
// QuickOCRResult struct removed - no longer needed after removing Phase 1

// FlexibleFloat64 can unmarshal from both string and number
type FlexibleFloat64 float64

func (f *FlexibleFloat64) UnmarshalJSON(data []byte) error {
	// Handle null
	if string(data) == "null" {
		*f = 0
		return nil
	}

	// Try as number first
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		*f = FlexibleFloat64(num)
		return nil
	}

	// Try as string
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return fmt.Errorf("cannot unmarshal %s as float64 or string", string(data))
	}

	// Handle empty string
	str = strings.TrimSpace(str)
	if str == "" {
		*f = 0
		return nil
	}

	// Parse string to float
	num, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return fmt.Errorf("cannot parse string %q as float64: %w", str, err)
	}

	*f = FlexibleFloat64(num)
	return nil
}

// Helper functions to create FlexibleValue from raw Gemini response

// MakeFlexibleString creates FlexibleValue from string with confidence
func MakeFlexibleString(value string, confidence float64) FlexibleValue {
	return FlexibleValue{
		Value:      value,
		RawText:    value,
		Confidence: confidence,
	}
}

// MakeFlexibleFloat creates FlexibleValue from float64 with confidence
func MakeFlexibleFloat(value float64, rawText string, confidence float64) FlexibleValue {
	return FlexibleValue{
		Value:      value,
		RawText:    rawText,
		Confidence: confidence,
	}
}

// ParseFlexibleNumber attempts to parse any value as float64 for FlexibleValue
func ParseFlexibleNumber(raw interface{}, confidence float64) FlexibleValue {
	rawText := fmt.Sprintf("%v", raw)

	switch v := raw.(type) {
	case float64:
		return MakeFlexibleFloat(v, rawText, confidence)
	case int:
		return MakeFlexibleFloat(float64(v), rawText, confidence)
	case string:
		if num, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return MakeFlexibleFloat(num, v, confidence)
		}
		// If can't parse as number, return as string
		return MakeFlexibleString(v, confidence)
	default:
		return MakeFlexibleString(rawText, confidence)
	}
}

// --- Phase 1: Quick OCR (REMOVED FOR PERFORMANCE) ---
// processQuickOCR function has been removed to save ~21 seconds per request (28% faster)
// System now goes directly to Full OCR extraction (processOCRAndGemini)
// This eliminates redundant image quality checks and reduces token usage

// --- Phase 2: Accounting Analysis (REMOVED) ---
// Old processAccountingAnalysis function has been removed
// System now uses processMultiImageAccountingAnalysis for all accounting analysis

// processMultiImageAccountingAnalysis analyzes multiple images and creates merged accounting entries
func processMultiImageAccountingAnalysis(downloadedImages interface{}, fullResults interface{}, accounts []bson.M, journalBooks []bson.M, creditors []bson.M, documentTemplates []bson.M, reqCtx *RequestContext) (string, *TokenUsage, error) {
	// Convert all OCR results to JSON for AI analysis
	allResultsJSON, _ := json.MarshalIndent(map[string]interface{}{
		"full_ocr_results":  fullResults,
		"downloaded_images": downloadedImages,
	}, "", "  ")

	// Build multi-image accounting prompt
	prompt := BuildMultiImageAccountingPrompt(string(allResultsJSON), accounts, journalBooks, creditors, documentTemplates)

	// Call Gemini API
	reqCtx.StartSubStep("init_gemini_client")
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(GEMINI_API_KEY))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel(MODEL_NAME)
	model.SetTemperature(0.2)
	reqCtx.EndSubStep("")

	reqCtx.StartSubStep("call_gemini_api")
	// For multi-image analysis, we pass all OCR data as text in the prompt
	// Images already analyzed in previous steps
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))

	if err != nil {
		reqCtx.EndSubStep("❌ FAILED")
		if gemErr, ok := err.(*GeminiError); ok {
			userMsg := buildUserFriendlyError(gemErr)
			return "", nil, fmt.Errorf("%s (technical: %w)", userMsg, err)
		}
		return "", nil, fmt.Errorf("Gemini API call failed: %w", err)
	}
	reqCtx.EndSubStep("")

	reqCtx.StartSubStep("parse_json_response")
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", nil, fmt.Errorf("no response from Gemini")
	}

	responseText := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)
	reqCtx.EndSubStep("")

	// Debug: Log what AI decided for multi-image accounting
	var accountingResult map[string]interface{}
	if err := json.Unmarshal([]byte(responseText), &accountingResult); err == nil {
		log.Printf("[%s] 💼 PHASE 3 - Multi-Image Accounting Analysis:", reqCtx.RequestID)

		// Log document analysis
		if docAnalysis, ok := accountingResult["document_analysis"].(map[string]interface{}); ok {
			log.Printf("[%s]   - Relationship: %v (Confidence: %v%%)",
				reqCtx.RequestID, docAnalysis["relationship"], docAnalysis["confidence"])
		}

		// Log creditor selection
		if creditor, ok := accountingResult["creditor"].(map[string]interface{}); ok {
			log.Printf("[%s]   - Creditor: %v | Name: %v", reqCtx.RequestID, creditor["creditor_code"], creditor["creditor_name"])
		}

		// Log journal entries
		if entries, ok := accountingResult["journal_entries"].([]interface{}); ok {
			log.Printf("[%s]   - Journal Entries (%d):", reqCtx.RequestID, len(entries))
			for i, entry := range entries {
				if e, ok := entry.(map[string]interface{}); ok {
					log.Printf("[%s]     %d. %s | %s | Dr: %.2f | Cr: %.2f",
						reqCtx.RequestID, i+1, e["journal_book_code"], e["account"], e["debit"], e["credit"])
				}
			}
		}
	}

	// Calculate token usage
	var tokenUsage *TokenUsage
	if resp.UsageMetadata != nil {
		tokens := CalculateTokenCost(
			int(resp.UsageMetadata.PromptTokenCount),
			int(resp.UsageMetadata.CandidatesTokenCount),
		)
		tokenUsage = &tokens
	}

	return responseText, tokenUsage, nil
}
