package shopbrain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP: one shop's brain as read-only tools for any MCP client (Claude,
// Codex, Gemini …). A request carries "Authorization: Bearer sbk_…"; the
// token decides the shop. Stateless Streamable HTTP with JSON responses, so
// it passes through the reverse proxy like any API call.

const mcpInstructions = `สมองร้าน (Shop Brain) ของร้านเดียว — อ่านอย่างเดียว
สมองจำว่าร้านนี้เคยลงบัญชีแต่ละเรื่องอย่างไร จากรายการบัญชีที่บันทึกไว้แล้ว และตอบเฉพาะเมื่อผ่านเกณฑ์ความมั่นใจ ไม่งั้นตอบว่า "ไม่รู้"

วิธีใช้:
1. เริ่มด้วย brain_overview เพื่อรู้จักร้าน ความพร้อม และความแม่นของสมอง
2. ถามวิธีลงบัญชี: ถ้ารู้ชื่อคู่ค้า เรียก search_parties ก่อนเพื่อได้ party_code แล้วจึงเรียก ask_brain
   ใส่คำอธิบายรายการ (description) ยอดรวมเอกสาร (amount) และ direction (in/out) สำหรับรายการเงินเข้า/ออกจาก Statement ธนาคาร
3. อยากรู้ว่าทำไมสมองตอบแบบนี้กับเอกสารที่บันทึกแล้ว ใช้ explain_journal
4. ดูสิ่งที่สมองจำไว้: list_patterns, get_pattern • ค้นผังบัญชี: search_accounts

กติกา:
- ถ้า ask_brain ตอบ known=false ห้ามเดาวิธีลงบัญชีแทนสมอง ให้บอกผู้ใช้ตามเหตุผลที่สมองให้มา
- สมองอยู่ในช่วงทดสอบ คำตอบเป็นข้อเสนอ ต้องให้คน/นักบัญชียืนยันก่อนบันทึกจริงเสมอ
- เครื่องมือทั้งหมดอ่านอย่างเดียว ไม่สามารถบันทึกหรือแก้รายการบัญชีได้
- ตอบผู้ใช้เป็นภาษาไทย อ้างตัวเลขจากผลของเครื่องมือเท่านั้น`

type mcpCtxKey struct{}

type mcpAuth struct {
	tokenID string
	shopID  string
}

