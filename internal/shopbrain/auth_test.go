package shopbrain

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeMainAPI serves /list-shop like the main API: two pages of shops for "good-token".
func fakeMainAPI(t *testing.T, calls *int32) *httptest.Server {
	t.Helper()
	pages := map[string]string{
		"1": `{"success":true,"data":[{"shopid":"OWNED","role":2},{"shopid":"ADMIN","role":1}],"pagination":{"totalPage":2}}`,
		"2": `{"success":true,"data":[{"shopid":"OWNED_P2","role":2}],"pagination":{"totalPage":2}}`,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.URL.Path != "/list-shop" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"success":false}`)
			return
		}
		fmt.Fprint(w, pages[r.URL.Query().Get("page")])
	}))
}

func TestOwnerVerifier(t *testing.T) {
	var calls int32
	srv := fakeMainAPI(t, &calls)
	defer srv.Close()
	v := NewOwnerVerifier(srv.URL + "/")
	ctx := context.Background()

	cases := []struct {
		token, shop string
		want        error
	}{
		{"good-token", "OWNED", nil},
		{"good-token", "OWNED_P2", nil}, // on page 2
		{"good-token", "ADMIN", ErrNotOwner},
		{"good-token", "SOMEONE_ELSE", ErrNotOwner},
		{"bad-token", "OWNED", ErrUnauthorized},
		{"", "OWNED", ErrUnauthorized},
	}
	for _, c := range cases {
		if err := v.Verify(ctx, c.token, c.shop); !errors.Is(err, c.want) && !(err == nil && c.want == nil) {
			t.Errorf("Verify(%q,%q) = %v, want %v", c.token, c.shop, err, c.want)
		}
	}
	before := atomic.LoadInt32(&calls)
	if err := v.Verify(ctx, "good-token", "OWNED"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != before {
		t.Errorf("second check of the same token+shop should come from cache")
	}
	if err := NewOwnerVerifier("").Verify(ctx, "good-token", "OWNED"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("empty MAIN_API_URL must refuse, got %v", err)
	}
}

func testHandlers(t *testing.T) (*gin.Engine, func()) {
	t.Helper()
	var calls int32
	srv := fakeMainAPI(t, &calls)
	m, js, g := fixture()
	brain := Build(m, js, g, 0, mechanicsOptions(day(10)))
	svc := NewService(NewOwnerVerifier(srv.URL))
	svc.Load = func(ctx context.Context, shopID string) (*Brain, error) { return brain, nil }
	h := &Handlers{Svc: svc}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/shop-brain", h.GetShopBrain)
	r.GET("/shop-brain/export", h.ExportShopBrain)
	return r, srv.Close
}

func do(r *gin.Engine, url, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHandlersEnforceOwner(t *testing.T) {
	r, done := testHandlers(t)
	defer done()
	for _, c := range []struct {
		url, token string
		code       int
	}{
		{"/shop-brain?shopid=OWNED", "", 401},
		{"/shop-brain?shopid=OWNED", "bad-token", 401},
		{"/shop-brain?shopid=ADMIN", "good-token", 403},
		{"/shop-brain/export?shopid=SOMEONE_ELSE&format=json", "good-token", 403},
		{"/shop-brain", "good-token", 400},
	} {
		if w := do(r, c.url, c.token); w.Code != c.code {
			t.Errorf("%s token=%q: got %d want %d (%s)", c.url, c.token, w.Code, c.code, w.Body.String())
		}
	}
}

func TestGetShopBrainOmitsJournalIndex(t *testing.T) {
	r, done := testHandlers(t)
	defer done()
	w := do(r, "/shop-brain?shopid=OWNED", "good-token")
	if w.Code != 200 {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.Success {
		t.Fatalf("bad body: %v %s", err, w.Body.String())
	}
	if strings.Contains(string(body.Data), "journal_index") || !strings.Contains(string(body.Data), `"patterns"`) {
		t.Errorf("view should have patterns and no journal_index")
	}
}

func TestExportVaultZipAndJSON(t *testing.T) {
	r, done := testHandlers(t)
	defer done()
	w := do(r, "/shop-brain/export?shopid=OWNED&format=vault", "good-token")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("vault export: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["shop-brain/00-Index.md"] || !names["shop-brain/04-คู่ค้า/คู่ค้า-เจ้าหนี้-C1.md"] {
		t.Errorf("zip entries: %v", names)
	}
	w = do(r, "/shop-brain/export?shopid=OWNED&format=json", "good-token")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "journal_index") {
		t.Errorf("full json export should include the index: %d", w.Code)
	}
	if w := do(r, "/shop-brain/export?shopid=OWNED&format=pdf", "good-token"); w.Code != 400 {
		t.Errorf("unknown format: %d", w.Code)
	}
}

// TestOwnerVerifierLive checks the real main API when SHOPBRAIN_TEST_TOKEN,
// SHOPBRAIN_TEST_API and SHOPBRAIN_TEST_SHOP are set (token is never written anywhere).
func TestOwnerVerifierLive(t *testing.T) {
	token, api, shop := os.Getenv("SHOPBRAIN_TEST_TOKEN"), os.Getenv("SHOPBRAIN_TEST_API"), os.Getenv("SHOPBRAIN_TEST_SHOP")
	if token == "" || api == "" || shop == "" {
		t.Skip("set SHOPBRAIN_TEST_TOKEN, SHOPBRAIN_TEST_API and SHOPBRAIN_TEST_SHOP to run against the real main API")
	}
	v := NewOwnerVerifier(api)
	ctx := context.Background()
	if err := v.Verify(ctx, token, shop); err != nil {
		t.Errorf("owner shop %s: %v", shop, err)
	}
	if err := v.Verify(ctx, token, "not-a-real-shop-id"); !errors.Is(err, ErrNotOwner) {
		t.Errorf("unknown shop: %v", err)
	}
	if err := v.Verify(ctx, "0000-not-a-token", shop); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("bad token: %v", err)
	}
}
