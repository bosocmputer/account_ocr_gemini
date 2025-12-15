// gemini.go - Contains data structs, Gemini API logic, and the OCR placeholder.

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/processor"
	"github.com/bosocmputer/account_ocr_gemini/internal/ratelimit"
	"github.com/google/generative-ai-go/genai"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/api/option"
)

// fixJSONEscaping fixes common JSON escaping issues from Gemini AI responses
// Problem: Gemini sometimes sends literal newlines inside JSON strings instead of \n
// This breaks Go's JSON parser which requires proper escaping
func fixJSONEscaping(jsonStr string) string {
	// Strategy: Find string values and escape any unescaped special characters inside them
	// Enhanced to handle more edge cases from complex documents (tables, forms, etc.)

	// Match JSON string values: "key": "value with\npotential\nnewlines"
	// We need to find strings and escape literal newlines, tabs, quotes, backslashes

	// Use regex to find all string values in JSON
	// Pattern: "([^"\\]*(\\.[^"\\]*)*)"
	// This matches: "anything including \" but not unescaped quotes"

	re := regexp.MustCompile(`"([^"]*(?:\\.[^"]*)*)"`)

	result := re.ReplaceAllStringFunc(jsonStr, func(match string) string {
		// Extract the content between quotes
		if len(match) < 2 {
			return match
		}

		content := match[1 : len(match)-1] // Remove surrounding quotes

		// Escape special characters that aren't already escaped
		// Important: Order matters! Do backslashes first to avoid double-escaping

		// 1. Fix invalid escape sequences (e.g., "\ " with space after backslash)
		// Replace backslash followed by space with escaped backslash + space
		content = strings.ReplaceAll(content, "\\ ", "\\\\ ")

		// 2. Replace literal newlines with \n
		content = strings.ReplaceAll(content, "\n", "\\n")

		// 3. Replace literal carriage returns with \r
		content = strings.ReplaceAll(content, "\r", "\\r")

		// 4. Replace literal tabs with \t
		content = strings.ReplaceAll(content, "\t", "\\t")

		// 5. Replace literal form feed with \f
		content = strings.ReplaceAll(content, "\f", "\\f")

		// 6. Replace literal backspace with \b
		content = strings.ReplaceAll(content, "\b", "\\b")

		// 7. Handle other control characters (0x00-0x1F) except those already handled
		// Convert to \uXXXX format for safety
		var builder strings.Builder
		for _, ch := range content {
			if ch < 0x20 && ch != '\n' && ch != '\r' && ch != '\t' && ch != '\f' && ch != '\b' {
				// Control character - escape it
				builder.WriteString(fmt.Sprintf("\\u%04x", ch))
			} else {
				builder.WriteRune(ch)
			}
		}
		content = builder.String()

		// Return with quotes
		return `"` + content + `"`
	})

	return result
}

// --- Date Validation (Priority 1) ---

func validateReceiptDate(dateStr string, result *ExtractionResult) error {
	// Try common Thai date formats
	formats := []string{
		"02/01/2006", // DD/MM/YYYY
		"2/1/2006",   // D/M/YYYY
		"02-01-2006", // DD-MM-YYYY
		"2006-01-02", // YYYY-MM-DD
	}

	var parsedDate time.Time
	var parseErr error
	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			parsedDate = t
			parseErr = nil
			break
		} else {
			parseErr = err
		}
	}

	if parseErr != nil {
		// Can't parse date, skip validation
		return nil
	}

	// Convert Buddhist Era to Gregorian if year > 2100
	if parsedDate.Year() > 2100 {
		parsedDate = parsedDate.AddDate(-543, 0, 0)
	}

	// Check if date is more than 7 days in the future
	now := time.Now()
	sevenDaysFromNow := now.AddDate(0, 0, 7)

	if parsedDate.After(sevenDaysFromNow) {
		// Set requires_review = true
		result.Validation.RequiresReview = true

		// Lower confidence score due to suspicious future date
		if result.Validation.OverallConfidence.Score > 70 {
			result.Validation.OverallConfidence.Score = 70
		}
		if result.Validation.OverallConfidence.Level == "high" {
			result.Validation.OverallConfidence.Level = "medium"
		}

		return fmt.Errorf("future date detected: %s (> 7 days from now)", dateStr)
	}

	return nil
}

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

