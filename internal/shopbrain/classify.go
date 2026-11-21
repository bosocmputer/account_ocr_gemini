package shopbrain

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// classifySource checks statement first: statement journals also carry a
// documentref (e.g. ภูพิงค์ แอร์), so documentref alone does not mean "image".
func classifySource(j Journal, groups map[string]ImageGroup) (Source, *ImageGroup) {
	if strings.TrimSpace(j.BatchID) != "" {
		return SourceStatement, nil
	}
	if j.DocumentRef != "" {
		if g, ok := groups[j.DocumentRef]; ok && len(g.ImageReferences) > 0 {
			return SourceImage, &g
		}
	}
	return SourceNoImage, nil
}

type partyRef struct {
	Kind, Code, Name, TaxID string
}

// validTaxID keeps only a real 13-digit Thai tax ID; placeholders such as
// "-" or "0000000000000" (walk-in customers) would otherwise merge many
// unrelated counterparties under one L1 key.
func validTaxID(s string) string {
	d := make([]rune, 0, 13)
	for _, r := range s {
		if r >= '0' && r <= '9' {
			d = append(d, r)
		} else if r != '-' && r != ' ' {
			return ""
		}
	}
	if len(d) != 13 || strings.Trim(string(d), "0") == "" {
		return ""
	}
	return string(d)
}

// counterparty follows bcaccount: debtaccounttype 0 = debtor, 1 = creditor,
// falling back to whichever side is filled.
func counterparty(j Journal) partyRef {
	pick := func(p *rawParty, kind string) partyRef {
		if p == nil || p.Code == "" {
			return partyRef{}
		}
		return partyRef{Kind: kind, Code: p.Code, Name: pickName(p.Names), TaxID: validTaxID(p.TaxID)}
	}
	var r partyRef
	if j.DebtAccountType == 1 {
		r = pick(j.Creditor, "creditor")
		if r.Code == "" {
			r = pick(j.Debtor, "debtor")
		}
	} else {
		r = pick(j.Debtor, "debtor")
		if r.Code == "" {
			r = pick(j.Creditor, "creditor")
		}
	}
	if r.TaxID == "" {
		for _, v := range j.Vats {
			if t := validTaxID(v.CustTaxID); t != "" {
				r.TaxID = t
				break
			}
		}
	}
	if r.TaxID == "" {
		for _, t := range j.Taxes {
			if id := validTaxID(t.CustTaxID); id != "" {
				r.TaxID = id
				break
			}
		}
	}
	return r
}

type lineAmt struct {
	code, name, side string
	amount           float64
}

// shapeOf reduces a journal to its booking shape plus per-line amounts.
// Lines of the same account and side are summed; zero lines are dropped.
func shapeOf(j Journal) (Shape, []lineAmt, float64) {
	agg := map[string]*lineAmt{}
	for _, d := range j.Details {
		if d.AccountCode == "" {
			continue
		}
		net := d.Debit - d.Credit
		if math.Abs(net) < 0.005 {
			continue
		}
		side := "D"
		if net < 0 {
			side = "C"
		}
		k := d.AccountCode + side
		if agg[k] == nil {
			agg[k] = &lineAmt{code: d.AccountCode, name: d.AccountName, side: side}
		}
		agg[k].amount += math.Abs(net)
	}
	lines := make([]lineAmt, 0, len(agg))
	total := 0.0
	for _, l := range agg {
		lines = append(lines, *l)
		if l.side == "D" {
			total += l.amount
		}
	}
	sort.Slice(lines, func(a, b int) bool { return lines[a].code+lines[a].side < lines[b].code+lines[b].side })
	keys := make([]string, len(lines))
	for i, l := range lines {
		keys[i] = l.code + l.side
	}
	s := Shape{Accounts: strings.Join(keys, ",")}
	if len(j.Vats) > 0 {
		v := j.Vats[0]
		s.VAT = &VatInfo{VatMode: v.VatMode, VatType: v.VatType, VatRate: v.VatRate}
	}
	if len(j.Taxes) > 0 {
		t := j.Taxes[0]
		w := &WhtInfo{TaxType: t.TaxType, ConditionTaxType: t.ConditionTaxType}
		if len(t.Details) > 0 {
			w.TaxRate = t.Details[0].TaxRate
			w.IncomeType = strings.TrimSpace(t.Details[0].Description)
		}
		s.WHT = w
	}
	return s, lines, total
}

