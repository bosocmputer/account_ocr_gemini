package shopbrain

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Service builds brains on request and keeps each shop's brain for TTL, so
// repeated page views do not re-read Mongo. Nothing is persisted.
type Service struct {
	Verifier interface {
		Verify(ctx context.Context, token, shopID string) error
	}
	// Load builds a fresh brain; LoadFromMongo in production, a fixture in tests.
	Load func(ctx context.Context, shopID string) (*Brain, error)
	TTL  time.Duration

	mu    sync.Mutex
	cache map[string]*Brain
	sf    singleflight.Group
}

func NewService(v *OwnerVerifier) *Service {
	return &Service{Verifier: v, Load: LoadFromMongo, TTL: 10 * time.Minute, cache: map[string]*Brain{}}
}

// LoadFromMongo is the production loader: master data + journals → Build.
func LoadFromMongo(ctx context.Context, shopID string) (*Brain, error) {
	master, errsM, err := LoadMaster(ctx, shopID)
	if err != nil {
		return nil, err
	}
	journals, groups, errsJ, err := LoadJournals(ctx, shopID)
	if err != nil {
		return nil, err
	}
	return Build(master, journals, groups, errsM+errsJ, DefaultOptions(time.Now())), nil
}

// Brain returns the shop's brain, from cache unless refresh is set.
// Concurrent requests for the same shop share one build.
func (s *Service) Brain(ctx context.Context, shopID string, refresh bool) (*Brain, error) {
	if !refresh {
		s.mu.Lock()
		b := s.cache[shopID]
		s.mu.Unlock()
		if b != nil && time.Since(b.GeneratedAt) < s.TTL {
			return b, nil
		}
	}
	v, err, _ := s.sf.Do(shopID, func() (interface{}, error) {
		// Detach from the caller so one cancelled page load does not fail the others waiting on it.
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
		defer cancel()
		b, err := s.Load(bctx, shopID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.cache[shopID] = b
		s.mu.Unlock()
		return b, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Brain), nil
}
