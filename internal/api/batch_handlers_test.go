package api

import (
	"os"
	"testing"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/joho/godotenv"
)

// TestRunAnalyzeForBatch_RealDocument calls the live Gemini API against one
// of the two eligible fixture documents (plan-clever-lemon.md's second
// fixture, taskguid=3J7SexZlS0fhUV5zZDnrBdW2qUF), which costs real money —
// this test only runs when RUN_LIVE_AI_TESTS=1 is set explicitly, unlike the
// other tests in this codebase that skip only on a missing MONGO_URI.
//
// This exercises the whole point of TODO-5: that RunAnalyzeForBatch's result
// is usable exactly like the interactive analyze-receipt response, with all
// the same top-level keys the frontend and internal/batchocr's worker (cost
// extraction) both depend on.
func TestRunAnalyzeForBatch_RealDocument(t *testing.T) {
	if os.Getenv("RUN_LIVE_AI_TESTS") != "1" {
		t.Skip("RUN_LIVE_AI_TESTS not set to 1 — skipping (this test spends real AI credits)")
	}

	godotenv.Load("../../.env")
	configs.LoadConfig()

	// runAnalyzePipeline downloads into configs.UPLOAD_DIR, which main.go
	// normally creates at process startup before the router is even built.
	// A test binary skips that, so replicate it here — otherwise every
	// download in this test fails with a misleading "no such file or
	// directory" that has nothing to do with the pipeline itself.
	if err := os.MkdirAll(configs.UPLOAD_DIR, 0755); err != nil {
		t.Fatalf("failed to create UPLOAD_DIR %q: %v", configs.UPLOAD_DIR, err)
	}

	if err := storage.InitMongoDB(); err != nil {
		t.Fatalf("mongo connect failed: %v", err)
	}
	t.Cleanup(storage.CloseMongoDB)

	const shopID = "36xq3C3RKkSrkcCJNj6lnjfBl6Z"

	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		t.Fatalf("GetOrLoadMasterData failed: %v", err)
	}

	documentTemplates := masterCache.DocumentTemplates

	imgRefs := []ImageReference{
		{
			DocumentImageGUID: "3J7Sj9o5oqdqK1vXlJfVEvgYvyG",
			ImageURI:          "https://dedeposblosstorage.blob.core.windows.net/dedeposdevcontainer/36xq3C3RKkSrkcCJNj6lnjfBl6Z/3J7Sgo208EEwxkOCAnIo1BgFtjo.png",
		},
	}

	result, jobErr := RunAnalyzeForBatch(shopID, "gemini", imgRefs, masterCache, documentTemplates)
	if jobErr != nil {
		t.Fatalf("RunAnalyzeForBatch failed: [%s] %s", jobErr.Code, jobErr.Message)
	}

	requiredKeys := []string{
		"shopid", "status", "document_analysis", "receipt", "accounting_entry",
		"validation", "template_info", "custom_prompts", "source_images", "metadata",
	}
	for _, key := range requiredKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("expected result to have key %q, missing (got keys: %v)", key, keysOf(result))
		}
	}

	if result["status"] != "success" {
		t.Errorf("expected status=success, got %v", result["status"])
	}

	metadata, ok := AsStringMap(result["metadata"])
	if !ok {
		t.Fatalf("expected metadata to be a map, got %T", result["metadata"])
	}
	tokenUsage, ok := AsStringMap(metadata["token_usage"])
	if !ok {
		t.Fatalf("expected metadata.token_usage to be a map, got %T", metadata["token_usage"])
	}
	if _, ok := tokenUsage["cost_thb"]; !ok {
		t.Errorf("expected metadata.token_usage.cost_thb for gemini provider, got keys: %v", keysOf(tokenUsage))
	}
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
