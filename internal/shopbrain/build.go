package shopbrain

import (
	"math"
	"sort"
	"strings"
	"time"
)

const BrainVersion = 1

// Options are the tunables; defaults come from DefaultOptions.
type Options struct {
	AsOf             time.Time `json:"as_of"`
	HalfLifeDays     float64   `json:"half_life_days"` // recency weight halves every N days
	CheckedBoost     float64   `json:"checked_boost"`  // weight multiplier for journals whose VAT/WHT was checked
	MaxExamples      int       `json:"max_examples"`   // journals kept as examples per variant
	MaxImages        int       `json:"max_images"`     // example images per variant
	MaxChanges       int       `json:"max_changes"`    // shape changes kept per pattern
	MinJournals      int       `json:"min_journals"`   // readiness thresholds
	MaxIdleDays      int       `json:"max_idle_days"`
	MinBacktestGuess int       `json:"min_backtest_predictions"`
	// A level is only used to predict when its leading shape holds at least
	// this share of the earlier journals under the key; otherwise the next
	// level is tried, and if none qualifies the brain answers "unknown"
	// instead of guessing. L4 (docformat + book) has its own, stricter bar.
	MinConsistency       float64 `json:"min_consistency"`
	MinConsistencyFormat float64 `json:"min_consistency_format"`
}

func DefaultOptions(now time.Time) Options {
	return Options{AsOf: now, HalfLifeDays: 180, CheckedBoost: 1.5, MaxExamples: 2, MaxImages: 2, MaxChanges: 5,
		MinJournals: 100, MaxIdleDays: 90, MinBacktestGuess: 30, MinConsistency: 0.6, MinConsistencyFormat: 0.9}
}

type entry struct {
	j     Journal
	src   Source
	group *ImageGroup
	party partyRef
	shape Shape
	lines []lineAmt
	total float64
	keys  []ladderKey
	when  time.Time
}

func prepare(journals []Journal, groups map[string]ImageGroup) []entry {
	out := make([]entry, 0, len(journals))
	for _, j := range journals {
		src, g := classifySource(j, groups)
		s, lines, total := shapeOf(j)
		if s.Accounts == "" {
			continue // nothing booked (all-zero lines) — nothing to learn
		}
		p := counterparty(j)
		when := j.CreatedAt
		if when.IsZero() || when.Year() < 2000 {
			when = j.DocDate
		}
		out = append(out, entry{j: j, src: src, group: g, party: p, shape: s, lines: lines, total: total, keys: keysFor(j, p), when: when})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].when.Before(out[b].when) })
	return out
}

// Build turns master data + journals into a Brain. It is pure (no I/O) so
// it can be tested with in-memory fixtures.
func Build(master MasterData, journals []Journal, groups map[string]ImageGroup, decodeErrors int, opts Options) *Brain {
	entries := prepare(journals, groups)
	chart := map[string]Account{}
	for _, a := range master.Accounts {
		chart[a.Code] = a
	}

	patterns := map[string]*Pattern{}
	for _, e := range entries {
		w := recencyWeight(e.when, opts)
		if isChecked(e.j) {
			w *= opts.CheckedBoost
		}
		for _, k := range e.keys {
			p := patterns[k.key]
			if p == nil {
				p = newPattern(k, e)
				patterns[k.key] = p
			}
			p.add(e, w, opts)
		}
	}

	list := make([]*Pattern, 0, len(patterns))
	for _, p := range patterns {
		p.finish(chart, opts)
		list = append(list, p)
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].Level != list[b].Level {
			return levelRank(list[a].Level) < levelRank(list[b].Level)
		}
		if list[a].Total != list[b].Total {
			return list[a].Total > list[b].Total
		}
		return list[a].Key < list[b].Key
	})

	b := &Brain{
		Version: BrainVersion, GeneratedAt: opts.AsOf, Options: opts, Master: master, Patterns: list,
		Usage: buildUsage(master, entries),
		Index: map[string]string{},
	}
	b.Backtest, b.guesses, b.shapes = replay(entries, opts)
	b.entries = entries
	b.byKey = make(map[string]*Pattern, len(list))
	for _, p := range list {
		b.byKey[p.Key] = p
	}
	b.chart = chart
	for _, j := range journals {
		b.Index[j.GuidFixed] = journalHash(j)
		if j.UpdatedAt.After(b.Watermark.MaxUpdatedAt) {
			b.Watermark.MaxUpdatedAt = j.UpdatedAt
		}
		if j.CreatedAt.After(b.Watermark.MaxCreatedAt) {
			b.Watermark.MaxCreatedAt = j.CreatedAt
		}
	}
	b.Watermark.Journals = len(journals)
	b.Readiness = buildReadiness(journals, groups, entries, decodeErrors, b.Backtest, opts)
	return b
}

