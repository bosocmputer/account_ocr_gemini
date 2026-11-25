package shopbrain

// Threshold experiment on real data (read-only). Skipped unless
// SHOPBRAIN_EXP_SHOP is set together with MONGO_URI / MONGO_DB_NAME:
//
//	SHOPBRAIN_EXP_SHOP=<shopid> MONGO_URI=... MONGO_DB_NAME=... go test ./internal/shopbrain -run TestExperimentThresholds -v
//
// It replays the shop's journals oldest first, like the backtest, under
// several gating rules and prints coverage / precision for each. Nothing is
// written anywhere.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
)

type expRule struct {
	name          string
	taxMinSupport int     // L1 minimum earlier journals (current: 1)
	directional   bool    // L1 key also carries buy/sell (creditor/debtor)
	wilsonZ       float64 // > 0: gate on the Wilson lower bound of the leading share instead of the raw share
	recentAgree   int     // > 0: the last N journals under the key must all match the leading shape
	noTax         bool    // skip L1 entirely (fall through to party / description)
	taxRecent     int     // > 0: recentAgree applied to L1 only
	stmtDir       bool    // statement journals: keys also carry money in/out (side of the batch's bank account)
}

// bankAnchor finds, per statement batch, the account present in every journal
// of the batch — the statement's own bank account (known at import time).
func bankAnchor(entries []entry) map[string]string {
	sets := map[string]map[string]int{}
	n := map[string]int{}
	for _, e := range entries {
		if e.src != SourceStatement || e.j.BatchID == "" {
			continue
		}
		n[e.j.BatchID]++
		if sets[e.j.BatchID] == nil {
			sets[e.j.BatchID] = map[string]int{}
		}
		seen := map[string]bool{}
		for _, l := range e.lines {
			if !seen[l.code] {
				sets[e.j.BatchID][l.code]++
				seen[l.code] = true
			}
		}
	}
	out := map[string]string{}
	for b, m := range sets {
		var cands []string
		for code, c := range m {
			if c == n[b] {
				cands = append(cands, code)
			}
		}
		sort.Strings(cands)
		if len(cands) > 0 {
			out[b] = cands[0]
		}
	}
	return out
}

func stmtDirection(e entry, anchor map[string]string) string {
	a := anchor[e.j.BatchID]
	if a == "" {
		return ""
	}
	for _, l := range e.lines {
		if l.code == a {
			if l.side == "D" {
				return "in"
			}
			return "out"
		}
	}
	return ""
}

type expTally struct {
	full   map[string]int
	n      int
	recent []string // newest last
}

type expStat struct {
	total, predicted, hitAcc, hitFull int
	byLevel                           map[Level]*[3]int  // predicted, hitAcc, hitFull
	bySource                          map[Source]*[3]int // total, predicted, hitAcc
	wrong                             []string
}

func expKeys(e entry, r expRule, anchor map[string]string) []ladderKey {
	dir := ""
	if r.stmtDir && e.src == SourceStatement {
		dir = stmtDirection(e, anchor)
	}
	if !r.directional && dir == "" {
		return e.keys
	}
	out := make([]ladderKey, len(e.keys))
	for i, k := range e.keys {
		if dir != "" {
			k.key += "|dir:" + dir
		}
		if r.directional && k.level == LevelTaxID {
			kind := e.party.Kind
			if kind == "" {
				kind = map[bool]string{true: "creditor", false: "debtor"}[e.j.DebtAccountType == 1]
			}
			k.key = "tax:" + kind + ":" + strings.TrimPrefix(k.key, "tax:")
		}
		out[i] = k
	}
	return out
}

func runRule(entries []entry, opts Options, r expRule) expStat {
	st := expStat{byLevel: map[Level]*[3]int{}, bySource: map[Source]*[3]int{}}
	seen := map[string]*expTally{}
	anchor := bankAnchor(entries)
	for _, e := range entries {
		keys := expKeys(e, r, anchor)
		st.total++
		var level Level
		var guess string
		for _, k := range keys {
			if r.noTax && k.level == LevelTaxID {
				continue
			}
			t := seen[k.key]
			min := minSupport(k.level)
			if k.level == LevelTaxID && r.taxMinSupport > 0 {
				min = r.taxMinSupport
			}
			if t == nil || t.n < min {
				continue
			}
			lead, cnt := "", -1
			for f, c := range t.full {
				if c > cnt || (c == cnt && f < lead) {
					lead, cnt = f, c
				}
			}
			share := float64(cnt) / float64(t.n)
			if r.wilsonZ > 0 {
				share = wilsonLower(cnt, t.n, r.wilsonZ)
			}
			if share < opts.minConsistency(k.level) {
				continue
			}
			need := r.recentAgree
			if k.level == LevelTaxID && r.taxRecent > need {
				need = r.taxRecent
			}
			if need > 0 {
				ok := len(t.recent) >= need
				for i := len(t.recent) - need; ok && i < len(t.recent); i++ {
					ok = i >= 0 && t.recent[i] == lead
				}
				if !ok {
					continue
				}
			}
			level, guess = k.level, lead
			break
		}
		full := e.shape.fullKey()
		bs := st.bySource[e.src]
		if bs == nil {
			bs = &[3]int{}
			st.bySource[e.src] = bs
		}
		bs[0]++
		if guess != "" {
			bs[1]++
			if strings.SplitN(guess, "|", 2)[0] == e.shape.Accounts {
				bs[2]++
			}
			st.predicted++
			b := st.byLevel[level]
			if b == nil {
				b = &[3]int{}
				st.byLevel[level] = b
			}
			b[0]++
			if strings.SplitN(guess, "|", 2)[0] == e.shape.Accounts {
				st.hitAcc++
				b[1]++
			} else if len(st.wrong) < 8 {
				st.wrong = append(st.wrong, fmt.Sprintf("%s %s %s", e.j.DocNo, level, e.when.Format("2006-01-02")))
			}
			if guess == full {
				st.hitFull++
				b[2]++
			}
		}
		for _, k := range keys {
			t := seen[k.key]
			if t == nil {
				t = &expTally{full: map[string]int{}}
				seen[k.key] = t
			}
			t.full[full]++
			t.n++
			t.recent = append(t.recent, full)
			if len(t.recent) > 5 {
				t.recent = t.recent[1:]
			}
		}
	}
	return st
}