// MCPHandler serves /billscan/mcp.
func (h *Handlers) MCPHandler() http.Handler {
	inner := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		a, _ := r.Context().Value(mcpCtxKey{}).(mcpAuth)
		return h.newMCPServer(a)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, status, msg := h.mcpAuthenticate(r)
		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Bearer realm="shopbrain"`)
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": msg})
			return
		}
		// last used + call count, off the request path
		go func(id string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := h.Tokens.TouchToken(ctx, id, time.Now()); err != nil {
				log.Printf("shopbrain mcp: touch token: %v", err)
			}
		}(a.tokenID)
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpCtxKey{}, a)))
	})
}

func (h *Handlers) mcpAuthenticate(r *http.Request) (mcpAuth, int, string) {
	if h.Tokens == nil {
		return mcpAuth{}, http.StatusServiceUnavailable, "mcp_unavailable"
	}
	tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if !strings.HasPrefix(tok, MCPTokenPrefix) {
		return mcpAuth{}, http.StatusUnauthorized, "missing_or_invalid_token"
	}
	t, err := h.Tokens.FindToken(r.Context(), HashMCPToken(tok))
	switch {
	case errors.Is(err, ErrTokenInvalid):
		return mcpAuth{}, http.StatusUnauthorized, "invalid_token"
	case err != nil:
		return mcpAuth{}, http.StatusServiceUnavailable, "mcp_unavailable"
	}
	switch t.Status(time.Now()) {
	case "revoked":
		return mcpAuth{}, http.StatusUnauthorized, "token_revoked"
	case "expired":
		return mcpAuth{}, http.StatusUnauthorized, "token_expired"
	}
	// ?shopid= is optional; when given it must match the token's shop
	if q := strings.TrimSpace(r.URL.Query().Get("shopid")); q != "" && q != t.ShopID {
		return mcpAuth{}, http.StatusForbidden, "token_not_for_this_shop"
	}
	return mcpAuth{tokenID: t.ID, shopID: t.ShopID}, 0, ""
}

func (h *Handlers) newMCPServer(a mcpAuth) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "shopbrain", Title: "สมองร้าน (Shop Brain)", Version: fmt.Sprintf("brain-v%d", BrainVersion)},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	addMCPTool(h, a, s, &mcp.Tool{Name: "brain_overview", Annotations: ro,
		Description: "ภาพรวมสมองของร้าน: จำนวนรายการ ช่วงเวลา ความพร้อม % (4 เงื่อนไข) ความแม่นจากการจำลองย้อนหลัง แยกบิล/Statement และเกณฑ์ที่ใช้"},
		func(ctx context.Context, b *Brain, _ struct{}) (string, any, error) {
			return h.mcpOverview(ctx, a, b)
		})

	addMCPTool(h, a, s, &mcp.Tool{Name: "ask_brain", Annotations: ro,
		Description: "ถามว่ารายการหนึ่งควรลงบัญชีอย่างไร ต้องมีอย่างน้อยหนึ่งอย่าง: party_code, taxid, docformat หรือ description ใส่ amount (ยอดรวมทั้งเอกสาร) เพื่อแตกยอดแต่ละบรรทัด และ direction (in/out) สำหรับ Statement ธนาคาร ตอบเป็น Dr/Cr หรือ known=false พร้อมเหตุผล"},
		func(_ context.Context, b *Brain, in mcpAskInput) (string, any, error) {
			ai := in.toAskInput()
			if ai.Empty() {
				return "", nil, errors.New("ต้องระบุอย่างน้อยหนึ่งอย่าง: party_code, taxid, docformat หรือ description")
			}
			ans := b.Ask(ai)
			return askSummary(ans), ans, nil
		})

	addMCPTool(h, a, s, &mcp.Tool{Name: "search_parties", Annotations: ro,
		Description: "ค้นหาคู่ค้า (เจ้าหนี้/ลูกหนี้) ด้วยชื่อ รหัส หรือเลขผู้เสียภาษี เพื่อเอา party_kind และ party_code ไปใช้กับ ask_brain"},
		func(_ context.Context, b *Brain, in mcpSearchInput) (string, any, error) {
			res := MatchParties(b.Master, in.Query, clampLimit(in.Limit, 8, 30))
			if len(res) == 0 {
				return fmt.Sprintf("ไม่พบคู่ค้าที่ตรงกับ %q", in.Query), map[string]any{"parties": []PartyMatch{}}, nil
			}
			return fmt.Sprintf("พบคู่ค้า %d ราย อันดับแรก: %s (%s %s)", len(res), res[0].Name, res[0].Kind, res[0].Code), map[string]any{"parties": res}, nil
		})

	addMCPTool(h, a, s, &mcp.Tool{Name: "search_accounts", Annotations: ro,
		Description: "ค้นหาผังบัญชีของร้านด้วยรหัสหรือชื่อบัญชี พร้อมจำนวนครั้งที่ร้านใช้จริง"},
		func(_ context.Context, b *Brain, in mcpSearchInput) (string, any, error) {
			res := searchAccounts(b, in.Query, clampLimit(in.Limit, 20, 100))
			return fmt.Sprintf("พบ %d บัญชี", len(res)), map[string]any{"accounts": res}, nil
		})

	addMCPTool(h, a, s, &mcp.Tool{Name: "list_patterns", Annotations: ro,
		Description: "รายการวิธีลงบัญชีที่สมองจำไว้ เรียงจากที่เจอบ่อยสุด กรองได้ด้วย level (L1_taxid, L2_party, L3_description, L4_format), usable_only (เฉพาะที่มั่นใจพอจะตอบ) และ query (คำค้นในคู่ค้า/คำอธิบาย/บัญชี)"},
		func(_ context.Context, b *Brain, in mcpListInput) (string, any, error) {
			res, total := listPatterns(b, in)
			return fmt.Sprintf("แสดง %d จาก %d วิธีลงบัญชีที่ตรงเงื่อนไข", len(res), total), map[string]any{"total": total, "patterns": res}, nil
		})

	addMCPTool(h, a, s, &mcp.Tool{Name: "get_pattern", Annotations: ro,
		Description: "รายละเอียดวิธีลงบัญชีหนึ่งแบบด้วย id (PAT-…): แบบที่ชนะ สัดส่วนยอดแต่ละบรรทัด แบบอื่นที่เคยลง ประวัติการเปลี่ยน และเอกสารตัวอย่าง"},
		func(_ context.Context, b *Brain, in mcpPatternInput) (string, any, error) {
			p := patternByID(b, in.ID)
			if p == nil {
				return "", nil, fmt.Errorf("ไม่พบวิธีลงบัญชี %q", in.ID)
			}
			return fmt.Sprintf("%s • เจอ %d ครั้ง • ลงแบบเดียวกัน %.0f%% • %s", patternSubjectText(p), p.Total, p.Consistency*100, usableText(p.Usable)), p, nil
		})

	addMCPTool(h, a, s, &mcp.Tool{Name: "explain_journal", Annotations: ro,
		Description: "ใส่เลขที่เอกสาร (docno) หรือ guidfixed ของรายการที่บันทึกแล้ว ดูว่าสมองจะทายว่าอะไร (โดยรู้เฉพาะรายการก่อนหน้า) เทียบกับที่บันทึกจริง และหลักฐานทุกชั้นว่าทำไม"},
		func(_ context.Context, b *Brain, in mcpJournalInput) (string, any, error) {
			i := journalIndex(b, in.DocNo, in.GuidFixed)
			if i < 0 {
				return "", nil, errors.New("ไม่พบรายการนี้ในข้อมูลที่สมองเรียน (ตรวจเลขที่เอกสาร หรือรายการไม่มีบรรทัดบัญชี)")
			}
			e, g := b.entries[i], b.guesses[i]
			out := map[string]any{
				"journal": b.quizJournal(e), "guess": b.guessView(g), "actual": actualView(b, e),
				"match_accounts": g.Known() && g.Accounts == e.shape.Accounts, "evidence": b.evidence(i, g),
			}
			sum := "สมองตอบไม่ได้: " + b.guessView(g).Reason
			if g.Known() {
				sum = fmt.Sprintf("สมองทายจาก%s (เคยเห็น %d ครั้ง) • %s", levelText(g.Level), g.Support, map[bool]string{true: "ตรงกับที่บันทึกไว้", false: "ไม่ตรงกับที่บันทึกไว้"}[g.Accounts == e.shape.Accounts])
			}
			return sum, out, nil
		})
	return s
}

// addMCPTool binds a tool to this shop's brain, logs the call and returns a
// Thai one-line summary plus the full result as JSON text (and structured).
func addMCPTool[In any](h *Handlers, a mcpAuth, s *mcp.Server, t *mcp.Tool, fn func(context.Context, *Brain, In) (string, any, error)) {
	mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ok := false
		defer func() {
			go func(ok bool) {
				lctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = h.Tokens.LogCall(lctx, MCPCall{TokenID: a.tokenID, ShopID: a.shopID, Tool: t.Name, OK: ok, At: time.Now()})
			}(ok)
		}()
		b, err := h.Svc.Brain(ctx, a.shopID, false)
		if err != nil {
			return nil, nil, fmt.Errorf("โหลดสมองของร้านไม่สำเร็จ: %v", err)
		}
		summary, out, err := fn(ctx, b, in)
		if err != nil {
			return nil, nil, err
		}
		ok = true
		js, _ := json.Marshal(out)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}, &mcp.TextContent{Text: string(js)}}}, out, nil
	})
}

// ---------- inputs ----------

type mcpAskInput struct {
	PartyKind   string  `json:"party_kind,omitempty" jsonschema:"creditor (ผู้ขาย/เจ้าหนี้) หรือ debtor (ลูกค้า/ลูกหนี้) จาก search_parties"`
	PartyCode   string  `json:"party_code,omitempty" jsonschema:"รหัสคู่ค้าจาก search_parties"`
	TaxID       string  `json:"taxid,omitempty" jsonschema:"เลขผู้เสียภาษี 13 หลักของคู่ค้า"`
	DocFormat   string  `json:"docformat,omitempty" jsonschema:"รหัสประเภทเอกสาร (ถ้าร้านใช้)"`
	BookCode    string  `json:"bookcode,omitempty" jsonschema:"รหัสสมุดบัญชี เช่น BOOK-02"`
	Description string  `json:"description,omitempty" jsonschema:"คำอธิบายรายการ เช่น ค่าไฟฟ้า หรือข้อความจาก Statement"`
	Amount      float64 `json:"amount,omitempty" jsonschema:"ยอดรวมทั้งเอกสาร (รวม VAT) เพื่อแตกยอดแต่ละบรรทัด"`
	Direction   string  `json:"direction,omitempty" jsonschema:"Statement ธนาคาร: in = เงินเข้า, out = เงินออก"`
}

func (in mcpAskInput) toAskInput() AskInput {
	return AskInput{PartyKind: in.PartyKind, PartyCode: in.PartyCode, TaxID: in.TaxID, DocFormat: in.DocFormat, BookCode: in.BookCode,
		Description: in.Description, Amount: math.Max(0, in.Amount), Direction: strings.ToLower(strings.TrimSpace(in.Direction))}
}

type mcpSearchInput struct {
	Query string `json:"query" jsonschema:"คำค้น: ชื่อ รหัส หรือเลขผู้เสียภาษี"`
	Limit int    `json:"limit,omitempty" jsonschema:"จำนวนผลลัพธ์สูงสุด"`
}

type mcpListInput struct {
	Level      string `json:"level,omitempty" jsonschema:"L1_taxid, L2_party, L3_description หรือ L4_format"`
	UsableOnly bool   `json:"usable_only,omitempty" jsonschema:"true = เฉพาะที่สมองมั่นใจพอจะตอบ"`
	Query      string `json:"query,omitempty" jsonschema:"คำค้นในคู่ค้า คำอธิบาย หรือบัญชี"`
	Limit      int    `json:"limit,omitempty" jsonschema:"จำนวนสูงสุด (ค่าเริ่ม 20, สูงสุด 200)"`
}

type mcpPatternInput struct {
	ID string `json:"id" jsonschema:"รหัสวิธีลงบัญชี เช่น PAT-1A2B3C4D จาก list_patterns"`
}

type mcpJournalInput struct {
	DocNo     string `json:"docno,omitempty" jsonschema:"เลขที่เอกสาร"`
	GuidFixed string `json:"guidfixed,omitempty" jsonschema:"guidfixed ของรายการ (ถ้ามี)"`
}

func clampLimit(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// ---------- helpers ----------

func levelText(l Level) string {
	return map[Level]string{LevelTaxID: "เลขผู้เสียภาษี", LevelParty: "รหัสคู่ค้า", LevelDesc: "คำอธิบายรายการ", LevelFormat: "ประเภทเอกสาร + สมุด", levelNoMatch: "ไม่รู้"}[l]
}

func usableText(u bool) string {
	if u {
		return "มั่นใจ ช่วยลงได้"
	}
	return "ยังไม่มั่นใจ"
}

func patternSubjectText(p *Pattern) string {
	s := ""
	switch p.Level {
	case LevelTaxID, LevelParty:
		s = strings.TrimSpace(p.PartyCode + " " + p.PartyName)
		if s == "" {
			s = "เลขผู้เสียภาษี " + p.TaxID
		}
	case LevelDesc:
		s = `"` + p.Description + `"`
	default:
		s = "สมุด " + p.BookCode
	}
	switch p.Direction {
	case "in":
		s += " • เงินเข้า"
	case "out":
		s += " • เงินออก"
	}
	return s
}

func shapeText(sv *ShapeView) string {
	if sv == nil {
		return ""
	}
	parts := make([]string, 0, len(sv.Lines))
	for _, l := range sv.Lines {
		side := "Dr"
		if l.Side == "C" {
			side = "Cr"
		}
		p := side + " " + l.AccountCode + " " + l.AccountName
		if l.Amount != 0 {
			p += fmt.Sprintf(" %.2f", l.Amount)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " / ")
}

func askSummary(a AskAnswer) string {
	if !a.Known {
		return "สมองตอบไม่ได้: " + a.Reason
	}
	return fmt.Sprintf("ลงบัญชี: %s (จำจาก%s • เคยลงแบบนี้ %d ครั้ง • ลงแบบเดียวกัน %.0f%%)", shapeText(a.Shape), levelText(a.Level), a.Support, a.Consistency*100)
}

type mcpAccount struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Category int    `json:"category"`
	Used     int    `json:"used"`
}

func searchAccounts(b *Brain, q string, limit int) []mcpAccount {
	q = strings.ToLower(strings.TrimSpace(q))
	used := map[string]int{}
	for _, u := range b.Usage.Accounts {
		used[u.Code] = u.Count
	}
	var out []mcpAccount
	for _, ac := range b.Master.Accounts {
		if q != "" && !strings.Contains(strings.ToLower(ac.Code+" "+ac.Name), q) {
			continue
		}
		out = append(out, mcpAccount{Code: ac.Code, Name: ac.Name, Category: ac.Category, Used: used[ac.Code]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Used != out[j].Used {
			return out[i].Used > out[j].Used
		}
		return out[i].Code < out[j].Code
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

type mcpPatternRow struct {
	ID          string       `json:"id"`
	Level       Level        `json:"level"`
	Subject     string       `json:"subject"`
	Total       int          `json:"total"`
	Consistency float64      `json:"consistency"`
	Usable      bool         `json:"usable"`
	Lines       []*ShapeLine `json:"lines"`
}

func listPatterns(b *Brain, in mcpListInput) ([]mcpPatternRow, int) {
	q := strings.ToLower(strings.TrimSpace(in.Query))
	var hit []*Pattern
	for _, p := range b.Patterns {
		if in.Level != "" && string(p.Level) != in.Level {
			continue
		}
		if in.UsableOnly && !p.Usable {
			continue
		}
		if q != "" {
			hay := strings.ToLower(patternSubjectText(p) + " " + p.TaxID)
			if p.Winner != nil {
				for _, l := range p.Winner.Lines {
					hay += " " + strings.ToLower(l.AccountCode+" "+l.AccountName)
				}
			}
			if !strings.Contains(hay, q) {
				continue
			}
		}
		hit = append(hit, p)
	}
	sort.SliceStable(hit, func(i, j int) bool { return hit[i].Total > hit[j].Total })
	limit := clampLimit(in.Limit, 20, 200)
	rows := make([]mcpPatternRow, 0, limit)
	for _, p := range hit {
		if len(rows) == limit {
			break
		}
		r := mcpPatternRow{ID: p.ID, Level: p.Level, Subject: patternSubjectText(p), Total: p.Total, Consistency: p.Consistency, Usable: p.Usable}
		if p.Winner != nil {
			r.Lines = p.Winner.Lines
		}
		rows = append(rows, r)
	}
	return rows, len(hit)
}

func patternByID(b *Brain, id string) *Pattern {
	id = strings.ToUpper(strings.TrimSpace(id))
	for _, p := range b.Patterns {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func journalIndex(b *Brain, docno, guid string) int {
	docno, guid = strings.TrimSpace(docno), strings.TrimSpace(guid)
	for i, e := range b.entries {
		if (guid != "" && e.j.GuidFixed == guid) || (docno != "" && strings.EqualFold(e.j.DocNo, docno)) {
			return i
		}
	}
	return -1
}

func (h *Handlers) mcpOverview(ctx context.Context, a mcpAuth, b *Brain) (string, any, error) {
	verdicts, known := 0, false
	if h.Store != nil {
		if list, err := h.Store.List(ctx, a.shopID); err == nil {
			known = true
			for _, f := range list {
				if f.Mode == "quiz" {
					verdicts++
				}
			}
		}
	}
	rd := ComputeReadiness(b, verdicts, known, time.Now())
	usable := 0
	for _, p := range b.Patterns {
		if p.Usable {
			usable++
		}
	}
	out := map[string]any{
		"shop":            map[string]any{"shopid": a.shopID, "name": b.Master.Shop.Name},
		"generated_at":    b.GeneratedAt,
		"readiness_score": rd,
		"journals":        b.Readiness.Journals,
		"by_source":       b.Readiness.BySource,
		"first_entry":     b.Readiness.FirstEntry,
		"last_entry":      b.Readiness.LastEntry,
		"patterns":        len(b.Patterns),
		"usable_patterns": usable,
		"backtest":        map[string]any{"overall": b.Backtest.Overall, "by_source": b.Backtest.BySource, "by_level": b.Backtest.ByLevel},
		"rules": map[string]any{
			"min_consistency": b.Options.MinConsistency, "min_consistency_format": b.Options.MinConsistencyFormat,
			"wilson_z": b.Options.WilsonZ, "recent_agree": b.Options.RecentAgree, "statement_direction": b.Options.StatementDirection,
		},
	}
	sum := fmt.Sprintf("ร้าน %s • สมองพร้อมใช้งาน %s • เรียนจาก %d รายการ • จำได้ %d แบบ (มั่นใจ %d) • จำลองย้อนหลัง: ตอบ %d จาก %d รายการ ถูก %d",
		b.Master.Shop.Name, rd.ScoreText(), b.Readiness.Journals, len(b.Patterns), usable, b.Backtest.Overall.Predicted, b.Backtest.Overall.Total, b.Backtest.Overall.HitAccounts)
	return sum, out, nil
}
