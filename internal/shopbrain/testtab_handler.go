package shopbrain

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// Routes for the "test the brain" tab. All go through authorize (owner only).

func (h *Handlers) feedbackUnavailable(c *gin.Context, err error) {
	if !errors.Is(err, ErrStoreUnavailable) {
		log.Printf("[shop-brain] feedback store: %v", err)
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "feedback_unavailable", "message": "ยังบันทึกผลทดสอบไม่ได้ (ที่เก็บผลไม่พร้อม)"})
}

// judged returns the journals the owner already judged; ok=false when the store is down.
func (h *Handlers) judged(c *gin.Context, shopID string) (map[string]bool, bool) {
	out := map[string]bool{}
	if h.Store == nil {
		return out, false
	}
	list, err := h.Store.List(c.Request.Context(), shopID)
	if err != nil {
		return out, false
	}
	for _, f := range list {
		if f.JournalGUID != "" {
			out[f.JournalGUID] = true
		}
	}
	return out, true
}

// GetQuiz handles GET /shop-brain/quiz?shopid=&days=180&level=&statement=1&review=1
func (h *Handlers) GetQuiz(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok {
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "180"))
	if days <= 0 || days > 3650 {
		days = 180
	}
	exclude, stored := h.judged(c, shopID)
	item, st := b.QuizWithStats(QuizOptions{Days: days, Level: Level(c.Query("level")), IncludeStatement: c.Query("statement") == "1", Exclude: exclude, ReviewOnly: c.Query("review") == "1"})
	if item == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": nil, "stats": st, "feedback_available": stored, "message": "ไม่มีรายการให้ตรวจในช่วงนี้แล้ว"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item, "stats": st, "feedback_available": stored})
}

// SearchParties handles GET /shop-brain/parties?shopid=&q=
func (h *Handlers) SearchParties(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok {
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	q := c.Query("q")
	if utf8.RuneCountInString(q) > 100 {
		q = string([]rune(q)[:100])
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": MatchParties(b.Master, q, 8)})
}

// AskBrain handles POST /shop-brain/ask?shopid=
func (h *Handlers) AskBrain(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok {
		return
	}
	var in AskInput
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.DocFormat) == "" || in.Amount < 0 || utf8.RuneCountInString(in.Description) > 300 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid_input", "message": "ต้องระบุประเภทเอกสาร และยอดเงินต้องไม่ติดลบ"})
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": b.Ask(in)})
}

type feedbackRequest struct {
	Mode            string    `json:"mode"`
	JournalGUID     string    `json:"journal_guid"`
	Input           *AskInput `json:"input"`
	Verdict         string    `json:"verdict"`
	CorrectAccounts string    `json:"correct_accounts"`
	Note            string    `json:"note"`
}

// PostFeedback handles POST /shop-brain/feedback?shopid=. The prediction is
// recomputed on the server (not taken from the request) so what is stored is
// what the brain really said.
func (h *Handlers) PostFeedback(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok {
		return
	}
	var r feedbackRequest
	if err := c.ShouldBindJSON(&r); err != nil || !validVerdicts[r.Verdict] || utf8.RuneCountInString(r.Note) > 500 || utf8.RuneCountInString(r.CorrectAccounts) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid_input"})
		return
	}
	if h.Store == nil {
		h.feedbackUnavailable(c, ErrStoreUnavailable)
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	f := Feedback{
		ShopID: shopID, Mode: r.Mode, Verdict: r.Verdict, CorrectAccounts: strings.TrimSpace(r.CorrectAccounts), Note: strings.TrimSpace(r.Note),
		BrainVersion: b.Version, MinConsistency: b.Options.MinConsistency, MinConsistencyFormat: b.Options.MinConsistencyFormat, CreatedAt: time.Now().UTC(),
	}
	switch r.Mode {
	case "quiz":
		e, g, found := b.entryByGuid(r.JournalGUID)
		if !found {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "journal_not_found"})
			return
		}
		f.JournalGUID, f.DocNo, f.Source = e.j.GuidFixed, e.j.DocNo, e.src
		f.Prediction = predictionRecord(g)
		f.Actual = e.shape.fullKey()
		f.AutoMatchAccounts = g.Known() && g.Accounts == e.shape.Accounts
	case "ask":
		if r.Input == nil || strings.TrimSpace(r.Input.DocFormat) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid_input"})
			return
		}
		a := b.Ask(*r.Input)
		f.Input = r.Input
		f.Prediction = PredictionRecord{Known: a.Known, Level: a.Level, PatternID: a.PatternID, Support: a.Support, Consistency: a.Consistency}
		if a.Shape != nil {
			f.Prediction.Accounts = a.Shape.Accounts
		}
		if !a.Known && len(a.Skipped) > 0 {
			f.Prediction.WouldBe, f.Prediction.WouldBeLevel, f.Prediction.WouldBeCons = a.Skipped[0].WouldBe.Accounts, a.Skipped[0].Level, a.Skipped[0].Consistency
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "mode must be quiz or ask"})
		return
	}
	if err := h.Store.Save(c.Request.Context(), f); err != nil {
		h.feedbackUnavailable(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func predictionRecord(g Guess) PredictionRecord {
	p := PredictionRecord{Known: g.Known(), Level: g.Level, Accounts: g.Accounts, Full: g.Full, Support: g.Support, Consistency: g.Consistency}
	if g.Known() {
		p.PatternID = patternID(g.Key)
	} else if len(g.Skipped) > 0 {
		p.WouldBe, p.WouldBeLevel, p.WouldBeCons = g.Skipped[0].WouldBe, g.Skipped[0].Level, g.Skipped[0].Consistency
	}
	return p
}

// GetFeedbackSummary handles GET /shop-brain/feedback/summary?shopid=
func (h *Handlers) GetFeedbackSummary(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok {
		return
	}
	if h.Store == nil {
		h.feedbackUnavailable(c, ErrStoreUnavailable)
		return
	}
	list, err := h.Store.List(c.Request.Context(), shopID)
	if err != nil {
		h.feedbackUnavailable(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": Summarize(list)})
}
