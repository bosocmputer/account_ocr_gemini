package shopbrain

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func onlyJournal(b *Brain, keep string) map[string]bool {
	ex := map[string]bool{}
	for _, e := range b.entries {
		if e.j.GuidFixed != keep {
			ex[e.j.GuidFixed] = true
		}
	}
	return ex
}

func TestQuizGuessesOnlyFromEarlierJournals(t *testing.T) {
	b := build(t)
	// j1 is C1's first purchase: later j2/j3 exist but must not leak into its guess.
	q, ok := b.Quiz(QuizOptions{Days: 3650, Exclude: onlyJournal(b, "j1")})
	if !ok || q.Journal.GuidFixed != "j1" {
		t.Fatalf("quiz = %+v, %v", q, ok)
	}
	if q.Guess.Known || q.Guess.Reason == "" {
		t.Errorf("first journal of a party must be unknown with a reason, got %+v", q.Guess)
	}
	if len(q.Journal.Images) != 1 || q.Actual.Accounts == "" || q.Journal.PartyCode != "C1" {
		t.Errorf("journal view incomplete: %+v / %+v", q.Journal, q.Actual)
	}

	q, _ = b.Quiz(QuizOptions{Days: 3650, Exclude: onlyJournal(b, "j2")})
	if !q.Guess.Known || q.Guess.Level != LevelTaxID || q.Guess.Support != 1 || !q.MatchAccount || !q.MatchFull {
		t.Errorf("j2 should be guessed from j1 by tax ID: %+v", q.Guess)
	}
	if len(q.Guess.Shape.Lines) != 3 || q.Guess.Shape.Lines[0].AccountName == "" {
		t.Errorf("guess lines should carry account names: %+v", q.Guess.Shape)
	}
}

func TestQuizFiltersStatementLevelAndDays(t *testing.T) {
	b := build(t)
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 30; i++ {
		q, ok := b.Quiz(QuizOptions{Days: 3650, Rand: r})
		if !ok || q.Journal.Source == SourceStatement {
			t.Fatalf("statement journals are excluded by default: %+v", q)
		}
	}
	if q, ok := b.Quiz(QuizOptions{Days: 3650, IncludeStatement: true, Exclude: onlyJournal(b, "j7")}); !ok || q.Journal.Source != SourceStatement {
		t.Errorf("statement=1 should include j7")
	}
	for i := 0; i < 20; i++ {
		q, _ := b.Quiz(QuizOptions{Days: 3650, Level: LevelTaxID, Rand: r})
		if q.Guess.Level != LevelTaxID {
			t.Fatalf("level filter ignored: %s", q.Guess.Level)
		}
	}
	if _, ok := b.Quiz(QuizOptions{Days: 1}); ok {
		t.Errorf("nothing was saved in the last day before AsOf (day 10)")
	}
}

func sideSum(lines []LineView, side string) float64 {
	s := 0.0
	for _, l := range lines {
		if l.Side == side {
			s += l.Amount
		}
	}
	return math.Round(s*100) / 100
}

func TestAskByPartyComputesBalancedAmounts(t *testing.T) {
	b := build(t)
	a := b.Ask(AskInput{PartyKind: "creditor", PartyCode: "C1", DocFormat: "PV", BookCode: "03", Amount: 107})
	if !a.Known || a.Level != LevelTaxID || a.Pattern == nil {
		t.Fatalf("ask by party: %+v", a)
	}
	if d, c := sideSum(a.Shape.Lines, "D"), sideSum(a.Shape.Lines, "C"); d != 107 || c != 107 {
		t.Errorf("Dr %.2f Cr %.2f, want 107 each: %+v", d, c, a.Shape.Lines)
	}
	if a.Checks[0].Level != LevelTaxID || a.Checks[0].Status != "used" {
		t.Errorf("checks should start with L1 used: %+v", a.Checks)
	}
	// the same answer by tax ID alone
	if a2 := b.Ask(AskInput{TaxID: "0105500000001", DocFormat: "PV"}); !a2.Known || a2.Level != LevelTaxID {
		t.Errorf("ask by tax id: %+v", a2)
	}
}