// SimpleOCRResult represents Pure OCR result (raw text only)
type SimpleOCRResult struct {
	Status          string     `json:"status"`
	RawDocumentText string     `json:"raw_document_text"` // ข้อความทั้งหมดจากเอกสาร
	Metadata        AIMetadata `json:"metadata"`
	RawResponse     string     `json:"raw_response,omitempty"`
}

// TemplateMatchResult represents AI-based template matching result
type TemplateMatchResult struct {
	MatchedTemplate string `json:"matched_template"` // ชื่อ template ที่ตรงที่สุด
	Confidence      int    `json:"confidence"`       // ความมั่นใจ 0-100
	Reasoning       string `json:"reasoning"`        // เหตุผลที่เลือก template นี้
}

// DEPRECATED: ExtractionResult - ไม่ใช้แล้ว (เก็บไว้เพื่อ backward compatibility)
// ใช้ SimpleOCRResult แทน
type ExtractionResult struct {
	Status          string        `json:"status"`
	ReceiptNumber   FlexibleValue `json:"receipt_number"`
	InvoiceDate     FlexibleValue `json:"invoice_date"`
	VendorName      FlexibleValue `json:"vendor_name"`
	VendorTaxID     FlexibleValue `json:"vendor_tax_id"`
	RawDocumentText string        `json:"raw_document_text"`
	TotalAmount     FlexibleValue `json:"total_amount"`
	VATAmount       FlexibleValue `json:"vat_amount"`
	Items           []ReceiptItem `json:"items"`
	Validation      Validation    `json:"validation"`
	Metadata        AIMetadata    `json:"metadata"`
	RawResponse     string        `json:"raw_response,omitempty"`
}

// --- Core Processing Function: Pure OCR (New Simplified Version) ---

