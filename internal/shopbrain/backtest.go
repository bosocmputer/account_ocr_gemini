package shopbrain

// runBacktest walks journals oldest first and, for each one, predicts its
// booking from the journals saved before it, then learns it. Counts are
// unweighted so the result is reproducible and comparable across runs.
func runBacktest(entries []entry, opts Options) Backtest {
	bt := Backtest{ByLevel: map[Level]*HitStat{}, BySource: map[Source]*HitStat{}, Skipped: map[Level]int{}, MinConsistency: opts.MinConsistency, MinConsistencyFormat: opts.MinConsistencyFormat}
	for _, l := range allLevels {
		bt.ByLevel[l] = &HitStat{}
	}
	for _, s := range allSources {
		bt.BySource[s] = &HitStat{}
	}

	type tally struct {
		accounts map[string]int
		full     map[string]int
		n        int
	}
	seen := map[string]*tally{}
	top := func(m map[string]int) string {
		best, n := "", -1
		for k, c := range m {
			if c > n || (c == n && k < best) {
				best, n = k, c
			}
		}
		return best
	}
	baseline := map[string]*tally{}

	for _, e := range entries {
		full := e.shape.fullKey()
		bt.Overall.Total++
		bs := bt.BySource[e.src]
		bs.Total++

		// Ladder prediction: first level whose key has enough earlier support
		// and a consistent enough leading shape; otherwise no guess.
		var used Level = levelNoMatch
		var guessAcc, guessFull string
		for _, k := range e.keys {
			t := seen[k.key]
			if t == nil || t.n < minSupport(k.level) {
				continue
			}
			if share := float64(t.full[top(t.full)]) / float64(t.n); share < opts.minConsistency(k.level) {
				bt.Skipped[k.level]++
				continue
			}
			used, guessAcc, guessFull = k.level, top(t.accounts), top(t.full)
			break
		}
		if used != levelNoMatch {
			bl := bt.ByLevel[used]
			for _, h := range []*HitStat{&bt.Overall, bs, bl} {
				h.Predicted++
				if guessAcc == e.shape.Accounts {
					h.HitAccounts++
				}
				if guessFull == full {
					h.HitFull++
				}
			}
			bl.Total++
		}
		for _, k := range e.keys {
			t := seen[k.key]
			if t == nil {
				t = &tally{accounts: map[string]int{}, full: map[string]int{}}
				seen[k.key] = t
			}
			t.accounts[e.shape.Accounts]++
			t.full[full]++
			t.n++
		}

		// Baseline = the earlier mongosh simulation: party code + docformat, any prior occurrence.
		if e.src != SourceStatement && e.party.Code != "" {
			k := e.party.Code + "#" + e.j.DocFormat
			b := &bt.BaselinePartyDocformat
			b.Total++
			if t := baseline[k]; t != nil {
				b.Predicted++
				if top(t.accounts) == e.shape.Accounts {
					b.HitAccounts++
				}
				if top(t.full) == full {
					b.HitFull++
				}
			} else {
				t = &tally{accounts: map[string]int{}, full: map[string]int{}}
				baseline[k] = t
			}
			baseline[k].accounts[e.shape.Accounts]++
			baseline[k].full[full]++
			baseline[k].n++
		}
	}

	finalize := func(h *HitStat) {
		if h.Predicted > 0 {
			h.RateAcc = round4(float64(h.HitAccounts) / float64(h.Predicted))
			h.RateFull = round4(float64(h.HitFull) / float64(h.Predicted))
		}
		if h.Total > 0 {
			h.Coverage = round4(float64(h.Predicted) / float64(h.Total))
		}
	}
	finalize(&bt.Overall)
	finalize(&bt.BaselinePartyDocformat)
	for _, h := range bt.ByLevel {
		finalize(h)
	}
	for _, h := range bt.BySource {
		finalize(h)
	}
	return bt
}
