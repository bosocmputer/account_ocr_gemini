// interface.go - OCR Provider Interface for supporting multiple AI providers

package ai

import (
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
)

// OCRProvider defines the interface that all OCR providers must implement
// This allows us to support multiple AI providers (Gemini, Mistral, etc.) with the same interface
type OCRProvider interface {
	// ProcessPureOCR processes an image and extracts raw text
	// imagePath: path to the image file
	// reqCtx: request context for logging and tracking
	// Returns: SimpleOCRResult, TokenUsage, and error
	ProcessPureOCR(imagePath string, reqCtx *common.RequestContext) (*SimpleOCRResult, *common.TokenUsage, error)

	// GetProviderName returns the name of the provider (e.g., "gemini", "mistral")
	GetProviderName() string
}

// OCRProviderConfig contains configuration for OCR providers
type OCRProviderConfig struct {
	// Provider name: "gemini" or "mistral"
	Provider string

	// Gemini configuration
	GeminiAPIKey string
	GeminiModel  string

	// Mistral configuration
	MistralAPIKey string
	MistralModel  string
}