func TestAskFallsBackToDescriptionAndSaysUnknown(t *testing.T) {
	b := build(t)
	a := b.Ask(AskInput{DocFormat: "JV", BookCode: "01", Description: "ค่าไฟฟ้า 04/26"})
	if !a.Known || a.Level != LevelDesc {
		t.Errorf("description route: %+v", a)
	}
	u := b.Ask(AskInput{DocFormat: "ZZ", Description: "ไม่เคยมี"})
	if u.Known || u.Reason == "" || u.Shape != nil {
		t.Errorf("unseen input must be unknown: %+v", u)
	}
	for _, c := range u.Checks {
		if c.Status == "used" {
			t.Errorf("no level may be used: %+v", u.Checks)
		}
	}
}

func TestMatchParties(t *testing.T) {
	m := MasterData{Creditors: []Party{
		{Kind: "creditor", Code: "C1", Name: "ผู้ขาย C1", TaxID: "0105500000001"},
		{Kind: "creditor", Code: "C9", Name: "บริษัท ช.การช่าง จำกัด"},
		{Kind: "creditor", Code: "C7", Name: "ห้างหุ้นส่วนจำกัด ไทยวัสดุ"},
	}}
	if got := MatchParties(m, "ช การช่าง", 5); len(got) == 0 || got[0].Code != "C9" {
		t.Errorf("legal-form words should not matter: %+v", got)
	}
	if got := MatchParties(m, "0105500000001", 5); len(got) == 0 || got[0].Code != "C1" || got[0].Score != 1 {
		t.Errorf("tax id search: %+v", got)
	}
	if got := MatchParties(m, "หจก. ไทยวัสดุ", 5); len(got) == 0 || got[0].Code != "C7" {
		t.Errorf("หจก vs ห้างหุ้นส่วนจำกัด: %+v", got)
	}
	if got := MatchParties(m, "  ", 5); got != nil {
		t.Errorf("blank query: %+v", got)
	}
}

func TestSummarizeKeepsJournalWrongOutOfBrainRate(t *testing.T) {
	s := Summarize([]Feedback{
		{Verdict: VerdictCorrect, Prediction: PredictionRecord{Level: LevelTaxID}, Mode: "quiz"},
		{Verdict: VerdictCorrect, Prediction: PredictionRecord{Level: LevelTaxID}, Mode: "quiz"},
		{Verdict: VerdictWrong, Prediction: PredictionRecord{Level: LevelFormat}, Mode: "ask"},
		{Verdict: VerdictJournalWrong, Prediction: PredictionRecord{Level: LevelTaxID}, Mode: "quiz", DocNo: "PV9"},
		{Verdict: VerdictShouldKnow, Prediction: PredictionRecord{Level: levelNoMatch}, Mode: "quiz"},
	})
	if s.Overall.Judged != 3 || s.Overall.Correct != 2 || s.Overall.Rate != 0.6667 {
		t.Errorf("overall %+v", s.Overall)
	}
	if s.ByLevel[LevelTaxID].Rate != 1 || s.ByLevel[LevelFormat].Rate != 0 || s.ByLevel[levelNoMatch].ShouldKnow != 1 {
		t.Errorf("by level %+v %+v", s.ByLevel[LevelTaxID], s.ByLevel[LevelFormat])
	}
	if len(s.JournalWrong) != 1 || s.JournalWrong[0].DocNo != "PV9" || s.Total != 5 {
		t.Errorf("journal wrong list %+v", s.JournalWrong)
	}
}

// ---------- handlers ----------

type memStore struct {
	mu   sync.Mutex
	list []Feedback
	down bool
}

func (m *memStore) Save(_ context.Context, f Feedback) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return ErrStoreUnavailable
	}
	m.list = append(m.list, f)
	return nil
}

func (m *memStore) List(_ context.Context, shopID string) ([]Feedback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return nil, ErrStoreUnavailable
	}
	var out []Feedback
	for _, f := range m.list {
		if f.ShopID == shopID {
			out = append(out, f)
		}
	}
	return out, nil
}

