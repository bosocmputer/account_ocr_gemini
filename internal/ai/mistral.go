// mistral.go - Mistral AI client for OCR processing

package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/processor"
)

// MistralProvider implements OCRProvider interface for Mistral AI
type MistralProvider struct {
	apiKey    string
	modelName string
	client    *http.Client
}

// NewMistralProvider creates a new Mistral AI provider
func NewMistralProvider(apiKey, modelName string) *MistralProvider {
	return &MistralProvider{
		apiKey:    apiKey,
		modelName: modelName,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// GetProviderName returns "mistral"
func (m *MistralProvider) GetProviderName() string {
	return "mistral"
}

// Mistral OCR API request/response structures
type mistralOCRDocument struct {
	Type        string `json:"type"`                   // "image_url", "document_url", or "file"
	ImageURL    string `json:"image_url,omitempty"`    // base64 data URL for type="image_url"
	DocumentURL string `json:"document_url,omitempty"` // URL for type="document_url"
	FileID      string `json:"file_id,omitempty"`      // file ID for type="file"
}

type mistralOCRRequest struct {
	Model    string             `json:"model"`
	Document mistralOCRDocument `json:"document"`
}

type mistralOCRPageDimensions struct {
	DPI    int `json:"dpi"`
	Height int `json:"height"`
	Width  int `json:"width"`
}

type mistralOCRPage struct {
	Index      int                      `json:"index"`
	Markdown   string                   `json:"markdown"`
	Images     []interface{}            `json:"images"`
	Dimensions mistralOCRPageDimensions `json:"dimensions"`
	Tables     []interface{}            `json:"tables"`
	Hyperlinks []interface{}            `json:"hyperlinks"`
	Header     interface{}              `json:"header"`
	Footer     interface{}              `json:"footer"`
}

type mistralOCRUsageInfo struct {
	PagesProcessed int `json:"pages_processed"`
	DocSizeBytes   int `json:"doc_size_bytes,omitempty"`
}

type mistralOCRResponse struct {
	Model     string              `json:"model"`
	Pages     []mistralOCRPage    `json:"pages"`
	UsageInfo mistralOCRUsageInfo `json:"usage_info"`
}

type mistralErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// ProcessPureOCR processes image using Mistral AI
func (m *MistralProvider) ProcessPureOCR(imagePath string, reqCtx *common.RequestContext) (*SimpleOCRResult, *common.TokenUsage, error) {
	reqCtx.LogInfo("🔷 Using Mistral AI provider (model: %s)", m.modelName)

	// Step 1: Check if imagePath is a URL (from frontend)
	reqCtx.StartSubStep("mistral_ocr_api_call")

	var request mistralOCRRequest

	// If imagePath is a URL (starts with http:// or https://), use it directly
	if strings.HasPrefix(imagePath, "http://") || strings.HasPrefix(imagePath, "https://") {
		reqCtx.LogInfo("📊 Using URL directly: %s", imagePath)
		request = mistralOCRRequest{
			Model: m.modelName,
			Document: mistralOCRDocument{
				Type:        "document_url",
				DocumentURL: imagePath,
			},
		}
	} else {
		// For local files, need to preprocess and convert to base64
		reqCtx.EndSubStep("")
		reqCtx.StartSubStep("image_preprocessing")
		imageData, mimeType, err := processor.PreprocessImageHighQuality(imagePath)
		reqCtx.EndSubStep("")
		if err != nil {
			reqCtx.LogInfo("⚠️  High-quality preprocessing failed, using original: %v", err)
			imageData, err = os.ReadFile(imagePath)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read file: %w", err)
			}

			// Detect MIME type
			mimeType = "image/jpeg"
			ext := strings.ToLower(filepath.Ext(imagePath))
			switch ext {
			case ".pdf":
				mimeType = "application/pdf"
			case ".png":
				mimeType = "image/png"
			case ".jpg", ".jpeg":
				mimeType = "image/jpeg"
			case ".gif":
				mimeType = "image/gif"
			case ".webp":
				mimeType = "image/webp"
			}
		}

		reqCtx.LogInfo("📊 Image size: %.2f KB, MIME type: %s", float64(len(imageData))/1024.0, mimeType)

		// Mistral OCR API does not support PDF as base64
		if mimeType == "application/pdf" {
			return nil, nil, fmt.Errorf("mistral OCR API does not support PDF files sent as base64. Please use an image format (JPEG, PNG, etc.) or provide a URL")
		}

		// For images, encode to base64 with proper MIME type
		base64Image := base64.StdEncoding.EncodeToString(imageData)
		imageURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Image)

		reqCtx.StartSubStep("mistral_ocr_api_call")
		request = mistralOCRRequest{
			Model: m.modelName,
			Document: mistralOCRDocument{
				Type:     "image_url",
				ImageURL: imageURL,
			},
		}
	}

	// Step 4: Call Mistral OCR API
	response, err := m.callMistralOCRAPI(request)
	reqCtx.EndSubStep("")
	if err != nil {
		return nil, nil, fmt.Errorf("mistral OCR API call failed: %w", err)
	}

	// Step 5: Extract text from response
	if len(response.Pages) == 0 {
		return nil, nil, fmt.Errorf("no pages returned from Mistral OCR API")
	}

	// Combine all pages' markdown content
	var extractedText strings.Builder
	for i, page := range response.Pages {
		if i > 0 {
			extractedText.WriteString("\n\n")
		}
		extractedText.WriteString(page.Markdown)
	}
	finalText := extractedText.String()
	reqCtx.LogInfo("✅ Extracted text from %d page(s), length: %d characters", len(response.Pages), len(finalText))

	// Log extracted text preview (similar to Gemini)
	previewLength := 500
	if len(finalText) < previewLength {
		previewLength = len(finalText)
	}
	if previewLength > 0 {
		reqCtx.LogInfo("📄 Extracted Text Preview (first %d chars):\n%s", previewLength, finalText[:previewLength])
		if len(finalText) > previewLength {
			reqCtx.LogInfo("... (และอีก %d ตัวอักษร)", len(finalText)-previewLength)
		}
	}

	// Step 6: Calculate costs
	// Mistral OCR 3: $2 per 1,000 pages
	pagesProcessed := response.UsageInfo.PagesProcessed
	costPerPage := 0.002 // $2 / 1000 = $0.002 per page
	totalCostUSD := float64(pagesProcessed) * costPerPage
	totalCostTHB := totalCostUSD * configs.USD_TO_THB

	tokenUsage := &common.TokenUsage{
		InputTokens:  pagesProcessed, // Store pages as "tokens" for compatibility
		OutputTokens: 0,
		TotalTokens:  pagesProcessed,
		CostUSD:      totalCostUSD,
		CostTHB:      totalCostTHB,
	}

	reqCtx.LogInfo("💰 Cost: %d page(s) × $%.3f = $%.6f USD (%.2f THB)", pagesProcessed, costPerPage, totalCostUSD, totalCostTHB)

	// Step 7: Build result
	result := &SimpleOCRResult{
		Status:          "success",
		RawDocumentText: finalText,
		IsPartial:       false,
		TextLength:      len(finalText),
		FallbackUsed:    false,
		Metadata: AIMetadata{
			ModelName:        response.Model,
			PromptTokens:     int32(pagesProcessed),
			CandidatesTokens: 0,
			TotalTokens:      int32(pagesProcessed),
		},
	}

	return result, tokenUsage, nil
}

// callMistralOCRAPI makes HTTP request to Mistral OCR API
func (m *MistralProvider) callMistralOCRAPI(request mistralOCRRequest) (*mistralOCRResponse, error) {
	// Marshal request
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request to OCR endpoint
	req, err := http.NewRequestWithContext(
		context.Background(),
		"POST",
		"https://api.mistral.ai/v1/ocr",
		bytes.NewBuffer(requestBody),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.apiKey))

	// Send request
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		var errorResp mistralErrorResponse
		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error.Message != "" {
			return nil, fmt.Errorf("mistral OCR API error (%d): %s", resp.StatusCode, errorResp.Error.Message)
		}
		return nil, fmt.Errorf("mistral OCR API error (%d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var response mistralOCRResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse OCR response: %w", err)
	}

	return &response, nil
}