func (o Options) minConsistency(l Level) float64 {
	if l == LevelFormat {
		return o.MinConsistencyFormat
	}
	return o.MinConsistency
}

func levelRank(l Level) int {
	for i, x := range allLevels {
		if x == l {
			return i
		}
	}
	return len(allLevels)
}

func recencyWeight(when time.Time, opts Options) float64 {
	if opts.HalfLifeDays <= 0 {
		return 1
	}
	age := opts.AsOf.Sub(when).Hours() / 24
	if age < 0 {
		age = 0
	}
	return math.Pow(0.5, age/opts.HalfLifeDays)
}

func newPattern(k ladderKey, e entry) *Pattern {
	p := &Pattern{ID: patternID(k.key), Level: k.level, Key: k.key, DocFormat: e.j.DocFormat,
		variants: map[string]*Variant{}, countsSoFar: map[string]int{}}
	switch k.level {
	case LevelTaxID:
		p.TaxID, p.PartyKind, p.PartyCode, p.PartyName = e.party.TaxID, e.party.Kind, e.party.Code, e.party.Name
	case LevelParty:
		p.PartyKind, p.PartyCode, p.PartyName, p.TaxID = e.party.Kind, e.party.Code, e.party.Name, e.party.TaxID
	case LevelDesc:
		p.Description = normalizeDescription(e.j.AccountDescription)
	case LevelFormat:
		p.BookCode = e.j.BookCode
	}
	return p
}

func (p *Pattern) add(e entry, w float64, opts Options) {
	fk := e.shape.fullKey()
	v := p.variants[fk]
	if v == nil {
		v = &Variant{Shape: e.shape, FirstSeen: e.when, Sources: map[Source]int{}, BookCodes: map[string]int{},
			DocNoPrefixes: map[string]int{}, BranchCodes: map[string]int{}}
		for _, l := range e.lines {
			v.Lines = append(v.Lines, &ShapeLine{AccountCode: l.code, AccountName: l.name, Side: l.side, RatioMin: math.Inf(1), RatioMax: math.Inf(-1)})
		}
		p.variants[fk] = v
	}
	// One tax ID can belong to several counterparty codes (duplicate records);
	// such a pattern is then not attributed to any single counterparty.
	if p.Level == LevelTaxID && p.PartyCode != "" && e.party.Code != p.PartyCode {
		p.PartyKind, p.PartyCode, p.PartyName, p.MultiParty = "", "", "", true
	}
	v.Count++
	v.Weight += w
	if isChecked(e.j) {
		v.CheckedCount++
	}
	if e.when.After(v.LastSeen) {
		v.LastSeen = e.when
	}
	v.Sources[e.src]++
	v.BookCodes[e.j.BookCode]++
	v.DocNoPrefixes[docnoPrefix(e.j.DocNo)]++
	if e.j.BranchCode != "" {
		v.BranchCodes[e.j.BranchCode]++
	}
	if e.total > 0 {
		for i, l := range e.lines {
			if i >= len(v.Lines) {
				break
			}
			r := l.amount / e.total
			sl := v.Lines[i]
			sl.ratioSum += r
			sl.ratioN++
			sl.RatioMin = math.Min(sl.RatioMin, r)
			sl.RatioMax = math.Max(sl.RatioMax, r)
			if sl.AccountName == "" {
				sl.AccountName = l.name
			}
		}
	}
	// Entries arrive oldest first, so keeping the last N gives the latest examples.
	v.Examples = append(v.Examples, Example{DocNo: e.j.DocNo, GuidFixed: e.j.GuidFixed, DocDate: e.j.DocDate, Source: e.src})
	if len(v.Examples) > opts.MaxExamples {
		v.Examples = v.Examples[len(v.Examples)-opts.MaxExamples:]
	}
	if e.group != nil {
		for _, img := range e.group.ImageReferences {
			if img.ImageURI != "" {
				v.ExampleImages = append(v.ExampleImages, img.ImageURI)
			}
		}
		if len(v.ExampleImages) > opts.MaxImages {
			v.ExampleImages = v.ExampleImages[len(v.ExampleImages)-opts.MaxImages:]
		}
	}

	// Track the leading shape over time (plain counts) to record when the shop changed how it books this key.
	p.Total++
	p.countsSoFar[fk]++
	leader := p.leader
	if leader == "" || p.countsSoFar[fk] > p.countsSoFar[leader] {
		leader = fk
	}
	if p.leader != "" && leader != p.leader && p.countsSoFar[p.leader] >= 2 {
		p.Changes = append(p.Changes, ShapeChange{At: e.when, From: p.leader, To: leader})
		if len(p.Changes) > opts.MaxChanges {
			p.Changes = p.Changes[len(p.Changes)-opts.MaxChanges:]
		}
	}
	p.leader = leader
}

