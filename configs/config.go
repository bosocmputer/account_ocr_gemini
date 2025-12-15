// config.go - Configuration loaded from environment variables

package configs

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

var (
	// Gemini AI Configuration
	GEMINI_API_KEY string
	MODEL_NAME     string

	// Gemini Pricing Configuration (per 1M tokens in USD)
	GEMINI_INPUT_PRICE_PER_MILLION  float64
	GEMINI_OUTPUT_PRICE_PER_MILLION float64
	USD_TO_THB                      float64

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

	// Required: Gemini API Key
	GEMINI_API_KEY = getEnv("GEMINI_API_KEY", "")
	if GEMINI_API_KEY == "" {
		log.Fatal("GEMINI_API_KEY environment variable is required")
	}

	// Optional with defaults
	MODEL_NAME = getEnv("MODEL_NAME", "gemini-2.5-flash")

	// Gemini Pricing (default to Flash-Lite pricing)
	GEMINI_INPUT_PRICE_PER_MILLION = getEnvFloat("GEMINI_INPUT_PRICE_PER_MILLION", 0.10)
	GEMINI_OUTPUT_PRICE_PER_MILLION = getEnvFloat("GEMINI_OUTPUT_PRICE_PER_MILLION", 0.40)
	USD_TO_THB = getEnvFloat("USD_TO_THB", 36.0)

	PORT = getEnv("PORT", "8080")
	UPLOAD_DIR = getEnv("UPLOAD_DIR", "uploads")
	ALLOWED_ORIGINS = getEnv("ALLOWED_ORIGINS", "*")

	// MongoDB Configuration
	MONGO_URI = getEnv("MONGO_URI", "mongodb://103.13.30.32:27017")
	MONGO_DB_NAME = getEnv("MONGO_DB_NAME", "smldevdb")

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
