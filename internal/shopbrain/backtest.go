package shopbrain

// runBacktest walks journals oldest first and, for each one, predicts its
// booking from the journals saved before it, then learns it. Counts are
// unweighted so the result is reproducible and comparable across runs.
func runBacktest(entries []entry, opts Options) Backtest {
	bt, _, _ := replay(entries, opts)
	return bt
}

// replay is runBacktest that also returns each entry's guess (aligned with
// entries) and the shapes seen, for the quiz.
func replay(entries []entry, opts Options) (Backtest, []Guess, map[string]Shape) {
	guesses := make([]Guess, len(entries))
	bt := Backtest{ByLevel: map[Level]*HitStat{}, BySource: map[Source]*HitStat{}, Skipped: map[Level]int{}, MinConsistency: opts.MinConsistency, MinConsistencyFormat: opts.MinConsistencyFormat}
	for _, l := range allLevels {
		bt.ByLevel[l] = &HitStat{}
	}
	for _, s := range allSources {
		bt.BySource[s] = &HitStat{}
	}

	top := topKey
	pr := newPredictor(opts)
	baseline := map[string]*tally{}

	for i, e := range entries {
		full := e.shape.fullKey()
		bt.Overall.Total++
		bs := bt.BySource[e.src]
		bs.Total++

		// Ladder prediction: first level whose key has enough earlier support
		// and a consistent enough leading shape; otherwise no guess.
		g := pr.predict(e.keys)
		guesses[i] = g
		for _, s := range g.Skipped {
			bt.Skipped[s.Level]++
		}
		if g.Known() {
			bl := bt.ByLevel[g.Level]
			for _, h := range []*HitStat{&bt.Overall, bs, bl} {
				h.Predicted++
				if g.Accounts == e.shape.Accounts {
					h.HitAccounts++
				}
				if g.Full == full {
					h.HitFull++
				}
			}
			bl.Total++
		}
		pr.learn(e)

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
	return bt, guesses, pr.shapes
}