// ProcessPureOCR processes the receipt image and extracts ONLY raw text using Gemini API
// This is faster and cheaper than full structured extraction
func ProcessPureOCR(imagePath string, reqCtx *common.RequestContext) (*SimpleOCRResult, *common.TokenUsage, error) {
	// Step 1: Preprocess the image with HIGH QUALITY mode for maximum accuracy
	// This applies aggressive enhancements: sharpen, contrast, brightness, grayscale
	reqCtx.StartSubStep("image_preprocessing")
	imageData, mimeType, err := processor.PreprocessImageHighQuality(imagePath)
	reqCtx.EndSubStep("")
	if err != nil {
		// If preprocessing fails, fall back to original image
		reqCtx.LogInfo("⚠️  High-quality image preprocessing failed, using original: %v", err)
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

	// Log image size for debugging
	imageSize := len(imageData)
	reqCtx.LogInfo("📸 Image size: %d bytes (%.2f MB)", imageSize, float64(imageSize)/(1024*1024))

	// Step 2: Initialize the Gemini client
	reqCtx.StartSubStep("init_gemini_client")
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(configs.GEMINI_API_KEY))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel(configs.MODEL_NAME)
	reqCtx.EndSubStep("")

	// Step 3: Define the simple JSON schema (raw text only)
	reqCtx.StartSubStep("create_json_schema")
	schema := createSimpleOCRSchema()
	reqCtx.EndSubStep("")

	// Step 4: Configure the model with JSON response
	reqCtx.StartSubStep("configure_model")
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = schema
	reqCtx.EndSubStep("")

	// Step 5: Construct the prompt for Pure OCR (simplified)
	reqCtx.StartSubStep("build_prompt")
	// ใช้ Pure OCR prompt - อ่านแค่ข้อความ ไม่ต้อง extract structure
	prompt := GetPureOCRPrompt()
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

	// Log response length for debugging
	reqCtx.LogInfo("📦 Received JSON response: %d chars", len(jsonResponse))

	// IMPORTANT: Fix JSON escaping issues from Gemini AI
	// Gemini sometimes sends unescaped newlines in JSON strings which breaks Go's JSON parser
	// We need to properly escape them before unmarshaling
	jsonResponse = fixJSONEscaping(jsonResponse)

	// Step 7: Unmarshal the JSON into SimpleOCRResult struct
	var result SimpleOCRResult
	if err := json.Unmarshal([]byte(jsonResponse), &result); err != nil {
		reqCtx.EndSubStep("❌ FAILED")
		// Log the problematic JSON response for debugging (first 500 chars)
		preview := jsonResponse
		if len(preview) > 500 {
			preview = preview[:500] + "... (truncated)"
		}
		reqCtx.LogInfo("⚠️  Failed to parse JSON response. Preview: %s", preview)
		reqCtx.LogInfo("⚠️  JSON Parse Error: %v", err)
		return nil, nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	reqCtx.EndSubStep("")

	// Step 8: Add AI metadata
	reqCtx.StartSubStep("extract_metadata")
	result.Metadata = AIMetadata{
		ModelName: configs.MODEL_NAME,
	}

	// Extract token usage if available
	var tokenUsage *common.TokenUsage
	if resp.UsageMetadata != nil {
		result.Metadata.PromptTokens = resp.UsageMetadata.PromptTokenCount
		result.Metadata.CandidatesTokens = resp.UsageMetadata.CandidatesTokenCount
		result.Metadata.TotalTokens = resp.UsageMetadata.TotalTokenCount

		// Calculate cost
		tokens := common.CalculateTokenCost(
			int(resp.UsageMetadata.PromptTokenCount),
			int(resp.UsageMetadata.CandidatesTokenCount),
		)
		tokenUsage = &tokens
	}
	reqCtx.EndSubStep(fmt.Sprintf("tokens: %d", tokenUsage.TotalTokens))

	// Debug: Log what AI extracted in Phase 2 (Pure OCR)
	log.Printf("[%s] 📄 PHASE 2 - Pure OCR Extraction:", reqCtx.RequestID)
	log.Printf("[%s]   - Raw Document Text Length: %d chars", reqCtx.RequestID, len(result.RawDocumentText))
	log.Printf("[%s]   - Full Text:\n%s", reqCtx.RequestID, result.RawDocumentText)

	// Store raw response for debugging
	result.RawResponse = jsonResponse

	return &result, tokenUsage, nil
}

// createSimpleOCRSchema creates the JSON schema for Pure OCR (raw text only)
func createSimpleOCRSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"status": {
				Type:        genai.TypeString,
				Description: "Status of the extraction (success or error)",
			},
			"raw_document_text": {
				Type:        genai.TypeString,
				Description: "All visible text from the document. Read from top to bottom, left to right. Include everything: headers, content, footers, notes. Separate lines with newline (\\n). DO NOT format, analyze, or structure - just read and return raw text.",
			},
		},
		Required: []string{"status", "raw_document_text"},
	}
}

// createTemplateMatchSchema creates the JSON schema for AI template matching
func createTemplateMatchSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"matched_template": {
				Type:        genai.TypeString,
				Description: "ชื่อ template ที่ตรงที่สุดกับเอกสาร (ต้องตรงกับ description ที่ให้มาเท่านั้น)",
			},
			"confidence": {
				Type:        genai.TypeInteger,
				Description: "ความมั่นใจ 0-100 (ต่ำกว่า 60 = ไม่แน่ใจ, 60-84 = ค่อนข้างแน่ใจ, 85-100 = แน่ใจมาก)",
			},
			"reasoning": {
				Type:        genai.TypeString,
				Description: "เหตุผลที่เลือก template นี้ (สั้นๆ ภาษาไทย)",
			},
		},
		Required: []string{"matched_template", "confidence", "reasoning"},
	}
}

