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

	"github.com/gin-gonic/gin"
)

func main() {
	// Step 0: Load configuration from environment variables
	LoadConfig()

	// Step 0.5: Set production mode
	if ginMode := os.Getenv("GIN_MODE"); ginMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Step 1: Create the UPLOAD_DIR folder if it doesn't exist
	if err := os.MkdirAll(UPLOAD_DIR, 0755); err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	} // Step 1.5: Initialize MongoDB connection
	if err := InitMongoDB(); err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer CloseMongoDB()

	// Step 2: Initialize the Gin router
	router := gin.Default()

	// Add CORS middleware - configure allowed origins for production
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", ALLOWED_ORIGINS)
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"service": "go-receipt-parser",
			"version": "1.0.0",
		})
	})

	// Step 3: Define the API route
	router.POST("/api/v1/analyze-receipt", analyzeReceiptHandler)

	// Step 4: Setup HTTP server with timeouts
	srv := &http.Server{
		Addr:           ":" + PORT,
		Handler:        router,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   3 * time.Minute, // Allow up to 3 minutes for long-running requests
		MaxHeaderBytes: 1 << 20,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting server on :%s", PORT)
		log.Println("API Endpoint: POST /api/v1/analyze-receipt")

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

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
