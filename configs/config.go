// config.go - Configuration loaded from environment variables

package configs

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

var (
	// OCR Provider Configuration
	OCR_PROVIDER string // "gemini" or "mistral"

	// Gemini AI Configuration
	GEMINI_API_KEY string

	// Mistral AI Configuration
	MISTRAL_API_KEY    string
	MISTRAL_MODEL_NAME string

	// Phase-specific Model Configuration
	OCR_MODEL_NAME                 string
	TEMPLATE_MODEL_NAME            string
	TEMPLATE_ACCOUNTING_MODEL_NAME string // For template-only mode (high confidence)
	ACCOUNTING_MODEL_NAME          string // For full analysis mode (low confidence)

	// Gemini Pricing Configuration (hardcoded based on official Gemini API pricing)
	// Gemini 2.5 Flash-Lite: $0.10 input, $0.40 output per 1M tokens
	// Gemini 2.5 Flash: $0.30 input, $2.50 output per 1M tokens
	OCR_INPUT_PRICE_PER_MILLION                  = 0.10
	OCR_OUTPUT_PRICE_PER_MILLION                 = 0.40
	TEMPLATE_INPUT_PRICE_PER_MILLION             = 0.10
	TEMPLATE_OUTPUT_PRICE_PER_MILLION            = 0.40
	TEMPLATE_ACCOUNTING_INPUT_PRICE_PER_MILLION  = 0.10
	TEMPLATE_ACCOUNTING_OUTPUT_PRICE_PER_MILLION = 0.40
	ACCOUNTING_INPUT_PRICE_PER_MILLION           = 0.30
	ACCOUNTING_OUTPUT_PRICE_PER_MILLION          = 2.50

	USD_TO_THB float64 // Exchange rate from .env

	// Server Configuration
	PORT            string
	UPLOAD_DIR      string
	ALLOWED_ORIGINS string

	// MongoDB Configuration
	MONGO_URI     string
	MONGO_DB_NAME string

	// Image preprocessing settings
	ENABLE_IMAGE_PREPROCESSING bool
	MAX_IMAGE_DIMENSION        int

	// Performance optimization settings
	ENABLE_QUICK_OCR    bool // Enable/disable quick OCR phase (can skip to save time)
	QUICK_OCR_TIMEOUT   int  // Timeout for quick OCR in seconds
	FULL_OCR_TIMEOUT    int  // Timeout for full OCR in seconds
	ACCOUNTING_TIMEOUT  int  // Timeout for accounting analysis in seconds
	PARALLEL_PROCESSING bool // Enable parallel image processing
	USE_SMALLER_MODEL   bool // Use smaller/faster model when speed is priority

	// Confidence threshold settings for validation
	CONFIDENCE_HIGH_THRESHOLD   = "high"   // AI is very confident
	CONFIDENCE_MEDIUM_THRESHOLD = "medium" // AI has some uncertainty
	CONFIDENCE_LOW_THRESHOLD    = "low"    // AI is uncertain, requires review
)

// LoadConfig loads configuration from environment variables
func LoadConfig() {
	// Load .env file if exists (for local development)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// OCR Provider Selection
	OCR_PROVIDER = getEnv("OCR_PROVIDER", "gemini")

	// Gemini API Key
	GEMINI_API_KEY = getEnv("GEMINI_API_KEY", "")

	// Mistral API Configuration
	MISTRAL_API_KEY = getEnv("MISTRAL_API_KEY", "")
	MISTRAL_MODEL_NAME = getEnv("MISTRAL_MODEL_NAME", "mistral-ocr-latest")

	// Validate API keys based on provider
	if OCR_PROVIDER == "gemini" && GEMINI_API_KEY == "" {
		log.Fatal("GEMINI_API_KEY is required when OCR_PROVIDER=gemini")
	}
	if OCR_PROVIDER == "mistral" && MISTRAL_API_KEY == "" {
		log.Fatal("MISTRAL_API_KEY is required when OCR_PROVIDER=mistral")
	}

	// Phase-specific models (customizable via .env)
	OCR_MODEL_NAME = getEnv("OCR_MODEL_NAME", "gemini-2.5-flash-lite")
	TEMPLATE_MODEL_NAME = getEnv("TEMPLATE_MODEL_NAME", "gemini-2.5-flash-lite")
	TEMPLATE_ACCOUNTING_MODEL_NAME = getEnv("TEMPLATE_ACCOUNTING_MODEL_NAME", "gemini-2.5-flash-lite")
	ACCOUNTING_MODEL_NAME = getEnv("ACCOUNTING_MODEL_NAME", "gemini-2.5-flash")

	// Pricing is hardcoded based on official Gemini API rates
	// No need to configure in .env - automatically matches model selection

	// Exchange rate (customizable via .env)
	USD_TO_THB = getEnvFloat("USD_TO_THB", 36.0)

	PORT = getEnv("PORT", "8080")
	UPLOAD_DIR = getEnv("UPLOAD_DIR", "uploads")
	ALLOWED_ORIGINS = getEnv("ALLOWED_ORIGINS", "*")

	// MongoDB Configuration
	MONGO_URI = getEnv("MONGO_URI", "mongodb://localhost:27017")
	MONGO_DB_NAME = getEnv("MONGO_DB_NAME", "your_database_name")

	// Image Processing
	ENABLE_IMAGE_PREPROCESSING = getEnvBool("ENABLE_IMAGE_PREPROCESSING", true)
	MAX_IMAGE_DIMENSION = getEnvInt("MAX_IMAGE_DIMENSION", 2000)

	// Performance Optimization
	ENABLE_QUICK_OCR = getEnvBool("ENABLE_QUICK_OCR", false)      // Default: skip quick OCR to save time
	QUICK_OCR_TIMEOUT = getEnvInt("QUICK_OCR_TIMEOUT", 30)        // 30 seconds
	FULL_OCR_TIMEOUT = getEnvInt("FULL_OCR_TIMEOUT", 45)          // Reduced from 60 to 45
	ACCOUNTING_TIMEOUT = getEnvInt("ACCOUNTING_TIMEOUT", 60)      // 60 seconds
	PARALLEL_PROCESSING = getEnvBool("PARALLEL_PROCESSING", true) // Enable parallel processing
	USE_SMALLER_MODEL = getEnvBool("USE_SMALLER_MODEL", false)    // Use flash-8b for speed

	log.Println("✓ Configuration loaded successfully")
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		}
	}
	return defaultValue
}
