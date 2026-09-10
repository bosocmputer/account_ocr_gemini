// mistral_retry.go - Retry logic and error handling for Mistral OCR API calls.
//
// Mirrors gemini_retry.go's structure and shares its RetryConfig/
// DefaultRetryConfig — mistral.go previously had no retry and no rate
// limiting at all. That was fine while only one interactive user could hit
// it at a time (a failed call just meant clicking the button again), but the
// batch OCR feature can drive dozens of Mistral calls back to back, and a
// single transient 429/5xx partway through used to fail the entire batch
// run with nothing to recover it.

package ai

import (
	"strconv"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/ratelimit"
)

// MistralError represents a categorized Mistral OCR API error.
type MistralError struct {
	OriginalError error
	Category      string
	StatusCode    int
	Message       string
	Retryable     bool
}

func (e *MistralError) Error() string {
	return e.Message
}

// mistralAPIError is implemented by callMistralOCRAPI's error return when it
// wraps an HTTP status code — categorizeMistralError type-asserts against
// this instead of re-parsing the error string, mirroring how
// categorizeGeminiError type-asserts against *googleapi.Error.
type mistralAPIError struct {
	StatusCode int
	Body       string
}

func (e *mistralAPIError) Error() string {
	return "mistral OCR API error (" + strconv.Itoa(e.StatusCode) + "): " + e.Body
}

// categorizeMistralError analyzes an error from callMistralOCRAPI and
// determines whether it's worth retrying. Status-code handling mirrors
// categorizeGeminiError's switch (internal/ai/gemini_retry.go) — 401/403/400
// mean the request or credentials are wrong and will fail identically on
// every retry, so those must not be retried; 429 and 5xx are transient.
func categorizeMistralError(err error) *MistralError {
	if err == nil {
		return nil
	}

	mistralErr := &MistralError{
		OriginalError: err,
		Category:      "unknown",
		Message:       err.Error(),
		Retryable:     false,
	}

	if apiErr, ok := err.(*mistralAPIError); ok {
		mistralErr.StatusCode = apiErr.StatusCode

		switch apiErr.StatusCode {
		case 400:
			mistralErr.Category = "bad_request"
			mistralErr.Message = "Invalid request format or parameters"
			mistralErr.Retryable = false

		case 401:
			mistralErr.Category = "unauthorized"
			mistralErr.Message = "Invalid API key or authentication failed"
			mistralErr.Retryable = false

		case 403:
			mistralErr.Category = "forbidden"
			mistralErr.Message = "API key lacks required permissions"
			mistralErr.Retryable = false

		case 404:
			mistralErr.Category = "not_found"
			mistralErr.Message = "Model not found or invalid endpoint"
			mistralErr.Retryable = false

		case 413:
			mistralErr.Category = "payload_too_large"
			mistralErr.Message = "Request size exceeds limit (reduce image size)"
			mistralErr.Retryable = false

		case 429:
			mistralErr.Category = "rate_limit"
			mistralErr.Message = "Rate limit exceeded - too many requests (wait 10-60 seconds)"
			mistralErr.Retryable = true

		case 500, 502, 503, 504:
			mistralErr.Category = "server_error"
			mistralErr.Message = "Mistral server error (" + strconv.Itoa(apiErr.StatusCode) + ")"
			mistralErr.Retryable = true

		default:
			mistralErr.Category = "unknown_api_error"
			mistralErr.Message = "API error: " + apiErr.Body
			mistralErr.Retryable = apiErr.StatusCode >= 500
		}

		return mistralErr
	}

	// Not a categorized HTTP status — fall back to matching the error
	// message text, same approach as categorizeGeminiError for non-
	// googleapi.Error cases (network errors, context timeouts, etc. surface
	// this way from net/http's client.Do).
	errMsg := strings.ToLower(err.Error())

	if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "resource exhausted") {
		mistralErr.Category = "rate_limit"
		mistralErr.Message = "Rate limit exceeded - too many requests (wait 10-60 seconds)"
		mistralErr.Retryable = true
		return mistralErr
	}

	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
		mistralErr.Category = "timeout"
		mistralErr.Message = "Request timeout"
		mistralErr.Retryable = true
		return mistralErr
	}

	if strings.Contains(errMsg, "connection") || strings.Contains(errMsg, "network") ||
		strings.Contains(errMsg, "eof") || strings.Contains(errMsg, "reset") {
		mistralErr.Category = "network_error"
		mistralErr.Message = "Network connection error"
		mistralErr.Retryable = true
		return mistralErr
	}

	// Default: unknown error, not retryable — matches categorizeGeminiError's
	// conservative default (an error we don't recognize might not be
	// transient, so don't spend more attempts assuming it is).
	mistralErr.Category = "unknown"
	mistralErr.Retryable = false
	return mistralErr
}

// callMistralOCRAPIWithRetry wraps callMistralOCRAPI with the same
// rate-limit-then-call-then-backoff loop as callGeminiWithRetry
// (gemini_retry.go). It deliberately shares ratelimit.WaitForRateLimit's
// global token bucket with Gemini rather than using a separate one: the
// real bottleneck for this service is AI-provider throughput regardless of
// which provider is in use, and one shared limiter is simpler and safer
// than reasoning about two independent ones. If Mistral ever needs its own
// bucket (e.g. because its rate limit differs meaningfully from Gemini's),
// that's a separate follow-up, not something to improvise here.
func (m *MistralProvider) callMistralOCRAPIWithRetry(request mistralOCRRequest, reqCtx *common.RequestContext, config RetryConfig) (*mistralOCRResponse, error) {
	var lastErr *MistralError

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		ratelimit.WaitForRateLimit()

		if attempt > 1 {
			reqCtx.LogInfo("Retry attempt %d/%d (Mistral OCR)", attempt, config.MaxAttempts)
		}

		resp, err := m.callMistralOCRAPI(request)
		if err == nil {
			if attempt > 1 {
				reqCtx.LogInfo("✅ Retry succeeded on attempt %d", attempt)
			}
			return resp, nil
		}

		lastErr = categorizeMistralError(err)
		reqCtx.LogError("Mistral OCR API call failed (attempt %d/%d): %s", attempt, config.MaxAttempts, lastErr.Message)

		if !lastErr.Retryable {
			reqCtx.LogError("Non-retryable error detected, aborting")
			return nil, lastErr
		}

		if attempt >= config.MaxAttempts {
			break
		}

		delay := calculateBackoff(attempt, config)
		if lastErr.Category == "rate_limit" {
			delay = 30 * time.Second * time.Duration(attempt)
			if delay > 90*time.Second {
				delay = 90 * time.Second
			}
			reqCtx.LogWarning("⚠️  Rate limit hit (429), waiting %v before retry (attempt %d/%d)", delay, attempt, config.MaxAttempts)
		} else {
			reqCtx.LogInfo("Waiting %v before retry", delay)
		}

		time.Sleep(delay)
	}

	reqCtx.LogError("❌ All %d attempts failed, last error: %s", config.MaxAttempts, lastErr.Message)
	return nil, lastErr
}
