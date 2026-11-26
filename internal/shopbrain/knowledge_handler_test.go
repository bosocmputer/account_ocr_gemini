package shopbrain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testKnowledgeStore struct {
	records map[string]KnowledgeItem
	history []KnowledgeHistory
}

func (m *testKnowledgeStore) ListKnowledge(_ context.Context, shopID string) ([]KnowledgeItem, error) {
	out := []KnowledgeItem{}
	for _, item := range m.records {
		if item.ShopID == shopID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *testKnowledgeStore) GetKnowledge(_ context.Context, shopID, id string) (*KnowledgeItem, error) {
	item, ok := m.records[id]
	if !ok || item.ShopID != shopID {
		return nil, errKnowledgeNotFound
	}
	return &item, nil
}

func (m *testKnowledgeStore) SaveKnowledge(_ context.Context, before *KnowledgeItem, after KnowledgeItem, action string) error {
	m.records[after.ID] = after
	m.history = append(m.history, KnowledgeHistory{KnowledgeID: after.ID, ShopID: after.ShopID, Before: before, After: &after, Action: action, By: after.UpdatedBy, At: after.UpdatedAt})
	return nil
}

func (m *testKnowledgeStore) KnowledgeHistory(_ context.Context, shopID, id string) ([]KnowledgeHistory, error) {
	out := []KnowledgeHistory{}
	for _, row := range m.history {
		if row.ShopID == shopID && row.KnowledgeID == id {
			out = append(out, row)
		}
	}
	return out, nil
}

func TestShopKnowledgeOwnerHistoryAndPrompt(t *testing.T) {
	mainAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		role := 1
		if r.Header.Get("Authorization") == "Bearer owner" {
			role = 2
		}
		if r.URL.Path == "/profile" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"username": "owner-fixture"}})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]any{{"shopid": "shopA", "role": role}}})
		}
	}))
	defer mainAPI.Close()
	m, journals, groups := fixture()
	service := NewService(NewOwnerVerifier(mainAPI.URL))
	service.Load = func(context.Context, string) (*Brain, error) {
		return Build(m, journals, groups, 0, mechanicsOptions(day(10))), nil
	}
	store := &testKnowledgeStore{records: map[string]KnowledgeItem{}}
	tokens := newMemTokens()
	draftCalls := 0
	h := &Handlers{Svc: service, Knowledge: store, Tokens: tokens}
	h.DraftModel = func(_ context.Context, in draftContext) (draftProposal, error) {
		draftCalls++
		if in.Request == "จำชื่อร้าน" {
			return draftProposal{Question: "ชื่อที่เห็นบนบิลคืออะไร?"}, nil
		}
		return draftProposal{Kind: "alias", Data: KnowledgeData{Name: "ชื่อบนบิลกรณีทดสอบ"}}, nil
	}
	router := gin.New()
	router.GET("/knowledge", h.ListShopKnowledge)
	router.POST("/knowledge", h.SaveShopKnowledge)
	router.POST("/knowledge/draft", h.GenerateShopKnowledgeDraft)
	router.PUT("/knowledge/:id", h.SaveShopKnowledge)
	router.DELETE("/knowledge/:id", h.DeleteShopKnowledge)
	router.GET("/knowledge/history", h.ShopKnowledgeHistory)
	router.GET("/prompt", h.ShopBrainPrompt)
	router.POST("/tokens/:id/rotate", h.RotateMCPToken)
	router.Any("/mcp", gin.WrapH(h.MCPHandler()))
	request := func(method, path, token, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, out.Code, want, out.Body)
		}
		return out
	}
	request("GET", "/knowledge?shopid=shopA", "staff", "", 403)
	draftPath := "/knowledge/draft?shopid=shopA"
	request("POST", draftPath, "staff", `{"text":"จำชื่อร้าน"}`, 403)
	request("POST", draftPath, "owner", `{"text":"  "}`, 400)
	if draftCalls != 0 {
		t.Fatalf("model called without owner access or valid input")
	}
	question := request("POST", draftPath, "owner", `{"text":"จำชื่อร้าน"}`, 200)
	draft := request("POST", draftPath, "owner", `{"text":"บนบิลใช้ชื่อ ชื่อบนบิลกรณีทดสอบ"}`, 200)
	if !strings.Contains(question.Body.String(), "ชื่อที่เห็นบนบิลคืออะไร?") ||
		!strings.Contains(draft.Body.String(), `"kind":"alias"`) || len(store.records) != 0 || len(store.history) != 0 {
		t.Fatalf("draft or question must never save records: %s %s", question.Body, draft.Body)
	}

	request("POST", "/knowledge?shopid=shopA", "owner", `{"kind":"rule","data":{"docformat":"PV","effective_date":"2026-11-01","lines":[{"accountcode":"BAD","side":"D"}]}}`, 400)
	out := request("POST", "/knowledge?shopid=shopA", "owner", `{"kind":"alias","data":{"name":"ชื่อบนบิลกรณีทดสอบ"}}`, 201)
	var created struct {
		Data KnowledgeItem `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &created); err != nil || created.Data.CreatedBy != "owner-fixture" || created.Data.ID == "" {
		t.Fatalf("creation missing identity or ID: %+v, %v", created, err)
	}
	path := "/knowledge/" + created.Data.ID + "?shopid=shopA"
	request("PUT", path, "owner", `{"kind":"alias","data":{"name":"ชื่อใหม่กรณีทดสอบ"},"revision":0}`, 409)
	request("PUT", path, "owner", `{"kind":"alias","data":{"name":"ชื่อใหม่กรณีทดสอบ"},"revision":1}`, 200)

	plain, token, err := NewMCPToken("shopA", "test token", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = tokens.CreateToken(context.Background(), token)
	srv := httptest.NewServer(router)
	defer srv.Close()
	cs := connect(t, srv.URL+"/mcp?shopid=shopA", plain)
	ins := cs.InitializeResult().Instructions
	if !strings.Contains(ins, "ชื่อใหม่กรณีทดสอบ") || strings.Contains(ins, "(REF)") || utf8.RuneCountInString(ins) > maxInstructionRunes {
		t.Fatal("MCP instructions missing shop context, Thai label, or length limit")
	}
	list, err := cs.ListPrompts(context.Background(), nil)
	if err != nil || len(list.Prompts) != 1 || list.Prompts[0].Name != "บันทึกบิล" {
		t.Fatalf("prompt list: %+v %v", list, err)
	}
	prompt := request("GET", "/prompt?shopid=shopA", "owner", "", 200)
	if !strings.Contains(prompt.Body.String(), "ชื่อใหม่กรณีทดสอบ") {
		t.Fatal("web prompt does not contain the current alias")
	}
	request("POST", "/knowledge?shopid=shopA", "owner", `{"kind":"rule","data":{"party_code":"C1","effective_date":"2026-11-01","lines":[{"accountcode":"11111","side":"D"},{"accountcode":"51212","side":"C"}]}}`, 201)
	answer, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "suggest_booking", Arguments: map[string]any{"party_code": "C1", "party_kind": "creditor", "doc_date": "2026-11-25", "total_amount": 100}})
	if err != nil || answer.IsError {
		t.Fatalf("call existing MCP connection after saving the rule: %v", err)
	}
	var suggestion Suggestion
	if err := json.Unmarshal([]byte(answer.Content[1].(*mcp.TextContent).Text), &suggestion); err != nil || suggestion.Source != "shop_rule" || len(suggestion.AppliedKnowledge) != 1 {
		t.Fatalf("existing chat did not use the new rule: %v %+v", err, suggestion)
	}
	rotated := request("POST", "/tokens/"+token.ID+"/rotate?shopid=shopA", "owner", "", 200)
	var newKey struct {
		Token string   `json:"token"`
		Data  MCPToken `json:"data"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &newKey); err != nil || newKey.Token == plain || newKey.Data.Name != token.Name {
		t.Fatalf("invalid rotation result: %v", err)
	}
	if postStatus(t, srv.URL+"/mcp", "Bearer "+plain) != 401 || postStatus(t, srv.URL+"/mcp", "Bearer "+newKey.Token) != 200 {
		t.Fatal("rotation did not revoke the original key and activate the replacement")
	}
	request("DELETE", path, "owner", "", 200)
	request("GET", "/knowledge/history?shopid=shopA&id="+created.Data.ID, "owner", "", 200)
	if len(store.history) != 4 || store.history[3].After.Active || store.history[1].Before.Data.Name != "ชื่อบนบิลกรณีทดสอบ" {
		t.Fatalf("unexpected audit history: %+v", store.history)
	}
}