func (p *Pattern) finish(chart map[string]Account, opts Options) {
	var vs []*Variant
	totalW := 0.0
	for _, v := range p.variants {
		for _, l := range v.Lines {
			if l.ratioN > 0 {
				// 6 decimals: amounts are rebuilt as ratio × total, and 4 decimals
				// turn 7/107 (VAT 7%) into 69.98 on a 1,070.00 bill.
				l.RatioAvg = round6(l.ratioSum / float64(l.ratioN))
				l.RatioMin, l.RatioMax = round6(l.RatioMin), round6(l.RatioMax)
			} else {
				l.RatioMin, l.RatioMax = 0, 0
			}
			if a, ok := chart[l.AccountCode]; ok && a.Name != "" {
				l.AccountName = a.Name
			}
		}
		delete(v.BranchCodes, "")
		totalW += v.Weight
		vs = append(vs, v)
	}
	sort.Slice(vs, func(a, b int) bool {
		if vs[a].Weight != vs[b].Weight {
			return vs[a].Weight > vs[b].Weight
		}
		return vs[a].Count > vs[b].Count
	})
	p.Winner = vs[0]
	if len(vs) > 1 {
		p.Others = vs[1:]
		if len(p.Others) > 3 {
			p.Others = p.Others[:3]
		}
	}
	if totalW > 0 {
		p.Consistency = round4(p.Winner.Weight / totalW)
	}
	p.Usable = p.Total >= minSupport(p.Level) && p.Consistency >= opts.minConsistency(p.Level)
	for _, v := range vs {
		v.Weight = round4(v.Weight)
	}
	if len(chart) > 0 {
		for _, l := range p.Winner.Lines {
			if _, ok := chart[l.AccountCode]; !ok {
				p.MissingAccts = append(p.MissingAccts, l.AccountCode)
			}
		}
	}
}

func round6(f float64) float64 {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	}
	return math.Round(f*1e6) / 1e6
}

func round4(f float64) float64 {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	}
	return math.Round(f*10000) / 10000
}

func buildReadiness(journals []Journal, groups map[string]ImageGroup, entries []entry, decodeErrors int, bt Backtest, opts Options) Readiness {
	r := Readiness{Journals: len(journals), BySource: map[Source]int{}, DecodeErrors: decodeErrors}
	months := map[string]bool{}
	for _, j := range journals {
		src, _ := classifySource(j, groups)
		r.BySource[src]++
	}
	for _, e := range entries {
		months[e.when.Format("2006-01")] = true
		if r.FirstEntry.IsZero() || e.when.Before(r.FirstEntry) {
			r.FirstEntry = e.when
		}
		if e.when.After(r.LastEntry) {
			r.LastEntry = e.when
		}
	}
	r.ActiveMonths = len(months)
	if r.Journals < opts.MinJournals {
		r.Reasons = append(r.Reasons, "journal น้อยกว่าเกณฑ์")
	}
	if r.LastEntry.IsZero() || opts.AsOf.Sub(r.LastEntry) > time.Duration(opts.MaxIdleDays)*24*time.Hour {
		r.Reasons = append(r.Reasons, "ไม่มีการบันทึกในช่วงล่าสุด")
	}
	if bt.Overall.Predicted < opts.MinBacktestGuess {
		r.Reasons = append(r.Reasons, "จำลองย้อนหลังเดาได้น้อยกว่าเกณฑ์")
	}
	if decodeErrors > 0 {
		r.Reasons = append(r.Reasons, "มีเอกสารที่อ่านไม่ได้บางส่วน")
	}
	r.Insufficient = len(r.Reasons) > 0 && !(len(r.Reasons) == 1 && strings.HasPrefix(r.Reasons[0], "มีเอกสาร"))
	return r
}
