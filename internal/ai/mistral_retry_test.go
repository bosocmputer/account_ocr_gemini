package ai

import (
	"errors"
	"testing"
)

func TestCategorizeMistralError_StatusCodes(t *testing.T) {
	cases := []struct {
		name          string
		statusCode    int
		wantRetryable bool
		wantCategory  string
	}{
		{"400 bad request must not retry", 400, false, "bad_request"},
		{"401 unauthorized must not retry", 401, false, "unauthorized"},
		{"403 forbidden must not retry", 403, false, "forbidden"},
		{"404 not found must not retry", 404, false, "not_found"},
		{"413 payload too large must not retry", 413, false, "payload_too_large"},
		{"429 rate limit must retry", 429, true, "rate_limit"},
		{"500 server error must retry", 500, true, "server_error"},
		{"502 server error must retry", 502, true, "server_error"},
		{"503 server error must retry", 503, true, "server_error"},
		{"504 server error must retry", 504, true, "server_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &mistralAPIError{StatusCode: tc.statusCode, Body: "test"}
			result := categorizeMistralError(err)

			if result.Retryable != tc.wantRetryable {
				t.Errorf("status %d: expected Retryable=%v, got %v", tc.statusCode, tc.wantRetryable, result.Retryable)
			}
			if result.Category != tc.wantCategory {
				t.Errorf("status %d: expected Category=%q, got %q", tc.statusCode, tc.wantCategory, result.Category)
			}
			if result.StatusCode != tc.statusCode {
				t.Errorf("expected StatusCode=%d preserved, got %d", tc.statusCode, result.StatusCode)
			}
		})
	}
}

func TestCategorizeMistralError_UnknownStatusCode(t *testing.T) {
	// An unrecognized 5xx should still be treated as retryable (server-side
	// problem), an unrecognized 4xx should not (client-side problem that
	// won't change on retry).
	serverErr := categorizeMistralError(&mistralAPIError{StatusCode: 599, Body: "test"})
	if !serverErr.Retryable {
		t.Error("expected unrecognized 5xx to be retryable")
	}

	clientErr := categorizeMistralError(&mistralAPIError{StatusCode: 418, Body: "test"})
	if clientErr.Retryable {
		t.Error("expected unrecognized 4xx to NOT be retryable")
	}
}

func TestCategorizeMistralError_MessageFallback(t *testing.T) {
	// Non-mistralAPIError errors (network errors, timeouts) are categorized
	// by matching the error message text, same fallback path
	// categorizeGeminiError uses for non-googleapi.Error cases.
	cases := []struct {
		name          string
		err           error
		wantRetryable bool
		wantCategory  string
	}{
		{"connection refused is retryable", errors.New("dial tcp: connection refused"), true, "network_error"},
		{"context deadline exceeded is retryable", errors.New("context deadline exceeded"), true, "timeout"},
		{"429 mentioned in message is retryable", errors.New("got 429 from upstream"), true, "rate_limit"},
		{"unrecognized message is not retryable", errors.New("something completely unexpected"), false, "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := categorizeMistralError(tc.err)
			if result.Retryable != tc.wantRetryable {
				t.Errorf("expected Retryable=%v, got %v (category=%s)", tc.wantRetryable, result.Retryable, result.Category)
			}
			if result.Category != tc.wantCategory {
				t.Errorf("expected Category=%q, got %q", tc.wantCategory, result.Category)
			}
		})
	}
}

func TestCategorizeMistralError_NilReturnsNil(t *testing.T) {
	if categorizeMistralError(nil) != nil {
		t.Error("expected nil error to categorize as nil")
	}
}
