// gemini_retry.go - Retry logic and error handling for Gemini API calls

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/googleapi"
)

// RetryConfig defines retry behavior for Gemini API calls
type RetryConfig struct {
	MaxAttempts     int
	InitialDelay    time.Duration
	MaxDelay        time.Duration
	BackoffMultiple float64
}

// DefaultRetryConfig provides sensible defaults for retry behavior
var DefaultRetryConfig = RetryConfig{
	MaxAttempts:     3,
	InitialDelay:    1 * time.Second,
	MaxDelay:        8 * time.Second,
	BackoffMultiple: 2.0,
}

// GeminiError represents a categorized Gemini API error
type GeminiError struct {
	OriginalError error
	Category      string
	StatusCode    int
	Message       string
	Retryable     bool
}

func (e *GeminiError) Error() string {
	return fmt.Sprintf("[%s] %s (status: %d, retryable: %v)", e.Category, e.Message, e.StatusCode, e.Retryable)
}

// categorizeGeminiError analyzes error and determines retry strategy
func categorizeGeminiError(err error) *GeminiError {
	if err == nil {
		return nil
	}

	geminiErr := &GeminiError{
		OriginalError: err,
		Category:      "unknown",
		Message:       err.Error(),
		Retryable:     false,
	}

	// Check if it's a Google API error
	if apiErr, ok := err.(*googleapi.Error); ok {
		geminiErr.StatusCode = apiErr.Code

		switch apiErr.Code {
		case 400:
			geminiErr.Category = "bad_request"
			geminiErr.Message = "Invalid request format or parameters"
			geminiErr.Retryable = false

		case 401:
			geminiErr.Category = "unauthorized"
			geminiErr.Message = "Invalid API key or authentication failed"
			geminiErr.Retryable = false

		case 403:
			geminiErr.Category = "forbidden"
			geminiErr.Message = "API key lacks required permissions"
			geminiErr.Retryable = false

		case 404:
			geminiErr.Category = "not_found"
			geminiErr.Message = "Model not found or invalid endpoint"
			geminiErr.Retryable = false

		case 413:
			geminiErr.Category = "payload_too_large"
			geminiErr.Message = "Request size exceeds limit (reduce image size)"
			geminiErr.Retryable = false

		case 429:
			geminiErr.Category = "rate_limit"
			geminiErr.Message = "Rate limit exceeded - too many requests"
			geminiErr.Retryable = true

		case 500, 502, 503, 504:
			geminiErr.Category = "server_error"
			geminiErr.Message = fmt.Sprintf("Gemini server error (%d)", apiErr.Code)
			geminiErr.Retryable = true

		default:
			geminiErr.Category = "unknown_api_error"
			geminiErr.Message = fmt.Sprintf("API error: %s", apiErr.Message)
			geminiErr.Retryable = apiErr.Code >= 500
		}

		return geminiErr
	}

	// Check for context errors
	if err == context.DeadlineExceeded {
		geminiErr.Category = "timeout"
		geminiErr.Message = "Request timeout - processing took too long"
		geminiErr.Retryable = true
		return geminiErr
	}

	if err == context.Canceled {
		geminiErr.Category = "canceled"
		geminiErr.Message = "Request was canceled"
		geminiErr.Retryable = false
		return geminiErr
	}

	// Check error message for common patterns
	errMsg := strings.ToLower(err.Error())

	if strings.Contains(errMsg, "quota") || strings.Contains(errMsg, "limit") {
		geminiErr.Category = "quota_exceeded"
		geminiErr.Message = "API quota exceeded - daily or monthly limit reached"
		geminiErr.Retryable = false
		return geminiErr
	}

	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
		geminiErr.Category = "timeout"
		geminiErr.Message = "Request timeout"
		geminiErr.Retryable = true
		return geminiErr
	}

	if strings.Contains(errMsg, "connection") || strings.Contains(errMsg, "network") {
		geminiErr.Category = "network_error"
		geminiErr.Message = "Network connection error"
		geminiErr.Retryable = true
		return geminiErr
	}

	// Default: unknown error, not retryable
	geminiErr.Category = "unknown"
	geminiErr.Retryable = false
	return geminiErr
}

