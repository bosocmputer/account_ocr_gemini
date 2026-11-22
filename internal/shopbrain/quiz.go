package shopbrain

import (
	"math/rand"
	"sort"
	"strings"
	"time"
)

// ---------- shared views for the "test the brain" tab ----------

type LineView struct {
	AccountCode string  `json:"accountcode"`
	AccountName string  `json:"accountname"`
	Side        string  `json:"side"` // D | C
	Amount      float64 `json:"amount,omitempty"`
}

type ShapeView struct {
	Accounts string     `json:"accounts"`
	Lines    []LineView `json:"lines"`
	VAT      *VatInfo   `json:"vat,omitempty"`
	WHT      *WhtInfo   `json:"wht,omitempty"`
}

type SkipView struct {
	Level       Level     `json:"level"`
	Support     int       `json:"support"`
	Consistency float64   `json:"consistency"`
	WouldBe     ShapeView `json:"would_be"`
}

// GuessView is what the page shows for a guess: Known=false means the brain
// answered "unknown"; Skipped lists the levels that had history but were not
// consistent enough (what they would have said, for calibrating the bar).
type GuessView struct {
	Known       bool       `json:"known"`
	Level       Level      `json:"level"`
	PatternID   string     `json:"pattern_id,omitempty"`
	Support     int        `json:"support,omitempty"`
	Consistency float64    `json:"consistency,omitempty"`
	Shape       *ShapeView `json:"shape,omitempty"`
	Skipped     []SkipView `json:"skipped,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

func (b *Brain) accountName(code string) string {
	if a, ok := b.chart[code]; ok && a.Name != "" {
		return a.Name
	}
	for _, u := range b.Usage.Accounts {
		if u.Code == code {
			return u.Name
		}
	}
	return ""
}

// shapeView expands a full key ("11111C,51212D|vat|wht") into named lines.
func (b *Brain) shapeView(full string) ShapeView {
	s, ok := b.shapes[full]
	if !ok {
		s = Shape{Accounts: strings.SplitN(full, "|", 2)[0]}
	}
	v := ShapeView{Accounts: s.Accounts, VAT: s.VAT, WHT: s.WHT}
	for _, k := range strings.Split(s.Accounts, ",") {
		if len(k) < 2 {
			continue
		}
		code, side := k[:len(k)-1], k[len(k)-1:]
		v.Lines = append(v.Lines, LineView{AccountCode: code, AccountName: b.accountName(code), Side: side})
	}
	debitsFirst(v.Lines)
	return v
}

// debitsFirst orders lines Dr before Cr (stable within a side).
func debitsFirst(lines []LineView) {
	sort.SliceStable(lines, func(a, b int) bool { return lines[a].Side == "D" && lines[b].Side != "D" })
}

func (b *Brain) guessView(g Guess) GuessView {
	v := GuessView{Known: g.Known(), Level: g.Level, Support: g.Support, Consistency: g.Consistency}
	if g.Known() {
		sv := b.shapeView(g.Full)
		v.Shape = &sv
		v.PatternID = patternID(g.Key)
	} else if len(g.Skipped) > 0 {
		v.Reason = "เคยเห็นแต่ลงบัญชีไม่สม่ำเสมอพอ จึงตอบว่าไม่รู้"
	} else {
		v.Reason = "ยังไม่เคยเห็นรายการแบบนี้มาก่อน"
	}
	for _, s := range g.Skipped {
		v.Skipped = append(v.Skipped, SkipView{Level: s.Level, Support: s.Support, Consistency: s.Consistency, WouldBe: b.shapeView(s.WouldBe)})
	}
	return v
}

// ---------- quiz ----------

type QuizJournal struct {
	GuidFixed   string    `json:"guidfixed"`
	DocNo       string    `json:"docno"`
	DocDate     time.Time `json:"docdate"`
	Description string    `json:"description"`
	DocFormat   string    `json:"docformat"`
	BookCode    string    `json:"bookcode"`
	Source      Source    `json:"source"`
	PartyKind   string    `json:"party_kind,omitempty"`
	PartyCode   string    `json:"party_code,omitempty"`
	PartyName   string    `json:"party_name,omitempty"`
	TaxID       string    `json:"taxid,omitempty"`
	Total       float64   `json:"total"`
	Images      []string  `json:"images,omitempty"`
}

type QuizItem struct {
	Journal      QuizJournal `json:"journal"`
	Guess        GuessView   `json:"guess"`
	Actual       ShapeView   `json:"actual"`
	MatchAccount bool        `json:"match_accounts"` // automatic comparison, before the owner judges
	MatchFull    bool        `json:"match_full"`
	Pool         int         `json:"pool"` // journals still left to judge under the same filter
}

// QuizStats counts the journals in the quiz window (after the statement filter).
type QuizStats struct {
	Window      int `json:"window"`       // journals in the window
	AutoMatched int `json:"auto_matched"` // brain guessed the same accounts → counted as right without asking
	Review      int `json:"review"`       // mismatched or unknown → worth asking the owner (judged ones included)
	Pool        int `json:"pool"`         // left to ask under the current filter
}

type QuizOptions struct {
	Days             int
	IncludeStatement bool
	Level            Level // "" = any; "none" = journals the brain could not answer
	Exclude          map[string]bool
	Rand             *rand.Rand
	// ReviewOnly asks only where the owner's answer adds something: the brain
	// guessed different accounts from the saved journal, or could not answer.
	// Matching guesses are counted in QuizStats.AutoMatched instead.
	ReviewOnly bool
}

// Quiz picks one recent journal the owner has not judged yet and returns what
// the brain guessed for it from the journals saved before it.
func (b *Brain) Quiz(o QuizOptions) (*QuizItem, bool) {
	item, st := b.QuizWithStats(o)
	return item, item != nil && st.Pool > 0
}

// QuizWithStats is Quiz plus the counts the "check the brain" page shows.
func (b *Brain) QuizWithStats(o QuizOptions) (*QuizItem, QuizStats) {
	var st QuizStats
	if o.Days <= 0 {
		o.Days = 180
	}
	if o.Rand == nil {
		o.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	since := b.Options.AsOf.AddDate(0, 0, -o.Days)
	var all, atLevel []int
	for i, e := range b.entries {
		if e.when.Before(since) || (e.src == SourceStatement && !o.IncludeStatement) {
			continue
		}
		st.Window++
		g := b.guesses[i]
		matched := g.Known() && g.Accounts == e.shape.Accounts
		if matched {
			st.AutoMatched++
		} else {
			st.Review++
		}
		if o.Exclude[e.j.GuidFixed] || (o.ReviewOnly && matched) {
			continue
		}
		all = append(all, i)
		if o.Level != "" && b.guesses[i].Level == o.Level {
			atLevel = append(atLevel, i)
		}
	}
	pool := all
	if o.Level != "" && len(atLevel) > 0 {
		pool = atLevel // fall back to any level when the shop has none at the asked level
	}
	st.Pool = len(all)
	if len(pool) == 0 {
		return nil, st
	}
	i := pool[o.Rand.Intn(len(pool))]
	e, g := b.entries[i], b.guesses[i]
	return &QuizItem{
		Journal:      b.quizJournal(e),
		Guess:        b.guessView(g),
		Actual:       actualView(b, e),
		MatchAccount: g.Known() && g.Accounts == e.shape.Accounts,
		MatchFull:    g.Known() && g.Full == e.shape.fullKey(),
		Pool:         len(all),
	}, st
}

func (b *Brain) quizJournal(e entry) QuizJournal {
	q := QuizJournal{
		GuidFixed: e.j.GuidFixed, DocNo: e.j.DocNo, DocDate: e.j.DocDate, Description: e.j.AccountDescription,
		DocFormat: e.j.DocFormat, BookCode: e.j.BookCode, Source: e.src,
		PartyKind: e.party.Kind, PartyCode: e.party.Code, PartyName: e.party.Name, TaxID: e.party.TaxID, Total: round4(e.total),
	}
	if e.group != nil {
		for _, im := range e.group.ImageReferences {
			if im.ImageURI != "" {
				q.Images = append(q.Images, im.ImageURI)
			}
		}
	}
	return q
}

func actualView(b *Brain, e entry) ShapeView {
	v := ShapeView{Accounts: e.shape.Accounts, VAT: e.shape.VAT, WHT: e.shape.WHT}
	for _, l := range e.lines {
		name := b.accountName(l.code)
		if name == "" {
			name = l.name
		}
		v.Lines = append(v.Lines, LineView{AccountCode: l.code, AccountName: name, Side: l.side, Amount: round4(l.amount)})
	}
	debitsFirst(v.Lines)
	return v
}

// entryByGuid finds a learned journal and the brain's guess for it.
func (b *Brain) entryByGuid(guid string) (entry, Guess, bool) {
	for i, e := range b.entries {
		if e.j.GuidFixed == guid {
			return e, b.guesses[i], true
		}
	}
	return entry{}, Guess{}, false
}
