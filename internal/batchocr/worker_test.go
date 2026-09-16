package batchocr

import (
	"os"
	"testing"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/joho/godotenv"
)

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
