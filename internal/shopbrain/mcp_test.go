package shopbrain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// memTokens is an in-memory MCPTokenStore for tests.
type memTokens struct {
	mu    sync.Mutex
	byID  map[string]*MCPToken
	calls []MCPCall
}

func newMemTokens() *memTokens { return &memTokens{byID: map[string]*MCPToken{}} }

func (m *memTokens) CreateToken(_ context.Context, t MCPToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[t.ID] = &t
	return nil
}
func (m *memTokens) ListTokens(_ context.Context, shopID string) ([]MCPToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MCPToken
	for _, t := range m.byID {
		if t.ShopID == shopID {
			out = append(out, *t)
		}
	}
	return out, nil
}
func (m *memTokens) RevokeToken(_ context.Context, shopID, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.byID[id]
	if t == nil || t.ShopID != shopID || t.RevokedAt != nil {
		return ErrTokenInvalid
	}
	t.RevokedAt = &at
	return nil
}
func (m *memTokens) FindToken(_ context.Context, hash string) (*MCPToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.byID {
		if t.Hash == hash {
			c := *t
			return &c, nil
		}
	}
	return nil, ErrTokenInvalid
}
func (m *memTokens) TouchToken(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.byID[id]; t != nil {
		t.LastUsedAt = &at
		t.Calls++
	}
	return nil
}
func (m *memTokens) LogCall(_ context.Context, c MCPCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c)
	return nil
}

func mcpTestServer(t *testing.T) (*httptest.Server, *memTokens, string) {
	t.Helper()
	m, js, g := fixture()
	svc := &Service{TTL: time.Minute, cache: map[string]*Brain{},
		Load: func(ctx context.Context, shopID string) (*Brain, error) {
			return Build(m, js, g, 0, mechanicsOptions(day(10))), nil
		}}
	tokens := newMemTokens()
	h := &Handlers{Svc: svc, Tokens: tokens}
	plain, rec, err := NewMCPToken("SHOP1", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = tokens.CreateToken(context.Background(), rec)
	srv := httptest.NewServer(h.MCPHandler())
	t.Cleanup(srv.Close)
	return srv, tokens, plain
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearerRT{token}}}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func postStatus(t *testing.T, url, auth string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestMCPRejectsMissingWrongExpiredRevokedAndOtherShop(t *testing.T) {
	srv, tokens, plain := mcpTestServer(t)
	if s := postStatus(t, srv.URL, ""); s != http.StatusUnauthorized {
		t.Errorf("no token = %d", s)
	}
	if s := postStatus(t, srv.URL, "Bearer sbk_wrong"); s != http.StatusUnauthorized {
		t.Errorf("wrong token = %d", s)
	}
	if s := postStatus(t, srv.URL+"?shopid=OTHER", "Bearer "+plain); s != http.StatusForbidden {
		t.Errorf("token for another shop = %d", s)
	}
	// expired
	exp, rec, _ := NewMCPToken("SHOP1", "old", time.Now().Add(-MCPTokenTTL-time.Hour))
	_ = tokens.CreateToken(context.Background(), rec)
	if s := postStatus(t, srv.URL, "Bearer "+exp); s != http.StatusUnauthorized {
		t.Errorf("expired token = %d", s)
	}
	// revoked
	rv, rec2, _ := NewMCPToken("SHOP1", "gone", time.Now())
	_ = tokens.CreateToken(context.Background(), rec2)
	_ = tokens.RevokeToken(context.Background(), "SHOP1", rec2.ID, time.Now())
	if s := postStatus(t, srv.URL, "Bearer "+rv); s != http.StatusUnauthorized {
		t.Errorf("revoked token = %d", s)
	}
}

func TestMCPToolsAreReadOnlyAndAnswerFromTheTokensShop(t *testing.T) {
	srv, tokens, plain := mcpTestServer(t)
	cs := connect(t, srv.URL+"?shopid=SHOP1", plain)
	if ins := cs.InitializeResult().Instructions; !strings.Contains(ins, "อ่านอย่างเดียว") {
		t.Errorf("instructions missing: %q", ins)
	}
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"brain_overview": true, "ask_brain": true, "search_parties": true, "search_accounts": true, "list_patterns": true, "get_pattern": true, "explain_journal": true}
	if len(lt.Tools) != len(want) {
		t.Errorf("tools = %d, want %d", len(lt.Tools), len(want))
	}
	for _, tl := range lt.Tools {
		if !want[tl.Name] || tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("tool %s must be known and read-only: %+v", tl.Name, tl.Annotations)
		}
	}

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}
	text := func(r *mcp.CallToolResult) string { return r.Content[0].(*mcp.TextContent).Text }

	if r := call("brain_overview", map[string]any{}); r.IsError || !strings.Contains(text(r), "สมองพร้อมใช้งาน") {
		t.Errorf("overview: %+v", r)
	}
	// the fixture's creditor C1 is answered by tax id (mechanics options)
	r := call("ask_brain", map[string]any{"party_code": "C1", "party_kind": "creditor", "docformat": "PV", "amount": 1070})
	if r.IsError || !strings.HasPrefix(text(r), "ลงบัญชี:") || len(r.Content) != 2 {
		t.Errorf("ask_brain: %s", text(r))
	}
	var ans AskAnswer
	if err := json.Unmarshal([]byte(r.Content[1].(*mcp.TextContent).Text), &ans); err != nil || !ans.Known {
		t.Errorf("ask_brain JSON: %v %+v", err, ans)
	}
	if r := call("ask_brain", map[string]any{}); !r.IsError {
		t.Errorf("empty ask must be an error result")
	}
	if r := call("search_parties", map[string]any{"query": "C1"}); r.IsError {
		t.Errorf("search_parties: %s", text(r))
	}
	if r := call("list_patterns", map[string]any{"limit": 5}); r.IsError || !strings.Contains(text(r), "แสดง") {
		t.Errorf("list_patterns: %s", text(r))
	}
	if r := call("get_pattern", map[string]any{"id": "PAT-NOPE"}); !r.IsError {
		t.Errorf("unknown pattern must be an error result")
	}
	if r := call("explain_journal", map[string]any{"guidfixed": "j2"}); r.IsError {
		t.Errorf("explain_journal: %s", text(r))
	}

	time.Sleep(100 * time.Millisecond) // usage log is written off the request path
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	if len(tokens.calls) < 7 {
		t.Errorf("each tool call is logged: %d", len(tokens.calls))
	}
	for _, c := range tokens.calls {
		if c.ShopID != "SHOP1" {
			t.Errorf("call logged for the wrong shop: %+v", c)
		}
	}
}

// Same numbers as the page's test (tests/shop-brain-readiness.test.js).
func TestComputeReadinessMatchesThePage(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	b := &Brain{Options: DefaultOptions(now)}
	b.Readiness = Readiness{Journals: 500, LastEntry: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	b.Backtest.BySource = map[Source]*HitStat{SourceImage: {Total: 300, Predicted: 150, HitAccounts: 144}}
	if r := ComputeReadiness(b, 0, true, now); r.Score == nil || *r.Score != 58 {
		t.Errorf("score = %v, want 58: %+v", r.ScoreText(), r.Conditions)
	}
	b.Readiness = Readiness{} // brand-new shop: only "data" counts
	b.Backtest.BySource = map[Source]*HitStat{}
	if r := ComputeReadiness(b, 0, true, now); r.Score == nil || *r.Score != 0 {
		t.Errorf("new shop score = %v", r.ScoreText())
	}
}
