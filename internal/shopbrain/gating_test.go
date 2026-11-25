package shopbrain

import (
	"testing"
	"time"
)

// mechanicsOptions keeps the ladder's original bars (raw share, no recent
// check, no statement direction) so the small fixtures can exercise the
// ladder mechanics with a handful of journals. The gates themselves are
// tested below with DefaultOptions.
func mechanicsOptions(now time.Time) Options {
	o := DefaultOptions(now)
	o.WilsonZ, o.RecentAgree, o.StatementDirection = 0, 0, false
	return o
}

func descEntry(shape string, at int) entry {
	return entry{keys: []ladderKey{{LevelDesc, "desc:x|"}}, shape: Shape{Accounts: shape}, when: day(at)}
}

func guessAfter(opts Options, shapes ...string) Guess {
	p := newPredictor(opts)
	for i, s := range shapes {
		p.learn(descEntry(s, i))
	}
	return p.predict([]ladderKey{{LevelDesc, "desc:x|"}})
}

func TestDefaultGatesNeedMoreThanTwoExamples(t *testing.T) {
	opts := DefaultOptions(day(100))
	if g := guessAfter(opts, "A", "A"); g.Known() {
		t.Errorf("2/2 has a Wilson lower bound ≈ 0.55 < 0.6 and must stay unknown: %+v", g)
	}
	if g := guessAfter(opts, "A", "A", "A"); !g.Known() || g.Full != (Shape{Accounts: "A"}).fullKey() {
		t.Errorf("3/3 (lower bound ≈ 0.65) should be answered: %+v", g)
	}
	if g := guessAfter(mechanicsOptions(day(100)), "A", "A"); !g.Known() {
		t.Errorf("without the gates 2/2 is answered: %+v", g)
	}
}

func TestDefaultGatesSkipAKeyWhoseLatestBookingChanged(t *testing.T) {
	opts := DefaultOptions(day(100))
	// 8 × A then B: A still leads (lower bound ≈ 0.69) but the last two disagree.
	g := guessAfter(opts, "A", "A", "A", "A", "A", "A", "A", "A", "B")
	if g.Known() || len(g.Skipped) != 1 || g.Skipped[0].Reason != "not_recent" {
		t.Fatalf("a changed booking must not be answered from the old majority: %+v", g)
	}
	if g := guessAfter(opts, "A", "A", "A", "A", "A", "A", "B", "A", "A"); !g.Known() {
		t.Errorf("an old exception followed by agreeing journals is fine: %+v", g)
	}
}

func stmtEntry(batch, desc, bankSide string, at int) entry {
	other := map[string]string{"D": "C", "C": "D"}[bankSide]
	e := entry{
		j:     Journal{BatchID: batch, AccountDescription: desc},
		src:   SourceStatement,
		lines: []lineAmt{{code: "1120", side: bankSide, amount: 100}, {code: "2999", side: other, amount: 100}},
		when:  day(at),
	}
	e.shape = Shape{Accounts: "1120" + bankSide + ",2999" + other}
	e.keys = keysFor(e.j, partyRef{})
	return e
}

func TestStatementDirectionSplitsDepositsFromWithdrawals(t *testing.T) {
	es := []entry{
		stmtEntry("b1", "transfer", "D", 1), // deposit
		stmtEntry("b1", "transfer", "C", 2), // withdrawal
		stmtEntry("b1", "fee", "C", 3),
	}
	addStatementDirection(es)
	if es[0].dir != "in" || es[1].dir != "out" || es[2].dir != "out" {
		t.Fatalf("direction from the bank account's side: %q %q %q", es[0].dir, es[1].dir, es[2].dir)
	}
	if es[0].keys[0].key == es[1].keys[0].key {
		t.Errorf("a deposit and a withdrawal with the same text must not share a key: %v", es[0].keys)
	}
	// a non-statement journal keeps its keys
	bill := entry{src: SourceImage, keys: []ladderKey{{LevelDesc, "desc:transfer|"}}}
	addStatementDirection([]entry{bill})
	if bill.keys[0].key != "desc:transfer|" || bill.dir != "" {
		t.Errorf("bills are untouched: %+v", bill)
	}
}

// With the default gates, the evidence must agree with the guess on every
// journal: exactly one "used" key when the brain answered, none otherwise,
// and every key above the answer explains why it was passed over.
func TestEvidenceAgreesWithGuessUnderDefaultGates(t *testing.T) {
	m, js, g := fixture()
	b := Build(m, js, g, 0, DefaultOptions(day(10)))
	for i := range b.entries {
		gs := b.guesses[i]
		used := 0
		for _, ev := range b.evidence(i, gs) {
			switch ev.Status {
			case "used":
				used++
				if ev.Level != gs.Level || ev.Total != gs.Support || ev.Confidence < ev.MinConsistency || !ev.RecentAgree {
					t.Errorf("%s: used evidence disagrees with the guess %+v: %+v", b.entries[i].j.GuidFixed, gs, ev)
				}
			case "not_consistent", "not_recent", "too_few", "not_seen", "not_needed":
			default:
				t.Errorf("unknown status %q", ev.Status)
			}
		}
		if want := map[bool]int{true: 1, false: 0}[gs.Known()]; used != want {
			t.Errorf("%s: %d used keys for known=%v", b.entries[i].j.GuidFixed, used, gs.Known())
		}
	}
}

func TestAskUsesStatementDirection(t *testing.T) {
	b := &Brain{byKey: map[string]*Pattern{
		"desc:transfer||dir:in":  {Key: "desc:transfer||dir:in"},
		"desc:transfer||dir:out": {Key: "desc:transfer||dir:out"},
		"desc:fee||dir:out":      {Key: "desc:fee||dir:out"},
	}}
	if p, amb := b.lookup("desc:transfer|", ""); p != nil || !amb {
		t.Errorf("both directions known and none asked → ambiguous")
	}
	if p, _ := b.lookup("desc:transfer|", "in"); p == nil || p.Key != "desc:transfer||dir:in" {
		t.Errorf("asked in → the deposit pattern, got %+v", p)
	}
	if p, amb := b.lookup("desc:fee|", ""); p == nil || amb {
		t.Errorf("only one direction learned → use it")
	}
}
