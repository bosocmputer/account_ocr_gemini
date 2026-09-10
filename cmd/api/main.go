// main.go - The entry point and router setup.

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/api"
	"github.com/bosocmputer/account_ocr_gemini/internal/batchocr"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
)

func main() {
	// Step 0: Load configuration from environment variables
	configs.LoadConfig()

	// Step 0.5: Set production mode
	if ginMode := os.Getenv("GIN_MODE"); ginMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Step 1: Create the UPLOAD_DIR folder if it doesn't exist
	if err := os.MkdirAll(configs.UPLOAD_DIR, 0755); err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	} // Step 1.5: Initialize MongoDB connection
	if err := storage.InitMongoDB(); err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer storage.CloseMongoDB()

	// Batch background OCR: index setup + resuming any runs left mid-flight
	// by a previous process (crash, redeploy). Neither may log.Fatal — a
	// problem in this subsystem must not prevent the service from serving
	// interactive requests, which don't depend on it at all.
	if err := batchocr.EnsureIndexes(); err != nil {
		log.Printf("⚠️ batch OCR index setup failed: %v", err)
	}
	batchocr.StartResume()

	// Step 2: Initialize the Gin router
	router := gin.Default()

	// Add CORS middleware - configure allowed origins for production
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", configs.ALLOWED_ORIGINS)
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		// X-Job-Token: the async job-polling endpoints (GetJobStatusHandler)
		// require this header to prove ownership of a job — without it in
		// the preflight allow-list, browsers silently block the polling
		// request before it's ever sent (curl doesn't enforce CORS at all,
		// so this gap only shows up as a browser-side "network error").
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Job-Token")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Root endpoint for SSL verification
	router.GET("/", func(c *gin.Context) {
		c.String(200, "ok")
	})

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"service": "go-receipt-parser",
			"version": "1.0.0",
		})
	})

	// Step 3: Define the API routes, grouped under /billscan so this service
	// can share a Caddy box with other apps via a plain reverse_proxy (no
	// path stripping needed on the Caddy side) — routes are identical
	// whether hit directly on :8080 locally or through Caddy in production.
	//
	// Both analyze-receipt and test-template are async: each submits a job
	// and returns immediately (202 + job id/token), and the client polls the
	// shared jobs/:id endpoint for progress and the final result — see
	// internal/jobs and Submit*Handler's doc comments for why (the pipeline
	// can legitimately run 1-3+ minutes, which no longer needs to hold an
	// HTTP request open the whole time; test-template is the tool used to
	// iteratively tune a template's prompt, so real progress feedback there
	// matters just as much as the main analyze flow).
	billscan := router.Group("/billscan")
	billscan.POST("/api/v1/analyze-receipt", api.SubmitAnalyzeReceiptHandler)
	billscan.POST("/api/v1/test-template", api.SubmitTestTemplateHandler)
	billscan.GET("/api/v1/jobs/:id", api.GetJobStatusHandler)
	// Synchronous (not job-based) — used by the bcaccount Excel importer to
	// bulk-check duplicate document numbers before a save. See
	// CheckDocnosExistHandler's doc comment for why this lives here rather
	// than on the main accounting API.
	billscan.POST("/api/v1/check-docnos-exist", api.CheckDocnosExistHandler)
	// Async job, same submit-then-poll pattern as analyze-receipt/
	// test-template above (reuses the same GET /api/v1/jobs/:id endpoint).
	// Moves the bcaccount Excel importer's full parse+validate pipeline
	// (previously a synchronous client-side JS loop over the whole file)
	// server-side, so validation logic is enforced identically for every
	// user and large files (5,000-20,000+ rows) no longer freeze the
	// browser tab. See SubmitImportValidationHandler's doc comment.
	billscan.POST("/api/v1/import-journal/validate", api.SubmitImportValidationHandler)
	// Async job, same submit-then-poll pattern as the generic import-journal
	// validator above — dedicated pipeline for the fixed-format SML ERP
	// "รายงานข้อมูลรายวัน" (+ optional "รายงานภาษีขาย") sales-journal export.
	// See SubmitSmlSalesImportValidationHandler's doc comment.
	billscan.POST("/api/v1/import-journal/validate-sml-sales", api.SubmitSmlSalesImportValidationHandler)

	// Batch background OCR — lets an accountant kick off AI analysis for
	// every eligible document in a task and close the browser; a
	// server-side worker keeps going and these endpoints let the task page
	// pick the run back up on reopening. See internal/batchocr and
	// plan-clever-lemon.md for the full design.
	billscan.GET("/api/v1/batch-ocr/preview", batchocr.GetBatchOcrPreviewHandler)
	billscan.POST("/api/v1/batch-ocr", batchocr.SubmitBatchOcrHandler)
	billscan.GET("/api/v1/batch-ocr/active", batchocr.GetActiveBatchOcrHandler)
	billscan.GET("/api/v1/batch-ocr/:batchId", batchocr.GetBatchOcrStatusHandler)
	billscan.POST("/api/v1/batch-ocr/:batchId/cancel", batchocr.CancelBatchOcrHandler)
	billscan.POST("/api/v1/batch-ocr/:batchId/retry", batchocr.RetryBatchOcrFailedHandler)

	// Step 4: Setup HTTP server with timeouts.
	// WriteTimeout no longer needs to cover the AI pipeline's own 5-minute
	// budget (internal/api/handlers.go's runAnalyzePipeline) — the longest
	// live HTTP request now is just the initial submit or a status poll,
	// both fast. This also fixes a pre-existing mismatch where WriteTimeout
	// (3m) was shorter than the pipeline's own timeout (5m), which meant the
	// server could kill the connection before the pipeline's own graceful
	// timeout response ever had a chance to be written.
	srv := &http.Server{
		Addr:           ":" + configs.PORT,
		Handler:        router,
		ReadTimeout:    30 * time.Second, // was 3s — too tight for test-template's multipart file upload
		WriteTimeout:   30 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting server on :%s", configs.PORT)
		log.Println("API Endpoints:")
		log.Println("  POST /billscan/api/v1/analyze-receipt (async — returns a job id)")
		log.Println("  POST /billscan/api/v1/test-template (async — returns a job id)")
		log.Println("  GET  /billscan/api/v1/jobs/:id (poll progress/result)")
		log.Println("  POST /billscan/api/v1/import-journal/validate-sml-sales (async — returns a job id)")
		log.Println("  GET  /billscan/api/v1/batch-ocr/preview (eligible/skipped counts, no run created)")
		log.Println("  POST /billscan/api/v1/batch-ocr (starts a batch run — returns a batchid)")
		log.Println("  GET  /billscan/api/v1/batch-ocr/active (is there an active run for this task?)")
		log.Println("  GET  /billscan/api/v1/batch-ocr/:batchId (poll a run's progress)")
		log.Println("  POST /billscan/api/v1/batch-ocr/:batchId/cancel")
		log.Println("  POST /billscan/api/v1/batch-ocr/:batchId/retry")

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop the current batch run from picking up new items and release its
	// lease so another instance can resume it immediately — does not wait
	// for whatever document is currently mid-analysis (see Drain's doc
	// comment for why that can't be interrupted anyway).
	batchocr.Drain(ctx)

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