func TestExperimentThresholds(t *testing.T) {
	shop := os.Getenv("SHOPBRAIN_EXP_SHOP")
	if shop == "" {
		t.Skip("set SHOPBRAIN_EXP_SHOP (+ MONGO_URI, MONGO_DB_NAME) to run")
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")
	if err := storage.InitMongoDB(); err != nil {
		t.Fatal(err)
	}
	defer storage.CloseMongoDB()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	journals, groups, _, err := LoadJournals(ctx, shop)
	if err != nil {
		t.Fatal(err)
	}
	entries := prepare(journals, groups)
	opts := DefaultOptions(time.Now())

	src := map[Source]int{}
	for _, e := range entries {
		src[e.src]++
	}
	t.Logf("shop %s: %d journals, %d usable entries, sources %v, %s → %s", shop, len(journals), len(entries), src,
		entries[0].when.Format("2006-01-02"), entries[len(entries)-1].when.Format("2006-01-02"))

	rules := []expRule{
		{name: "A ปัจจุบัน"},
		{name: "B เลขผู้เสียภาษี ≥2 ครั้ง", taxMinSupport: 2},
		{name: "C แยกซื้อ/ขาย", directional: true},
		{name: "D = B+C", taxMinSupport: 2, directional: true},
		{name: "E Wilson z=1.28", wilsonZ: 1.28},
		{name: "F 2 ครั้งล่าสุดตรงกัน", recentAgree: 2},
		{name: "G = B+C+E", taxMinSupport: 2, directional: true, wilsonZ: 1.28},
		{name: "H = B+C+E+F", taxMinSupport: 2, directional: true, wilsonZ: 1.28, recentAgree: 2},
		{name: "I ไม่ใช้เลขผู้เสียภาษี", noTax: true},
		{name: "J เลขผู้เสียภาษี 3 ครั้งล่าสุดตรงกัน", taxRecent: 3},
		{name: "K = J+E", taxRecent: 3, wilsonZ: 1.28},
		{name: "L = I+E+F", noTax: true, wilsonZ: 1.28, recentAgree: 2},
		{name: "M Statement เข้า/ออก", stmtDir: true},
		{name: "N = E+F+M", wilsonZ: 1.28, recentAgree: 2, stmtDir: true},
	}
	pc := func(a, b int) string {
		if b == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(a)/float64(b))
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("%-28s %6s %6s %6s %8s %8s %8s", "rule", "total", "answer", "wrong", "coverage", "acc-hit", "full-hit"))
	for _, r := range rules {
		s := runRule(entries, opts, r)
		lines = append(lines, fmt.Sprintf("%-28s %6d %6d %6d %8s %8s %8s", r.name, s.total, s.predicted, s.predicted-s.hitAcc, pc(s.predicted, s.total), pc(s.hitAcc, s.predicted), pc(s.hitFull, s.predicted)))
		lv := make([]string, 0)
		for _, l := range []Level{LevelTaxID, LevelParty, LevelDesc, LevelFormat} {
			if b := s.byLevel[l]; b != nil {
				lv = append(lv, fmt.Sprintf("%s %d/%d", l, b[1], b[0]))
			}
		}
		sort.Strings(lv)
		lines = append(lines, "    by level (acc-hit/answered): "+strings.Join(lv, " • "))
		var bsrc []string
		for _, so := range []Source{SourceImage, SourceNoImage, SourceStatement} {
			if b := s.bySource[so]; b != nil {
				bsrc = append(bsrc, fmt.Sprintf("%s answered %d/%d (%s) right %s", so, b[1], b[0], pc(b[1], b[0]), pc(b[2], b[1])))
			}
		}
		lines = append(lines, "    by source: "+strings.Join(bsrc, " • "))
		if r.name == "A ปัจจุบัน" && len(s.wrong) > 0 {
			lines = append(lines, "    first wrong: "+strings.Join(s.wrong, "; "))
		}
	}
	t.Log("\n" + strings.Join(lines, "\n"))

	// the shipped code with DefaultOptions must reproduce rule N
	master, _, err := LoadMaster(ctx, shop)
	if err != nil {
		t.Fatal(err)
	}
	bt := Build(master, journals, groups, 0, opts).Backtest
	o := bt.Overall
	t.Logf("SHIPPED DefaultOptions: total %d answered %d wrong %d coverage %s acc-hit %s full-hit %s", o.Total, o.Predicted, o.Predicted-o.HitAccounts, pc(o.Predicted, o.Total), pc(o.HitAccounts, o.Predicted), pc(o.HitFull, o.Predicted))
	for _, so := range []Source{SourceImage, SourceStatement} {
		if h := bt.BySource[so]; h != nil {
			t.Logf("SHIPPED %s: answered %d/%d right %s", so, h.Predicted, h.Total, pc(h.HitAccounts, h.Predicted))
		}
	}
}
