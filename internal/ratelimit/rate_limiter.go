// rate_limiter.go - Rate limiting to prevent hitting Gemini API limits

package ratelimit

import (
	"sync"
	"time"
)

// RateLimiter implements a simple token bucket rate limiter
type RateLimiter struct {
	tokens         int
	maxTokens      int
	refillRate     time.Duration
	lastRefillTime time.Time
	mu             sync.Mutex
}

// NewRateLimiter creates a new rate limiter
// maxTokens: maximum number of concurrent requests
// refillRate: time between token refills
func NewRateLimiter(maxTokens int, refillRate time.Duration) *RateLimiter {
	return &RateLimiter{
		tokens:         maxTokens,
		maxTokens:      maxTokens,
		refillRate:     refillRate,
		lastRefillTime: time.Now(),
	}
}

// Wait blocks until a token is available
func (rl *RateLimiter) Wait() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Refill tokens based on time elapsed
	now := time.Now()
	elapsed := now.Sub(rl.lastRefillTime)
	tokensToAdd := int(elapsed / rl.refillRate)

	if tokensToAdd > 0 {
		rl.tokens += tokensToAdd
		if rl.tokens > rl.maxTokens {
			rl.tokens = rl.maxTokens
		}
		rl.lastRefillTime = now
	}

	// Wait until we have a token
	for rl.tokens <= 0 {
		rl.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		rl.mu.Lock()

		// Refill again after waiting
		now = time.Now()
		elapsed = now.Sub(rl.lastRefillTime)
		tokensToAdd = int(elapsed / rl.refillRate)

		if tokensToAdd > 0 {
			rl.tokens += tokensToAdd
			if rl.tokens > rl.maxTokens {
				rl.tokens = rl.maxTokens
			}
			rl.lastRefillTime = now
		}
	}

	// Consume one token
	rl.tokens--
}

// Global rate limiter for Gemini API
// gemini-2.0-flash-lite: 15 RPM = 1 request per 4 seconds
// Changed to safer settings to prevent 429 errors:
// - Reduced tokens from 15 to 12 (80% capacity for safety buffer)
// - Increased refill interval from 4s to 5s (25% slower but safer)
// This gives ~20% safety margin to handle network latency and burst traffic
var globalRateLimiter = NewRateLimiter(12, 5*time.Second)

// WaitForRateLimit waits if we're hitting rate limits
func WaitForRateLimit() {
	globalRateLimiter.Wait()
}