func testTabRouter(t *testing.T, store FeedbackStore) (*gin.Engine, func()) {
	t.Helper()
	var calls int32
	srv := fakeMainAPI(t, &calls)
	m, js, g := fixture()
	brain := Build(m, js, g, 0, DefaultOptions(day(10)))
	svc := NewService(NewOwnerVerifier(srv.URL))
	svc.Load = func(ctx context.Context, shopID string) (*Brain, error) { return brain, nil }
	h := &Handlers{Svc: svc, Store: store}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/quiz", h.GetQuiz)
	r.GET("/parties", h.SearchParties)
	r.POST("/ask", h.AskBrain)
	r.POST("/feedback", h.PostFeedback)
	r.GET("/summary", h.GetFeedbackSummary)
	return r, srv.Close
}

func send(r *gin.Engine, method, url, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, url, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTestTabRoutes(t *testing.T) {
	store := &memStore{}
	r, done := testTabRouter(t, store)
	defer done()

	for _, u := range []string{"/quiz?shopid=OWNED", "/parties?shopid=OWNED&q=C1", "/summary?shopid=OWNED"} {
		if w := send(r, "GET", u, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without token = %d", u, w.Code)
		}
		if w := send(r, "GET", u, "good-token", nil); w.Code != http.StatusOK {
			t.Errorf("%s = %d %s", u, w.Code, w.Body.String())
		}
	}
	if w := send(r, "GET", "/quiz?shopid=NOTMINE", "good-token", nil); w.Code != http.StatusForbidden {
		t.Errorf("not owner = %d", w.Code)
	}

	// The stored prediction comes from the server, whatever the client claims.
	w := send(r, "POST", "/feedback?shopid=OWNED", "good-token", map[string]any{"mode": "quiz", "journal_guid": "j2", "verdict": VerdictCorrect, "note": "ok", "prediction": map[string]any{"level": "L4_format"}})
	if w.Code != http.StatusOK {
		t.Fatalf("feedback = %d %s", w.Code, w.Body.String())
	}
	if len(store.list) != 1 || store.list[0].Prediction.Level != LevelTaxID || store.list[0].DocNo != "PVj2" || store.list[0].ShopID != "OWNED" || store.list[0].Actual == "" {
		t.Errorf("stored %+v", store.list)
	}
	// a judged journal is not asked again
	for i := 0; i < 20; i++ {
		var resp struct {
			Data              *QuizItem `json:"data"`
			FeedbackAvailable bool      `json:"feedback_available"`
		}
		_ = json.Unmarshal(send(r, "GET", "/quiz?shopid=OWNED&days=3650", "good-token", nil).Body.Bytes(), &resp)
		if !resp.FeedbackAvailable || (resp.Data != nil && resp.Data.Journal.GuidFixed == "j2") {
			t.Fatalf("judged journal returned again or store not reported: %+v", resp)
		}
	}

	w = send(r, "POST", "/ask?shopid=OWNED", "good-token", AskInput{PartyCode: "C1", DocFormat: "PV", Amount: 214})
	if w.Code != http.StatusOK {
		t.Errorf("ask = %d %s", w.Code, w.Body.String())
	}
	if w = send(r, "POST", "/ask?shopid=OWNED", "good-token", AskInput{}); w.Code != http.StatusBadRequest {
		t.Errorf("ask with nothing to look up = %d", w.Code)
	}
	// shops that never set a document format can still ask by party
	if w = send(r, "POST", "/ask?shopid=OWNED", "good-token", AskInput{PartyCode: "C1"}); w.Code != http.StatusOK {
		t.Errorf("ask by party only = %d %s", w.Code, w.Body.String())
	}
	if w = send(r, "POST", "/feedback?shopid=OWNED", "good-token", map[string]any{"mode": "ask", "verdict": VerdictWrong, "input": AskInput{PartyCode: "C1", DocFormat: "PV"}, "correct_accounts": "52100D"}); w.Code != http.StatusOK {
		t.Errorf("ask feedback = %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []map[string]any{
		{"mode": "quiz", "journal_guid": "j2", "verdict": "maybe"},
		{"mode": "quiz", "journal_guid": "nope", "verdict": VerdictCorrect},
		{"mode": "other", "verdict": VerdictCorrect},
	} {
		if w = send(r, "POST", "/feedback?shopid=OWNED", "good-token", bad); w.Code != http.StatusBadRequest {
			t.Errorf("bad feedback %v = %d", bad, w.Code)
		}
	}

	var sum struct {
		Data FeedbackSummary `json:"data"`
	}
	_ = json.Unmarshal(send(r, "GET", "/summary?shopid=OWNED", "good-token", nil).Body.Bytes(), &sum)
	if sum.Data.Total != 2 || sum.Data.Overall.Judged != 2 || sum.Data.Overall.Correct != 1 {
		t.Errorf("summary %+v", sum.Data)
	}
}

func TestStoreDownOnlyBlocksSaving(t *testing.T) {
	for _, store := range []FeedbackStore{nil, &memStore{down: true}} {
		r, done := testTabRouter(t, store)
		w := send(r, "GET", "/quiz?shopid=OWNED&days=3650", "good-token", nil)
		if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"feedback_available":false`)) {
			t.Errorf("quiz must work without the store: %d %s", w.Code, w.Body.String())
		}
		if w = send(r, "POST", "/feedback?shopid=OWNED", "good-token", map[string]any{"mode": "quiz", "journal_guid": "j2", "verdict": VerdictCorrect}); w.Code != http.StatusServiceUnavailable {
			t.Errorf("feedback without store = %d", w.Code)
		}
		if w = send(r, "GET", "/summary?shopid=OWNED", "good-token", nil); w.Code != http.StatusServiceUnavailable {
			t.Errorf("summary without store = %d", w.Code)
		}
		done()
	}
}

func TestMongoFeedbackWithoutURIIsUnavailable(t *testing.T) {
	var m *MongoFeedback
	if err := m.Save(context.Background(), Feedback{}); err != ErrStoreUnavailable {
		t.Errorf("nil store: %v", err)
	}
	if _, err := NewMongoFeedback("", "").List(context.Background(), "x"); err != ErrStoreUnavailable {
		t.Errorf("empty uri: %v", err)
	}
}

func TestAskAmountsExactForVAT7(t *testing.T) {
	b := build(t)
	a := b.Ask(AskInput{PartyCode: "C1", DocFormat: "PV", Amount: 1070})
	want := map[string]float64{"51212": 1000, "11541": 70, "11111": 1070}
	for _, l := range a.Shape.Lines {
		if l.Amount != want[l.AccountCode] {
			t.Errorf("%s = %.2f, want %.2f", l.AccountCode, l.Amount, want[l.AccountCode])
		}
	}
	if a.Shape.Lines[0].Side != "D" || a.Shape.Lines[len(a.Shape.Lines)-1].Side != "C" {
		t.Errorf("debits first: %+v", a.Shape.Lines)
	}
}

func TestQuizReviewOnlySkipsMatchesAndCountsThem(t *testing.T) {
	b := build(t)
	_, all := b.QuizWithStats(QuizOptions{Days: 3650})
	if all.Window == 0 || all.AutoMatched == 0 || all.AutoMatched+all.Review != all.Window {
		t.Fatalf("stats %+v", all)
	}
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 30; i++ {
		q, st := b.QuizWithStats(QuizOptions{Days: 3650, ReviewOnly: true, Rand: r})
		if q == nil {
			t.Fatalf("review pool empty: %+v", st)
		}
		if q.Guess.Known && q.MatchAccount {
			t.Fatalf("review-only returned a matching guess: %+v", q.Journal.GuidFixed)
		}
		if st.Pool != st.Review {
			t.Fatalf("nothing judged yet, pool should equal review: %+v", st)
		}
	}
}

func TestBothOKCountsAsCorrect(t *testing.T) {
	s := Summarize([]Feedback{{Verdict: VerdictBothOK, Prediction: PredictionRecord{Level: LevelTaxID}}, {Verdict: VerdictWrong, Prediction: PredictionRecord{Level: LevelTaxID}}})
	if s.Overall.Judged != 2 || s.Overall.Correct != 1 || s.Overall.BothOK != 1 {
		t.Errorf("%+v", s.Overall)
	}
}
