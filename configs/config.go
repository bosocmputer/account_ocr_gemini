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

	// Template Matching Configuration
	TEMPLATE_CONFIDENCE_THRESHOLD float64 // Minimum confidence to use template-only mode (default: 95%)

	// Gemini Pricing Configuration (hardcoded based on official Gemini API pricing)
	// Reference: https://ai.google.dev/pricing (Updated: January 2026 - PAID TIER)
	// Gemini 2.5 Flash-Lite (Paid): $0.10 input, $0.40 output per 1M tokens
	// Gemini 2.5 Flash (Paid): $0.30 input, $2.50 output per 1M tokens
	// Note: Thinking tokens are billed as INPUT tokens
	OCR_INPUT_PRICE_PER_MILLION                  = 0.10 // Flash-Lite pricing (Paid tier)
	OCR_OUTPUT_PRICE_PER_MILLION                 = 0.40 // Flash-Lite pricing (Paid tier)
	TEMPLATE_INPUT_PRICE_PER_MILLION             = 0.10 // Flash-Lite pricing (Paid tier)
	TEMPLATE_OUTPUT_PRICE_PER_MILLION            = 0.40 // Flash-Lite pricing (Paid tier)
	TEMPLATE_ACCOUNTING_INPUT_PRICE_PER_MILLION  = 0.10 // Flash-Lite pricing (when template matched ≥95%)
	TEMPLATE_ACCOUNTING_OUTPUT_PRICE_PER_MILLION = 0.40 // Flash-Lite pricing (when template matched ≥95%)
	ACCOUNTING_INPUT_PRICE_PER_MILLION           = 0.30 // Flash pricing (when template not matched <95%)
	ACCOUNTING_OUTPUT_PRICE_PER_MILLION          = 2.50 // Flash pricing INCLUDING THINKING TOKENS

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

	// OCR_WORKER_COUNT: how many images within one analyze-receipt/test-template
	// request are OCR'd concurrently. Actually wired in (internal/api/handlers.go's
	// runAnalyzePipeline), unlike the performance settings above. Gemini's own
	// per-request rate is separately capped by internal/ratelimit's global token
	// bucket regardless of this value — this only controls how many images can be
	// in flight (network+inference latency) at once within a single request, not
	// how fast Gemini calls are submitted.
	OCR_WORKER_COUNT int

	// Confidence threshold settings for validation
	CONFIDENCE_HIGH_THRESHOLD   = "high"   // AI is very confident
	CONFIDENCE_MEDIUM_THRESHOLD = "medium" // AI has some uncertainty
	CONFIDENCE_LOW_THRESHOLD    = "low"    // AI is uncertain, requires review

	// Excel journal-import validation limits — this is the first pipeline in
	// this service to need file-size/row-count guards, so there was no prior
	// convention to inherit. Rejected synchronously (400, before a job is
	// created) when known from the upload itself (file size); rejected as an
	// early job.Fail when only knowable after opening the file (row count).
	EXCEL_IMPORT_MAX_ROWS         int // reject files with more data rows than this
	EXCEL_IMPORT_MAX_FILE_SIZE_MB int // reject uploads larger than this, checked from the multipart header before saving to disk
	EXCEL_IMPORT_TIMEOUT_SEC      int // pipeline deadline, checked periodically inside the row-processing loop (not just at stage boundaries — that loop is this pipeline's dominant cost)

	// Batch background OCR — lets an accountant kick off AI analysis for every
	// eligible document in a task and close the browser; a server-side worker
	// keeps going and the task page picks the run back up when reopened.
	// Throughput here is bounded by internal/ratelimit's global Gemini quota
	// (~12 calls/min for the whole process), not by these settings — raising
	// BATCH_OCR_CONCURRENCY does not make a batch finish faster, it only takes
	// more of that shared quota away from people actively using the analyze
	// endpoint interactively. Keep concurrency low and rely on the delay
	// instead.
	BATCH_OCR_ENABLED     bool // emergency kill switch for the whole feature
	BATCH_OCR_CONCURRENCY int  // documents processed in parallel per batch run — keep low, see note above
	BATCH_OCR_MAX_ITEMS   int  // hard cap on documents accepted into one batch run

	// Delay between documents within a batch, so a long-running batch doesn't
	// starve interactive users of the same shared rate-limit budget.
	BATCH_OCR_ITEM_DELAY_MS int

	// Automatic retry count per document before it's marked failed (covers
	// transient errors like a slow image download, not permanent ones).
	BATCH_OCR_MAX_ATTEMPTS int

	// Per-document timeout, matching the ~5 minute wall-clock deadline the
	// existing single-document analyze pipeline already enforces.
	BATCH_OCR_ITEM_TIMEOUT_SEC int

	// How long a batch run's lease can go without a heartbeat before another
	// instance is allowed to claim it as orphaned (e.g. after a hard crash or
	// container restart mid-run).
	BATCH_OCR_STALE_LEASE_SEC int

	// Our own MongoDB collection for batch run/item state — safe to add
	// indexes on, unlike DOCUMENT_IMAGE_GROUP_COLLECTION below.
	BATCH_OCR_COLLECTION string

	// Rough per-document cost estimate shown to the user before they confirm
	// a batch run (so a several-hundred-baht button press isn't a surprise).
	// Adjust from real logged costs over time — this is a starting guess, not
	// a computed price.
	BATCH_OCR_EST_COST_PER_DOC_THB float64

	// The MongoDB collection holding document image groups — confirmed via
	// Mongo Compass to be "documentImageGroups" (camelCase, trailing "s").
	// This collection belongs to the main API/team, not this service: we
	// only read/write specific fields on it (ocranalyzeai, via
	// internal/storage/documentimagegroup.go) and never create indexes on it
	// (it has none beyond the default _id_ index — every query against it is
	// a full collection scan by design; see internal/batchocr's package
	// comment for how the worker avoids doing that per-document).
	DOCUMENT_IMAGE_GROUP_COLLECTION string
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

	// Template Matching Configuration
	TEMPLATE_CONFIDENCE_THRESHOLD = getEnvFloat("TEMPLATE_CONFIDENCE_THRESHOLD", 95.0)

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

	// Default 3: lets a multi-image receipt overlap network+inference latency
	// across images instead of paying it fully serially, while the global
	// Gemini rate limiter (internal/ratelimit) still caps actual API request
	// rate independent of this number — raising this doesn't risk more 429s.
	OCR_WORKER_COUNT = getEnvInt("OCR_WORKER_COUNT", 3)

	// Excel journal-import validation limits
	EXCEL_IMPORT_MAX_ROWS = getEnvInt("EXCEL_IMPORT_MAX_ROWS", 20000)
	EXCEL_IMPORT_MAX_FILE_SIZE_MB = getEnvInt("EXCEL_IMPORT_MAX_FILE_SIZE_MB", 15)
	EXCEL_IMPORT_TIMEOUT_SEC = getEnvInt("EXCEL_IMPORT_TIMEOUT_SEC", 180)

	// Batch background OCR
	BATCH_OCR_ENABLED = getEnvBool("BATCH_OCR_ENABLED", true)
	BATCH_OCR_CONCURRENCY = getEnvInt("BATCH_OCR_CONCURRENCY", 2)
	BATCH_OCR_MAX_ITEMS = getEnvInt("BATCH_OCR_MAX_ITEMS", 100)
	BATCH_OCR_ITEM_DELAY_MS = getEnvInt("BATCH_OCR_ITEM_DELAY_MS", 2000)
	BATCH_OCR_MAX_ATTEMPTS = getEnvInt("BATCH_OCR_MAX_ATTEMPTS", 2)
	BATCH_OCR_ITEM_TIMEOUT_SEC = getEnvInt("BATCH_OCR_ITEM_TIMEOUT_SEC", 300)
	BATCH_OCR_STALE_LEASE_SEC = getEnvInt("BATCH_OCR_STALE_LEASE_SEC", 900)
	BATCH_OCR_COLLECTION = getEnv("BATCH_OCR_COLLECTION", "batch_ocr_runs")
	BATCH_OCR_EST_COST_PER_DOC_THB = getEnvFloat("BATCH_OCR_EST_COST_PER_DOC_THB", 0.30)
	DOCUMENT_IMAGE_GROUP_COLLECTION = getEnv("DOCUMENT_IMAGE_GROUP_COLLECTION", "documentImageGroups")

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