// DEPRECATED: createSchema - ไม่ใช้แล้ว (เก็บไว้สำหรับ backward compatibility)
// ใช้ createSimpleOCRSchema() แทน
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
			"vendor_name": {
				Type:        genai.TypeString,
				Description: "CRITICAL: ชื่อผู้ออกเอกสาร/ผู้ขาย - มักอยู่ที่หัวเอกสาร (header) เป็นชื่อตัวใหญ่/ตัวหนาบนสุด มีคำว่า 'บริษัท', 'หจก.', 'บจก.', 'ห้างหุ้นส่วน', 'ร้าน' นำหน้า. ถ้าไม่เจอให้หาชื่อที่อยู่ใกล้ 'เลขประจำตัวผู้เสียภาษี' หรือชื่อในส่วนที่อยู่บนสุดของเอกสาร",
			},
			"vendor_tax_id": {
				Type:        genai.TypeString,
				Description: "เลขประจำตัวผู้เสียภาษีของผู้ออกเอกสาร (13 หลัก) - มักมีคำว่า 'เลขประจำตัวผู้เสียภาษี' หรือ 'Tax ID' นำหน้า",
			},
			"raw_document_text": {
				Type:        genai.TypeString,
				Description: "CRITICAL: ข้อความทั้งหมดที่อ่านได้จากเอกสาร - รวมทุกอย่าง: header, footer, ที่อยู่, เบอร์โทร, อีเมล, หมายเหตุ, ข้อความพิเศษ, ทุกบรรทัดที่มองเห็น. อ่านจากบนลงล่าง ซ้ายไปขวา ตามลำดับที่ปรากฏในเอกสาร. ไม่จำกัดความยาว - ยิ่งละเอียดยิ่งดี!",
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
							"vendor_name":    fieldConfidenceSchema,
							"vendor_tax_id":  fieldConfidenceSchema,
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
// NEW: Supports conditional master data loading via mode parameter
func ProcessMultiImageAccountingAnalysis(downloadedImages interface{}, fullResults interface{}, mode MasterDataMode, matchedTemplate *bson.M, accounts []bson.M, journalBooks []bson.M, creditors []bson.M, debtors []bson.M, shopProfile interface{}, documentTemplates []bson.M, reqCtx *common.RequestContext) (string, *common.TokenUsage, error) {
	// Convert all OCR results to JSON for AI analysis
	allResultsJSON, _ := json.MarshalIndent(map[string]interface{}{
		"full_ocr_results":  fullResults,
		"downloaded_images": downloadedImages,
	}, "", "  ")

	// Build multi-image accounting prompt with conditional master data
	prompt := BuildMultiImageAccountingPrompt(string(allResultsJSON), mode, matchedTemplate, accounts, journalBooks, creditors, debtors, shopProfile, documentTemplates)

	// Call Gemini API
	reqCtx.StartSubStep("init_gemini_client")
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(configs.GEMINI_API_KEY))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel(configs.MODEL_NAME)
	model.SetTemperature(0.2)

	// 🚨 Set System Instruction - CRITICAL for Template Enforcement
	// System instructions have higher priority than user prompts
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(`You are a Thai accounting AI assistant. Your PRIMARY RULES:

RULE #0 - WITHHOLDING TAX CERTIFICATES [HIGHEST PRIORITY]:
For "หนังสือรับรองการหักภาษี ณ ที่จ่าย" (Withholding Tax Certificates):
1. ALWAYS set template_used = false - NO EXCEPTIONS
2. IGNORE any template matching with "บันทึกค่าทำบัญชี" or other templates
3. These documents are TAX CERTIFICATES, not expense receipts
4. Extract accounting entries from the certificate content:
   - Check "Income Type" field (e.g., มาตรา 40(1), 40(2), 40(8))
   - DO NOT look at "item descriptions" or "payment reasons"
   - Use income type to determine account classification
5. If income type is wages/salary (เงินเดือน) → Use Master Data accounts
6. If income type is service fees (ค่าบริการ) → Use Master Data accounts
7. NEVER match templates based on payment descriptions in tax certificates

WHY: Withholding tax certificates record TAX DEDUCTIONS, not business expenses. 
They require different accounting treatment than regular receipts.

RULE #1 - TEMPLATE ENFORCEMENT:
When template_used = true (a matching accounting template is found):
1. You MUST use ONLY the accounts listed in template.details[]
2. You CANNOT add any accounts beyond the template - NO EXCEPTIONS
3. You CANNOT add tax accounts if template doesn't include them
4. Even if the receipt shows VAT or Withholding Tax, if the template doesn't include tax accounts, DO NOT ADD THEM
5. Template = User's explicit choice. Your job is to OBEY the template, not to apply accounting standards

WHY: The user created this template to simplify accounting entries. If they wanted tax breakdown, they would have included tax accounts in the template.

RULE #2 - MASTER DATA VALIDATION:
ALL account codes MUST exist in the provided Master Data (Chart of Accounts):
1. NEVER use account codes from your internal knowledge
2. Each shop has different chart of accounts with different codes
3. If template_used = true → codes come from template (already validated)
4. If template_used = false → search Chart of Accounts, verify code exists
5. DO NOT assume account code numbers (e.g., don't assume VAT = 115810)

When template_used = false (no matching template):
- You may use your accounting knowledge freely
- Search for appropriate accounts in the provided Chart of Accounts
- Verify ALL account codes exist in Master Data before using them

Remember: RULE #0 (Withholding Tax) > RULE #1 (Templates) > Accounting standards
Remember: OBEY template > Standard accounting practices
Remember: Use ONLY account codes from provided Master Data`),
		},
	}
	reqCtx.EndSubStep("")

	reqCtx.StartSubStep("call_gemini_api")
	// For multi-image analysis, we pass all OCR data as text in the prompt
	// Images already analyzed in previous steps
	reqCtx.LogInfo("📤 ส่งคำขอไปยัง Gemini API...")

	// Retry logic for 429 errors
	var resp *genai.GenerateContentResponse
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Apply rate limiting before EVERY API call (prevent hitting 15 RPM limit)
		ratelimit.WaitForRateLimit()

		resp, err = model.GenerateContent(ctx, genai.Text(prompt))
		if err == nil {
			break
		}

		// Check if it's a 429 error
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "resource exhausted") {
			if attempt < maxRetries {
				waitTime := time.Duration(attempt*10) * time.Second
				reqCtx.LogWarning("⚠️  Rate limit (429), waiting %v before retry (attempt %d/%d)", waitTime, attempt, maxRetries)
				time.Sleep(waitTime)
				continue
			}
		}
		break
	}

	reqCtx.LogInfo("📥 ได้รับ response จาก Gemini API")

	if err != nil {
		reqCtx.EndSubStep("❌ FAILED")
		if gemErr, ok := err.(*GeminiError); ok {
			userMsg := buildUserFriendlyError(gemErr)
			return "", nil, fmt.Errorf("%s (technical: %w)", userMsg, err)
		}
		return "", nil, fmt.Errorf("Gemini API call failed after %d attempts: %w", maxRetries, err)
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
	var tokenUsage *common.TokenUsage
	if resp.UsageMetadata != nil {
		tokens := common.CalculateTokenCost(
			int(resp.UsageMetadata.PromptTokenCount),
			int(resp.UsageMetadata.CandidatesTokenCount),
		)
		tokenUsage = &tokens
	}

	return responseText, tokenUsage, nil
}