// callGeminiWithRetry executes a Gemini API call with retry logic
func callGeminiWithRetry(
	ctx context.Context,
	model *genai.GenerativeModel,
	prompt genai.Part,
	image genai.Part,
	reqCtx *RequestContext,
	config RetryConfig,
) (*genai.GenerateContentResponse, error) {

	var lastGeminiErr *GeminiError

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Log attempt
		if attempt > 1 {
			reqCtx.LogInfo("Retry attempt %d/%d", attempt, config.MaxAttempts)
		}

		// Make API call
		resp, err := model.GenerateContent(ctx, prompt, image)

		// Success!
		if err == nil {
			if attempt > 1 {
				reqCtx.LogInfo("✅ Retry succeeded on attempt %d", attempt)
			}
			return resp, nil
		}

		// Categorize error
		lastGeminiErr = categorizeGeminiError(err)

		// Log error details
		reqCtx.LogError("API call failed (attempt %d/%d): %s", attempt, config.MaxAttempts, lastGeminiErr.Error())

		// If error is not retryable, fail immediately
		if !lastGeminiErr.Retryable {
			reqCtx.LogError("Non-retryable error detected, aborting")
			return nil, lastGeminiErr
		}

		// If this was the last attempt, don't sleep
		if attempt >= config.MaxAttempts {
			break
		}

		// Calculate delay with exponential backoff
		delay := calculateBackoff(attempt, config)

		// Special case: rate limit - use longer delay
		if lastGeminiErr.Category == "rate_limit" {
			delay = delay * 2
			reqCtx.LogWarning("Rate limit hit, waiting %v before retry", delay)
		} else {
			reqCtx.LogInfo("Waiting %v before retry", delay)
		}

		// Wait before retry (respect context cancellation)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled during retry wait: %w", ctx.Err())
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	// All attempts failed
	reqCtx.LogError("❌ All %d attempts failed, last error: %s", config.MaxAttempts, lastGeminiErr.Error())
	return nil, fmt.Errorf("gemini API call failed after %d attempts: %w", config.MaxAttempts, lastGeminiErr)
}

// calculateBackoff computes exponential backoff delay
func calculateBackoff(attempt int, config RetryConfig) time.Duration {
	delay := float64(config.InitialDelay) * pow(config.BackoffMultiple, float64(attempt-1))

	// Cap at max delay
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}

	return time.Duration(delay)
}

// pow computes base^exp for floats (simple implementation)
func pow(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}

// buildUserFriendlyError converts technical error to user-friendly message
func buildUserFriendlyError(geminiErr *GeminiError) map[string]interface{} {
	errorResponse := map[string]interface{}{
		"error":    "AI processing failed",
		"category": geminiErr.Category,
		"details":  geminiErr.Message,
	}

	// Add specific guidance based on error type
	switch geminiErr.Category {
	case "rate_limit":
		errorResponse["suggestion"] = "Too many requests. Please wait a moment and try again."
		errorResponse["retry_after"] = "30-60 seconds"

	case "quota_exceeded":
		errorResponse["suggestion"] = "Daily API quota exceeded. Please contact support or try again tomorrow."
		errorResponse["action_required"] = "upgrade_plan"

	case "unauthorized":
		errorResponse["suggestion"] = "API authentication failed. Please contact system administrator."
		errorResponse["action_required"] = "check_api_key"

	case "payload_too_large":
		errorResponse["suggestion"] = "Image size is too large. Please use a smaller image (max 5MB recommended)."
		errorResponse["action_required"] = "reduce_image_size"

	case "timeout":
		errorResponse["suggestion"] = "Request took too long. Please try again with a clearer image."
		errorResponse["retry_recommended"] = true

	case "server_error":
		errorResponse["suggestion"] = "Gemini service is temporarily unavailable. Please try again in a few minutes."
		errorResponse["retry_recommended"] = true

	case "network_error":
		errorResponse["suggestion"] = "Network connection issue. Please check your internet connection and try again."
		errorResponse["retry_recommended"] = true

	default:
		errorResponse["suggestion"] = "An unexpected error occurred. Please try again or contact support."
		errorResponse["retry_recommended"] = false
	}

	return errorResponse
}