func vatKey(v *VatInfo) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("m%d/t%d/r%s", v.VatMode, v.VatType, fnum(v.VatRate))
}

func whtKey(w *WhtInfo) string {
	if w == nil {
		return "-"
	}
	return fmt.Sprintf("t%d/c%d/r%s", w.TaxType, w.ConditionTaxType, fnum(w.TaxRate))
}

func fnum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// normalizeDescription keeps the words of accountdescription and drops
// numbers, dates, amounts and punctuation so "ค่าน้ำมัน 12/03 1,250.00" and
// "ค่าน้ำมัน 15/04 980" fall under the same key.
func normalizeDescription(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteRune(' ')
			space = true
		}
	}
	out := strings.TrimSpace(b.String())
	if rs := []rune(out); len(rs) > 60 {
		out = strings.TrimSpace(string(rs[:60]))
	}
	return out
}

// docnoPrefix is the leading non-digit part of a document number ("PV6903-0012" → "PV").
func docnoPrefix(docno string) string {
	docno = strings.TrimSpace(docno)
	for i, r := range docno {
		if unicode.IsDigit(r) {
			return strings.TrimRight(docno[:i], "-_/ ")
		}
	}
	return docno
}

type ladderKey struct {
	level Level
	key   string
}

// keysFor returns the lookup keys for a journal, most specific first.
func keysFor(j Journal, p partyRef) []ladderKey {
	out := make([]ladderKey, 0, 4)
	if p.TaxID != "" {
		out = append(out, ladderKey{LevelTaxID, "tax:" + p.TaxID + "|" + j.DocFormat})
	}
	if p.Code != "" {
		out = append(out, ladderKey{LevelParty, "party:" + p.Kind + ":" + p.Code + "|" + j.DocFormat})
	}
	if d := normalizeDescription(j.AccountDescription); d != "" {
		out = append(out, ladderKey{LevelDesc, "desc:" + d + "|" + j.DocFormat})
	}
	out = append(out, ladderKey{LevelFormat, "fmt:" + j.DocFormat + "|" + j.BookCode})
	return out
}

// minSupport is how many earlier journals a key needs before it is trusted.
func minSupport(l Level) int {
	if l == LevelTaxID {
		return 1
	}
	return 2
}

func isChecked(j Journal) bool {
	for _, v := range j.Vats {
		if v.IsChecked > 0 {
			return true
		}
	}
	for _, t := range j.Taxes {
		if t.IsChecked > 0 {
			return true
		}
	}
	return false
}

func patternID(key string) string {
	h := sha1.Sum([]byte(key))
	return "PAT-" + strings.ToUpper(hex.EncodeToString(h[:4]))
}

// journalHash covers every field the brain learns from, so an edited
// journal gets a different hash even when updatedat was not bumped.
func journalHash(j Journal) string {
	s, lines, _ := shapeOf(j)
	p := counterparty(j)
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|%s|%s|%d|%s|%s|%t",
		j.DocNo, j.DocDate.UTC().Format("2006-01-02"), j.DocFormat, j.BookCode, j.BranchCode, j.BatchID, j.DocumentRef,
		j.AccountDescription, s.fullKey(), j.DebtAccountType, p.Code, p.TaxID, isChecked(j))
	for _, l := range lines {
		fmt.Fprintf(&b, "|%s%s%.2f", l.code, l.side, l.amount)
	}
	h := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(h[:8])
}
