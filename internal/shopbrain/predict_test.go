package shopbrain

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// legacyLadder is the backtest loop as it was before predictor was split out
// (commit 1556ec2), kept only to prove the refactor changed no numbers.
func legacyLadder(entries []entry, opts Options) (map[Level]int, map[Level]int, int, int, int, map[Level]int) {
	type tl struct {
		accounts map[string]int
		full     map[string]int
		n        int
	}
	seen := map[string]*tl{}
	top := func(m map[string]int) string {
		best, n := "", -1
		for k, c := range m {
			if c > n || (c == n && k < best) {
				best, n = k, c
			}
		}
		return best
	}
	used := map[Level]int{}
	hitAcc := map[Level]int{}
	skipped := map[Level]int{}
	predicted, hitA, hitF := 0, 0, 0
	for _, e := range entries {
		full := e.shape.fullKey()
		var lv Level = levelNoMatch
		var gA, gF string
		for _, k := range e.keys {
			t := seen[k.key]
			if t == nil || t.n < minSupport(k.level) {
				continue
			}
			if share := float64(t.full[top(t.full)]) / float64(t.n); share < opts.minConsistency(k.level) {
				skipped[k.level]++
				continue
			}
			lv, gA, gF = k.level, top(t.accounts), top(t.full)
			break
		}
		if lv != levelNoMatch {
			predicted++
			used[lv]++
			if gA == e.shape.Accounts {
				hitA++
				hitAcc[lv]++
			}
			if gF == full {
				hitF++
			}
		}
		for _, k := range e.keys {
			t := seen[k.key]
			if t == nil {
				t = &tl{accounts: map[string]int{}, full: map[string]int{}}
				seen[k.key] = t
			}
			t.accounts[e.shape.Accounts]++
			t.full[full]++
			t.n++
		}
	}
	return used, hitAcc, predicted, hitA, hitF, skipped
}

func randomJournals(r *rand.Rand, n int) []Journal {
	parties := []*rawParty{creditor("C1", "0105500000001"), creditor("C2", "0105500000002"), creditor("C3", ""), nil}
	expenses := []string{"51212", "53000", "52100"}
	descs := []string{"ค่าขนส่ง", "ค่าไฟฟ้า", "ซื้อวัสดุ", ""}
	out := make([]Journal, n)
	for i := range out {
		j := purchase(fmt.Sprintf("r%d", i), i, parties[r.Intn(len(parties))], expenses[r.Intn(len(expenses))], float64(100+r.Intn(900)))
		j.AccountDescription = descs[r.Intn(len(descs))]
		j.DocFormat = []string{"PV", "JV"}[r.Intn(2)]
		if r.Intn(4) == 0 {
			j.Vats = nil
		}
		out[i] = j
	}
	return out
}

func TestPredictorMatchesLegacyBacktest(t *testing.T) {
	var anyPredicted, anySkipped int
	for seed := int64(1); seed <= 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		entries := prepare(randomJournals(r, 60+r.Intn(200)), nil)
		opts := mechanicsOptions(day(400))
		if seed%3 == 0 {
			opts.MinConsistency, opts.MinConsistencyFormat = 0.5, 0.8
		}
		bt := runBacktest(entries, opts)
		used, hitAcc, predicted, hitA, hitF, skipped := legacyLadder(entries, opts)
		if bt.Overall.Predicted != predicted || bt.Overall.HitAccounts != hitA || bt.Overall.HitFull != hitF {
			t.Fatalf("seed %d: overall %+v vs legacy predicted=%d hitA=%d hitF=%d", seed, bt.Overall, predicted, hitA, hitF)
		}
		for _, l := range allLevels {
			if bt.ByLevel[l].Predicted != used[l] || bt.ByLevel[l].HitAccounts != hitAcc[l] {
				t.Fatalf("seed %d level %s: %+v vs legacy used=%d hit=%d", seed, l, bt.ByLevel[l], used[l], hitAcc[l])
			}
		}
		got := map[Level]int{}
		for l, n := range bt.Skipped {
			if n > 0 {
				got[l] = n
			}
		}
		anyPredicted += predicted
		for _, n := range skipped {
			anySkipped += n
		}
		if !reflect.DeepEqual(got, skipped) {
			t.Fatalf("seed %d skipped %v vs legacy %v", seed, got, skipped)
		}
	}
	if anyPredicted == 0 || anySkipped == 0 {
		t.Fatalf("fixture too easy: predicted=%d skipped=%d", anyPredicted, anySkipped)
	}
}
