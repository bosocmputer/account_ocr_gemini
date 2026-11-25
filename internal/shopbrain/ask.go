package shopbrain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// ---------- counterparty search ----------

type PartyMatch struct {
	Kind   string  `json:"kind"`
	Code   string  `json:"code"`
	Name   string  `json:"name"`
	TaxID  string  `json:"taxid,omitempty"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// Thai and English legal-form words that differ between a bill and the
// master record ("บริษัท ช.การช่าง จำกัด" vs "ช การช่าง").
var nameNoise = []string{
	"บริษัทมหาชนจำกัด", "ห้างหุ้นส่วนจำกัด", "ห้างหุ้นส่วนสามัญ", "บริษัท", "บจก", "หจก", "จำกัด", "มหาชน", "สำนักงานใหญ่", "สาขา", "ร้าน",
	"company", "limited", "ltd", "pcl", "partnership",
}

// normalizeName keeps letters and digits only, lowercased, without legal-form words.
func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	for _, w := range nameNoise {
		out = strings.ReplaceAll(out, w, "")
	}
	return out
}

func trigrams(s string) map[string]bool {
	rs := []rune(s)
	out := map[string]bool{}
	if len(rs) < 3 {
		if len(rs) > 0 {
			out[s] = true
		}
		return out
	}
	for i := 0; i+3 <= len(rs); i++ {
		out[string(rs[i:i+3])] = true
	}
	return out
}

func trigramSim(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for k := range ta {
		if tb[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(ta)+len(tb)-inter)
}

// MatchParties finds creditors/debtors by tax ID, code or (fuzzy) name.
func MatchParties(m MasterData, q string, limit int) []PartyMatch {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}
	tax := validTaxID(q)
	nq := normalizeName(q)
	var out []PartyMatch
	for _, p := range append(append([]Party{}, m.Creditors...), m.Debtors...) {
		var score float64
		var why string
		np := normalizeName(p.Name)
		switch {
		case tax != "" && validTaxID(p.TaxID) == tax:
			score, why = 1, "เลขผู้เสียภาษีตรงกัน"
		case strings.EqualFold(strings.TrimSpace(p.Code), q):
			score, why = 0.98, "รหัสตรงกัน"
		case nq != "" && np == nq:
			score, why = 0.95, "ชื่อตรงกัน"
		case nq != "" && np != "" && strings.HasPrefix(np, nq):
			score, why = 0.85, "ชื่อขึ้นต้นตรงกัน"
		case len([]rune(nq)) >= 2 && strings.Contains(np, nq):
			score, why = 0.75, "ชื่อมีคำที่ค้น"
		default:
			if s := trigramSim(nq, np); s >= 0.3 {
				score, why = 0.7*s, "ชื่อคล้ายกัน"
			}
		}
		if score > 0 {
			out = append(out, PartyMatch{Kind: p.Kind, Code: p.Code, Name: p.Name, TaxID: p.TaxID, Score: math.Round(score*100) / 100, Reason: why})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return out[a].Code < out[b].Code
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ---------- ask ----------

type AskInput struct {
	PartyKind   string  `json:"party_kind,omitempty" bson:"party_kind,omitempty"` // creditor | debtor
	PartyCode   string  `json:"party_code,omitempty" bson:"party_code,omitempty"`
	TaxID       string  `json:"taxid,omitempty" bson:"taxid,omitempty"`
	DocFormat   string  `json:"docformat" bson:"docformat"`
	BookCode    string  `json:"bookcode,omitempty" bson:"bookcode,omitempty"`
	Description string  `json:"description,omitempty" bson:"description,omitempty"`
	Amount      float64 `json:"amount,omitempty" bson:"amount,omitempty"`       // document total (sum of debits, incl. VAT)
	Direction   string  `json:"direction,omitempty" bson:"direction,omitempty"` // bank statement: in | out (optional)
}

// lookup finds the pattern for a key. Statement patterns carry money in/out
// in their key; without a direction in the question, a key learned in only
// one direction is used as is, and one learned both ways is ambiguous.
func (b *Brain) lookup(key, dir string) (p *Pattern, ambiguous bool) {
	if dir == "in" || dir == "out" {
		if p = b.byKey[key+"|dir:"+dir]; p != nil {
			return p, false
		}
		return b.byKey[key], false
	}
	if p = b.byKey[key]; p != nil {
		return p, false
	}
	pin, pout := b.byKey[key+"|dir:in"], b.byKey[key+"|dir:out"]
	switch {
	case pin == nil:
		return pout, false
	case pout == nil:
		return pin, false
	}
	return nil, true
}

// Empty reports an input with nothing to look up: no party, tax id, document
// format or description. Many shops never set a document format, so it is
// not required on its own.
func (in AskInput) Empty() bool {
	return strings.TrimSpace(in.PartyCode) == "" && strings.TrimSpace(in.TaxID) == "" &&
		strings.TrimSpace(in.DocFormat) == "" && strings.TrimSpace(in.Description) == ""
}

type LevelCheck struct {
	Level       Level   `json:"level"`
	Status      string  `json:"status"` // used | not_seen | not_consistent | no_input | need_direction
	Total       int     `json:"total,omitempty"`
	Consistency float64 `json:"consistency,omitempty"`
}

type AskAnswer struct {
	GuessView
	Checks  []LevelCheck `json:"checks"`
	Pattern *Pattern     `json:"pattern,omitempty"`
}

// Ask answers from the shop's current patterns (recency-weighted, the same
// ones the page lists) for a journal described by the input.
func (b *Brain) Ask(in AskInput) AskAnswer {
	return b.askKeys(in, b.askLadder(in))
}

// askLadder builds the ladder keys of a journal described by the input.
func (b *Brain) askLadder(in AskInput) []ladderKey {
	j := Journal{DocFormat: strings.TrimSpace(in.DocFormat), BookCode: strings.TrimSpace(in.BookCode), AccountDescription: in.Description}
	kind := in.PartyKind
	taxID := validTaxID(in.TaxID)
	if in.PartyCode != "" {
		for _, p := range append(append([]Party{}, b.Master.Creditors...), b.Master.Debtors...) {
			if p.Code == in.PartyCode && (kind == "" || p.Kind == kind) {
				kind = p.Kind
				if taxID == "" {
					taxID = validTaxID(p.TaxID)
				}
				break
			}
		}
		rp := &rawParty{Code: in.PartyCode, TaxID: taxID}
		if kind == "debtor" {
			j.Debtor = rp
		} else {
			j.Creditor, j.DebtAccountType = rp, 1
		}
	} else if taxID != "" {
		j.Vats = []rawVat{{CustTaxID: taxID}}
	}
	return keysFor(j, counterparty(j))
}

func (b *Brain) askKeys(in AskInput, keys []ladderKey) AskAnswer {
	have := map[Level]bool{}
	for _, k := range keys {
		have[k.level] = true
	}
	ans := AskAnswer{GuessView: GuessView{Level: levelNoMatch}}
	mixed, fewest := false, 0 // a skipped key booked several ways / booked one way but seen too few times
	for _, l := range allLevels {
		if !have[l] {
			ans.Checks = append(ans.Checks, LevelCheck{Level: l, Status: "no_input"})
		}
	}
	for _, k := range keys {
		p, ambiguous := b.lookup(k.key, in.Direction)
		switch {
		case ambiguous:
			ans.Checks = append(ans.Checks, LevelCheck{Level: k.level, Status: "need_direction"})
		case p == nil:
			ans.Checks = append(ans.Checks, LevelCheck{Level: k.level, Status: "not_seen"})
		case !p.Usable:
			ans.Checks = append(ans.Checks, LevelCheck{Level: k.level, Status: "not_consistent", Total: p.Total, Consistency: p.Consistency})
			if len(p.Others) > 0 {
				mixed = true
			} else if fewest == 0 || p.Total < fewest {
				fewest = p.Total
			}
			if p.Winner != nil {
				ans.Skipped = append(ans.Skipped, SkipView{Level: k.level, Support: p.Total, Consistency: p.Consistency, WouldBe: b.variantView(p.Winner, in.Amount)})
			}
		default:
			ans.Checks = append(ans.Checks, LevelCheck{Level: k.level, Status: "used", Total: p.Total, Consistency: p.Consistency})
			if !ans.Known {
				sv := b.variantView(p.Winner, in.Amount)
				ans.Known, ans.Level, ans.PatternID, ans.Support, ans.Consistency, ans.Shape, ans.Pattern = true, k.level, p.ID, p.Total, p.Consistency, &sv, p
			}
		}
	}
	sort.SliceStable(ans.Checks, func(a, b int) bool { return levelRank(ans.Checks[a].Level) < levelRank(ans.Checks[b].Level) })
	if !ans.Known {
		needDir := false
		for _, c := range ans.Checks {
			needDir = needDir || c.Status == "need_direction"
		}
		if needDir && len(ans.Skipped) == 0 {
			ans.Reason = "รายการแบบนี้ร้านลงต่างกันระหว่างเงินเข้ากับเงินออก บอกด้วยว่าเงินเข้าหรือออก"
		} else if len(ans.Skipped) > 0 && !mixed && fewest > 0 {
			ans.Reason = fmt.Sprintf("เคยเห็นเพียง %d ครั้ง ลงแบบเดียวกันทุกครั้ง แต่ยังน้อยเกินกว่าจะมั่นใจ จึงตอบว่าไม่รู้", fewest)
		} else if len(ans.Skipped) > 0 {
			ans.Reason = "เคยเห็นแต่ลงบัญชีไม่สม่ำเสมอพอ จึงตอบว่าไม่รู้"
		} else {
			ans.Reason = "ยังไม่เคยเห็นรายการแบบนี้มาก่อน"
		}
	}
	return ans
}

// variantView turns a learned variant into lines; with an amount, each line
// gets ratio × amount, rounded to satang and balanced so Dr = Cr = amount.
func (b *Brain) variantView(v *Variant, amount float64) ShapeView {
	sv := ShapeView{Accounts: v.Shape.Accounts, VAT: v.Shape.VAT, WHT: v.Shape.WHT}
	for _, l := range v.Lines {
		name := l.AccountName
		if name == "" {
			name = b.accountName(l.AccountCode)
		}
		lv := LineView{AccountCode: l.AccountCode, AccountName: name, Side: l.Side}
		if amount > 0 {
			lv.Amount = math.Round(l.RatioAvg*amount*100) / 100
		}
		sv.Lines = append(sv.Lines, lv)
	}
	debitsFirst(sv.Lines)
	if amount > 0 {
		balanceSide(sv.Lines, "D", amount)
		balanceSide(sv.Lines, "C", amount)
	}
	return sv
}

// balanceSide puts the rounding difference on the side's largest line.
func balanceSide(lines []LineView, side string, amount float64) {
	sum, big := 0.0, -1
	for i, l := range lines {
		if l.Side != side {
			continue
		}
		sum += l.Amount
		if big < 0 || l.Amount > lines[big].Amount {
			big = i
		}
	}
	if big >= 0 {
		lines[big].Amount = math.Round((lines[big].Amount+amount-sum)*100) / 100
	}
}
