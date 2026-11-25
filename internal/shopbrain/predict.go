package shopbrain

// predictor is the ladder guess shared by the backtest and the quiz: it
// learns journals one at a time (oldest first) and predicts a journal's
// booking from what it has learned so far. Counts are unweighted so a run is
// reproducible.
type predictor struct {
	opts   Options
	seen   map[string]*tally
	shapes map[string]Shape // full key → shape, to show VAT/WHT of a guess
}

type tally struct {
	accounts map[string]int
	full     map[string]int
	n        int
	recent   []string // newest last, at most keepRecent
}

func newPredictor(opts Options) *predictor {
	return &predictor{opts: opts, seen: map[string]*tally{}, shapes: map[string]Shape{}}
}

func topKey(m map[string]int) string {
	best, n := "", -1
	for k, c := range m {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	return best
}

// Skip is a ladder level that had history but was not consistent enough to
// answer from; WouldBe is what it would have guessed with a lower bar.
type Skip struct {
	Level       Level   `json:"level"`
	Support     int     `json:"support"`
	Consistency float64 `json:"consistency"`
	WouldBe     string  `json:"would_be"` // full key
	Reason      string  `json:"reason"`   // not_consistent | not_recent
}

// Guess is one prediction. Level is levelNoMatch ("none") when the brain
// answers "unknown".
type Guess struct {
	Level       Level   `json:"level"`
	Key         string  `json:"key,omitempty"`
	Accounts    string  `json:"accounts,omitempty"`
	Full        string  `json:"full,omitempty"`
	Support     int     `json:"support,omitempty"`     // earlier journals under the key
	Consistency float64 `json:"consistency,omitempty"` // share of the leading shape
	Skipped     []Skip  `json:"skipped,omitempty"`
}

func (g Guess) Known() bool { return g.Level != levelNoMatch }

func (p *predictor) predict(keys []ladderKey) Guess {
	g := Guess{Level: levelNoMatch}
	for _, k := range keys {
		t := p.seen[k.key]
		if t == nil || t.n < minSupport(k.level) {
			continue
		}
		lead := topKey(t.full)
		share := float64(t.full[lead]) / float64(t.n)
		if p.opts.confidence(t.full[lead], t.n) < p.opts.minConsistency(k.level) {
			g.Skipped = append(g.Skipped, Skip{Level: k.level, Support: t.n, Consistency: round4(share), WouldBe: lead, Reason: "not_consistent"})
			continue
		}
		if !p.opts.recentAgrees(t.recent, lead) {
			g.Skipped = append(g.Skipped, Skip{Level: k.level, Support: t.n, Consistency: round4(share), WouldBe: lead, Reason: "not_recent"})
			continue
		}
		g.Level, g.Key, g.Accounts, g.Full = k.level, k.key, topKey(t.accounts), lead
		g.Support, g.Consistency = t.n, round4(share)
		return g
	}
	return g
}

func (p *predictor) learn(e entry) {
	full := e.shape.fullKey()
	p.shapes[full] = e.shape
	for _, k := range e.keys {
		t := p.seen[k.key]
		if t == nil {
			t = &tally{accounts: map[string]int{}, full: map[string]int{}}
			p.seen[k.key] = t
		}
		t.accounts[e.shape.Accounts]++
		t.full[full]++
		t.n++
		t.recent = append(t.recent, full)
		if len(t.recent) > keepRecent {
			t.recent = t.recent[1:]
		}
	}
}
