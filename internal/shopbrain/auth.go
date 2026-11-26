package shopbrain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RoleOwner is the shop role the main API returns for the shop's owner
// (bcaccount labels 0 User, 1 Admin, 2 Owner — ShopUsers.vue getRoleLabel).
const RoleOwner = 2

var (
	ErrUnauthorized  = errors.New("token missing, invalid or expired")
	ErrNotOwner      = errors.New("only the shop owner can open the shop brain")
	ErrNotConfigured = errors.New("MAIN_API_URL is not configured")
)

// OwnerVerifier asks the main API, with the caller's own token, whether the
// caller owns a shop. The shopid sent by the browser is never trusted on its
// own: GET /list-shop only lists shops that token can access, with its role.
type OwnerVerifier struct {
	BaseURL string
	Client  *http.Client
	TTL     time.Duration

	mu        sync.Mutex
	cache     map[string]verdict
	usernames map[[32]byte]cachedUsername
}

type verdict struct {
	err error
	at  time.Time
}

func NewOwnerVerifier(baseURL string) *OwnerVerifier {
	return &OwnerVerifier{BaseURL: baseURL, Client: &http.Client{Timeout: 10 * time.Second}, TTL: 5 * time.Minute, cache: map[string]verdict{}}
}

type listShopResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		ShopID string `json:"shopid"`
		Role   int    `json:"role"`
	} `json:"data"`
	Pagination struct {
		TotalPage int `json:"totalPage"`
	} `json:"pagination"`
}

// Verify returns nil when the token's user is the owner of shopID,
// ErrUnauthorized for a bad token, ErrNotOwner otherwise. Only definite
// answers are cached (keyed by a hash of the token, never the token itself).
func (v *OwnerVerifier) Verify(ctx context.Context, token, shopID string) error {
	if strings.TrimSpace(v.BaseURL) == "" {
		return ErrNotConfigured
	}
	if token == "" || shopID == "" {
		return ErrUnauthorized
	}
	h := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(h[:]) + "|" + shopID

	v.mu.Lock()
	if c, ok := v.cache[key]; ok && time.Since(c.at) < v.TTL {
		v.mu.Unlock()
		return c.err
	}
	v.mu.Unlock()

	err := v.ask(ctx, token, shopID)
	if err == nil || errors.Is(err, ErrNotOwner) || errors.Is(err, ErrUnauthorized) {
		v.mu.Lock()
		v.cache[key] = verdict{err: err, at: time.Now()}
		v.mu.Unlock()
	}
	return err
}

func (v *OwnerVerifier) ask(ctx context.Context, token, shopID string) error {
	base := strings.TrimRight(v.BaseURL, "/")
	for page := 1; page <= 50; page++ {
		u := fmt.Sprintf("%s/list-shop?page=%d&limit=100", base, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		resp, err := v.Client.Do(req)
		if err != nil {
			return fmt.Errorf("main API unreachable: %w", redactURL(err))
		}
		var body listShopResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return ErrUnauthorized
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("main API list-shop answered %d", resp.StatusCode)
		case decodeErr != nil:
			return fmt.Errorf("main API list-shop: %w", decodeErr)
		case !body.Success:
			return ErrUnauthorized
		}
		for _, s := range body.Data {
			if s.ShopID == shopID {
				if s.Role == RoleOwner {
					return nil
				}
				return ErrNotOwner
			}
		}
		if len(body.Data) == 0 || page >= body.Pagination.TotalPage {
			return ErrNotOwner
		}
	}
	return ErrNotOwner
}

// redactURL keeps the transport error but drops the URL query from it.
func redactURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

type cachedUsername struct {
	name string
	at   time.Time
}

// username uses the owner request's original token. The returned identity is
// only audit metadata, never a substitute for Verify.
func (v *OwnerVerifier) username(ctx context.Context, token string) string {
	key := sha256.Sum256([]byte(token))
	v.mu.Lock()
	cached, ok := v.usernames[key]
	v.mu.Unlock()
	if ok && time.Since(cached.at) < v.TTL {
		return cached.name
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(v.BaseURL, "/")+"/profile", nil)
	if err != nil {
		return "unknown"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := v.Client.Do(req)
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil || !body.Success || strings.TrimSpace(body.Data.Username) == "" {
		return "unknown"
	}
	name := strings.TrimSpace(body.Data.Username)
	v.mu.Lock()
	if v.usernames == nil {
		v.usernames = map[[32]byte]cachedUsername{}
	}
	v.usernames[key] = cachedUsername{name: name, at: time.Now()}
	v.mu.Unlock()
	return name
}
