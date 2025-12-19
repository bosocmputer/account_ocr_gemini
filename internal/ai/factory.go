// factory.go - OCR Provider Factory for creating provider instances

package ai

import (
	"fmt"
	"log"

	"github.com/bosocmputer/account_ocr_gemini/configs"
)

// CreateOCRProvider creates an OCR provider based on configuration
func CreateOCRProvider() (OCRProvider, error) {
	provider := configs.OCR_PROVIDER

	switch provider {
	case "gemini":
		log.Printf("🔵 Creating Gemini OCR provider")
		return NewGeminiProvider(configs.GEMINI_API_KEY, configs.OCR_MODEL_NAME), nil

	case "mistral":
		log.Printf("🔷 Creating Mistral OCR provider")
		return NewMistralProvider(configs.MISTRAL_API_KEY, configs.MISTRAL_MODEL_NAME), nil

	default:
		return nil, fmt.Errorf("unsupported OCR provider: %s (supported: gemini, mistral)", provider)
	}
}

// CreateOCRProviderWithFallback creates an OCR provider with automatic fallback
// If the primary provider fails, it will try the fallback provider
func CreateOCRProviderWithFallback() (primary OCRProvider, fallback OCRProvider, err error) {
	// Create primary provider
	primary, err = CreateOCRProvider()
	if err != nil {
		return nil, nil, err
	}

	// Create fallback provider (opposite of primary)
	primaryName := primary.GetProviderName()

	switch primaryName {
	case "gemini":
		// If Gemini is primary and Mistral is configured, use it as fallback
		if configs.MISTRAL_API_KEY != "" {
			fallback = NewMistralProvider(configs.MISTRAL_API_KEY, configs.MISTRAL_MODEL_NAME)
			log.Printf("✅ Fallback provider configured: Mistral")
		}

	case "mistral":
		// If Mistral is primary and Gemini is configured, use it as fallback
		if configs.GEMINI_API_KEY != "" {
			fallback = NewGeminiProvider(configs.GEMINI_API_KEY, configs.OCR_MODEL_NAME)
			log.Printf("✅ Fallback provider configured: Gemini")
		}
	}

	return primary, fallback, nil
}
