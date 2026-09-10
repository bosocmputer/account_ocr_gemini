package batchocr

import (
	"os"
	"testing"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func TestExtractCostTHB_GeminiShape(t *testing.T) {
	result := map[string]interface{}{
		"metadata": map[string]interface{}{
			"token_usage": map[string]interface{}{
				"cost_thb": "฿12.50",
			},
		},
	}
	cost, ok := extractCostTHB(result)
	if !ok {
		t.Fatal("expected extractCostTHB to succeed for gemini shape")
	}
	if cost != 12.50 {
		t.Errorf("expected cost 12.50, got %v", cost)
	}
}

func TestExtractCostTHB_MistralShape(t *testing.T) {
	result := map[string]interface{}{
		"metadata": map[string]interface{}{
			"token_usage": map[string]interface{}{
				"ocr_usage":     map[string]interface{}{"cost_thb": "฿1.00"},
				"ai_processing": map[string]interface{}{"cost_thb": "฿2.00"},
				"total":         map[string]interface{}{"cost_thb": "฿3.00", "cost_usd": "$0.09"},
			},
		},
	}
	cost, ok := extractCostTHB(result)
	if !ok {
		t.Fatal("expected extractCostTHB to succeed for mistral shape")
	}
	if cost != 3.00 {
		t.Errorf("expected cost 3.00 (from total.cost_thb, not ocr_usage or ai_processing), got %v", cost)
	}
}

func TestExtractCostTHB_HandlesGinHNesting(t *testing.T) {
	// runAnalyzePipeline actually builds its response with nested gin.H
	// (confirmed: internal/api/handlers.go builds metadata["token_usage"]
	// as gin.H, and receiptData/several other fields are gin.H too) — not
	// plain map[string]interface{} throughout. gin.H is a distinct named
	// type (`type H map[string]any`), so a type assertion against the
	// unnamed map type fails for it even though the representation is
	// identical. This is the exact shape that broke the naive type
	// assertion in RunAnalyzeForBatch's own result unwrapping (see
	// TestRunAnalyzeForBatch_RealDocument in internal/api) — extractCostTHB
	// must handle it via AsStringMap, not a direct .(map[string]interface{}).
	result := map[string]interface{}{
		"metadata": gin.H{
			"token_usage": gin.H{
				"cost_thb": "฿0.12",
			},
		},
	}
	cost, ok := extractCostTHB(result)
	if !ok {
		t.Fatal("expected extractCostTHB to succeed when metadata/token_usage are gin.H")
	}
	if cost != 0.12 {
		t.Errorf("expected cost 0.12, got %v", cost)
	}
}

func TestExtractCostTHB_MissingMetadata(t *testing.T) {
	if _, ok := extractCostTHB(map[string]interface{}{}); ok {
		t.Error("expected extractCostTHB to fail gracefully when metadata is missing")
	}
}

func TestExtractCostTHB_UnparsableCost(t *testing.T) {
	result := map[string]interface{}{
		"metadata": map[string]interface{}{
			"token_usage": map[string]interface{}{
				"cost_thb": "not a number",
			},
		},
	}
	if _, ok := extractCostTHB(result); ok {
		t.Error("expected extractCostTHB to fail gracefully on unparsable cost string, not panic or return a bogus value")
	}
}

func TestExtractCostTHB_MissingBahtSign(t *testing.T) {
	// Defensive: even if the ฿ prefix were ever missing, this should still
	// parse rather than fail, since TrimPrefix is a no-op when the prefix
	// isn't present.
	result := map[string]interface{}{
		"metadata": map[string]interface{}{
			"token_usage": map[string]interface{}{
				"cost_thb": "7.25",
			},
		},
	}
	cost, ok := extractCostTHB(result)
	if !ok || cost != 7.25 {
		t.Errorf("expected cost 7.25 with ok=true, got cost=%v ok=%v", cost, ok)
	}
}

func TestCheckTaskIsOpen_UnconfirmedCollectionIsInconclusiveNotClosed(t *testing.T) {
	// checkTaskIsOpen must never report a task as closed just because the
	// lookup itself failed (wrong collection, network hiccup, field
	// renamed) — that would auto-cancel batches for a reason that has
	// nothing to do with the task actually being closed. Needs a live Mongo
	// connection to be a meaningful check (an uninitialized storage.GetMongoDB()
	// short-circuits to the same "inconclusive" answer via a different path).
	godotenv.Load("../../.env")
	if os.Getenv("MONGO_URI") == "" {
		t.Skip("MONGO_URI not set — skipping live-DB integration test")
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")
	if err := storage.InitMongoDB(); err != nil {
		t.Fatalf("mongo connect failed: %v", err)
	}
	t.Cleanup(storage.CloseMongoDB)

	isOpen, checked, err := checkTaskIsOpen("nonexistent-shop-batchocr-test", "nonexistent-task-batchocr-test")
	if err != nil {
		t.Fatalf("expected no error for a lookup miss, got %v", err)
	}
	if checked {
		t.Error("expected checked=false for a task that doesn't exist (inconclusive, not confirmed open)")
	}
	if !isOpen {
		t.Error("expected isOpen=true (fail open) when the check is inconclusive")
	}
}

func TestCheckTaskIsOpen_RealClosedAndOpenTasks(t *testing.T) {
	godotenv.Load("../../.env")
	if os.Getenv("MONGO_URI") == "" {
		t.Skip("MONGO_URI not set — skipping live-DB integration test")
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")
	if err := storage.InitMongoDB(); err != nil {
		t.Fatalf("mongo connect failed: %v", err)
	}
	t.Cleanup(storage.CloseMongoDB)

	const shopID = "36xq3C3RKkSrkcCJNj6lnjfBl6Z"

	// Confirmed via direct query against the "tasks" collection: status 4
	// really does exist for this shop and really does mean closed (matches
	// isJobClosed in JournalFromImageDetail.vue).
	isOpen, checked, err := checkTaskIsOpen(shopID, "3896cJKfzaLjoyBxz8PUg5bouG7")
	if err != nil {
		t.Fatalf("checkTaskIsOpen failed: %v", err)
	}
	if !checked {
		t.Fatal("expected checked=true for a task known to exist")
	}
	if isOpen {
		t.Error("expected isOpen=false for a real task with status=4 (closed)")
	}

	// The batch-OCR fixture task itself — status 6 ("ห้ามอนุมัติ", not
	// closed) — must be reported as open, not confused with 4.
	isOpen, checked, err = checkTaskIsOpen(shopID, "3J7SexZlS0fhUV5zZDnrBdW2qUF")
	if err != nil {
		t.Fatalf("checkTaskIsOpen failed: %v", err)
	}
	if !checked {
		t.Fatal("expected checked=true for the fixture task")
	}
	if !isOpen {
		t.Error("expected isOpen=true for a task with status=6 (not closed, even though not fully open either)")
	}
}
