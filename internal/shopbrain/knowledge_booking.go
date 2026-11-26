package shopbrain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func (in mcpSuggestInput) billParties() (billParty, billParty, bool) {
	seller := billParty{Name: strings.TrimSpace(in.SellerName), TaxID: strings.TrimSpace(in.SellerTaxID)}
	buyer := billParty{Name: strings.TrimSpace(in.BuyerName), TaxID: strings.TrimSpace(in.BuyerTaxID)}
	return seller, buyer, seller != (billParty{}) || buyer != (billParty{})
}

func (b *Brain) suggestWithKnowledge(in mcpSuggestInput, items []KnowledgeItem, now time.Time) (Suggestion, error) {
	day := dateText(now)
	if strings.TrimSpace(in.DocDate) != "" {
		parsed, err := parseDay(in.DocDate)
		if err != nil {
			return Suggestion{}, err
		}
		day = dateText(parsed)
	}

	profile := profileOf(b.Master.Shop, knowledgeAliases(items))
	seller, buyer, hasHeader := in.billParties()
	var kind billClassification
	if hasHeader {
		kind = classifyBill(profile, seller, buyer)
		if kind.Direction == "unknown" {
			return Suggestion{BillType: kind.Direction, BillTypeReason: kind.Reason, Reason: kind.Reason, DocDate: day, Shop: &profile, AppliedKnowledge: []KnowledgeItem{}, RefsNote: "ไม่มีรายการอ้างอิง"}, nil
		}
		partyKind := "creditor"
		if kind.Direction == "sale" {
			partyKind = "debtor"
		}
		if in.PartyKind != partyKind || (validTaxID(in.TaxID) != "" && validTaxID(in.TaxID) != validTaxID(kind.Counterparty.TaxID)) {
			in.PartyCode = ""
		}
		in.PartyName, in.TaxID, in.PartyKind = kind.Counterparty.Name, kind.Counterparty.TaxID, partyKind
	}

	s := b.suggestBooking(in)
	s.BillType, s.BillTypeReason, s.BillTypeMatchedBy, s.DocDate, s.Shop = kind.Direction, kind.Reason, kind.MatchedBy, day, &profile
	s.AppliedKnowledge = []KnowledgeItem{}
	if s.Known {
		s.Source = "learned"
	}
	format := strings.TrimSpace(in.DocFormat)
	if format == "" && s.Known {
		format = s.DocFormat
	}
	pp := partyPicked{code: strings.TrimSpace(in.PartyCode), kind: strings.TrimSpace(in.PartyKind), tax: validTaxID(in.TaxID)}
	if s.Party != nil {
		pp = partyPicked{code: s.Party.Code, kind: s.Party.Kind, tax: validTaxID(s.Party.TaxID)}
		if pp.tax == "" {
			pp.tax = validTaxID(in.TaxID)
		}
	}
	if pp.tax == validTaxID(b.Master.Shop.TaxID) {
		pp.tax = ""
	}
	billType := kind.Direction
	if billType == "" && in.Direction == "" {
		billType = billKindForParty(pp.kind)
	}
	bill := knowledgeBill{TaxID: pp.tax, PartyCode: pp.code, PartyKind: pp.kind, Description: in.Description, DocFormat: format, BillType: billType, Date: day}
	rule, overlaps := chooseRule(items, bill)
	if rule != nil {
		if s.Booking != nil {
			prior := vatCheck(Shape{VAT: s.Booking.VAT, WHT: s.Booking.WHT}, in.TotalAmount, in.VatAmount, in.WhtAmount)
			s.Warnings = s.Warnings[:len(s.Warnings)-len(prior)]
		}
		s.Known, s.Source, s.Reason = true, "shop_rule", ""
		s.Level, s.PatternID, s.MatchedBy, s.Seen, s.Consistency = "", "", "กฎของร้าน", 0, 0
		s.Checks, s.AmountNotes, s.TopAccounts = nil, nil, nil
		s.DocFormat = format
		s.AppliedKnowledge = append(s.AppliedKnowledge, *rule)
		s.Booking, s.AmountNotes = b.priceRule(rule.Data, in)
		s.Warnings = append(s.Warnings, warningsForRuleAmounts(rule.Data, in)...)
		if overlaps {
			s.Warnings = append(s.Warnings, "มีกฎซ้อนกัน")
		}
		if len(s.Refs) == 0 {
			b.fillRuleRefs(&s, rule)
		}
		for i := range s.Refs {
			same := refMatchesRule(s.Refs[i], rule.Data)
			s.Refs[i].SameWay = &same
		}
		s.RefsNote = fmt.Sprintf("รายการอ้างอิงที่ร้านเคยบันทึก %d จาก %d รายการ", len(s.Refs), s.RefsTotal)
	} else if s.Booking != nil {
		b.applyRemaps(&s, remapsFor(items, day))
	}
	if len(s.AppliedKnowledge) > 0 {
		applied := map[string]bool{}
		for _, item := range s.AppliedKnowledge {
			applied[item.ID] = true
		}
		for _, warning := range b.knowledgeWarnings(items, applied) {
			s.Warnings = append(s.Warnings, warning.Message)
		}
	}
	return s, nil
}

func billKindForParty(kind string) string {
	switch kind {
	case "creditor":
		return "purchase"
	case "debtor":
		return "sale"
	}
	return ""
}

func (b *Brain) priceRule(rule KnowledgeData, in mcpSuggestInput) (*ShapeView, []string) {
	out := &ShapeView{VAT: rule.VAT, WHT: rule.WHT}
	for _, l := range rule.Lines {
		out.Lines = append(out.Lines, LineView{AccountCode: l.AccountCode, AccountName: b.accountName(l.AccountCode), Side: l.Side})
	}
	out.Accounts = lineSignature(out.Lines)
	total := satang(in.TotalAmount)
	if total <= 0 {
		return out, []string{"ไม่ได้ส่งยอดรวม จึงแสดงเฉพาะบัญชีตามกฎของร้าน"}
	}

	amounts := make([]int64, len(rule.Lines))
	for _, side := range []string{"D", "C"} {
		left := total
		var free []int
		var weights float64
		for i, line := range rule.Lines {
			if line.Side != side {
				continue
			}
			switch line.Basis {
			case "vat":
				amounts[i] = satang(in.VatAmount)
			case "wht":
				amounts[i] = satang(in.WhtAmount)
			default:
				free = append(free, i)
				weights += max(1, line.Weight)
			}
			left -= amounts[i]
		}
		if left < 0 || len(free) == 0 {
			return out, []string{"ยอดภาษีหรือบรรทัดในกฎไม่สมดุลกับยอดรวม จึงแสดงเฉพาะบัญชี กรุณาตรวจตัวเลขและกฎ"}
		}
		for _, i := range free {
			weight := max(1, rule.Lines[i].Weight)
			amounts[i] = min(left, int64(math.Round(float64(left)*weight/weights)))
			left -= amounts[i]
			weights -= weight
		}
		amounts[free[len(free)-1]] += left
	}
	for i, amount := range amounts {
		out.Lines[i].Amount = baht(amount)
	}
	return out, []string{"คำนวณยอดตามกฎของร้าน โดยใช้ยอดภาษีที่ส่งมาจากบิลและแบ่งยอดคงเหลือตามสัดส่วนของกฎ"}
}

func warningsForRuleAmounts(rule KnowledgeData, in mcpSuggestInput) []string {
	warnings := vatCheck(Shape{VAT: rule.VAT, WHT: rule.WHT}, in.TotalAmount, in.VatAmount, in.WhtAmount)
	for _, check := range []struct {
		basis, label string
		amount       float64
	}{
		{"vat", "VAT", in.VatAmount}, {"wht", "หัก ณ ที่จ่าย", in.WhtAmount},
	} {
		if check.amount <= 0 {
			continue
		}
		found := false
		for _, line := range rule.Lines {
			found = found || line.Basis == check.basis
		}
		if !found {
			warnings = append(warnings, "บิลมียอด"+check.label+" แต่กฎไม่มีบรรทัดแยกภาษีนี้")
		}
	}
	return warnings
}

func lineSignature(lines []LineView) string {
	keys := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		key := line.AccountCode + line.Side
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func (b *Brain) applyRemaps(s *Suggestion, remaps map[string]*KnowledgeItem) {
	used := map[string]bool{}
	for i := range s.Booking.Lines {
		line := &s.Booking.Lines[i]
		r := remaps[line.AccountCode]
		if r == nil {
			continue
		}
		line.AccountCode, line.AccountName = r.Data.ToAccount, b.accountName(r.Data.ToAccount)
		if !used[r.ID] {
			used[r.ID] = true
			s.AppliedKnowledge = append(s.AppliedKnowledge, *r)
			s.AmountNotes = append(s.AmountNotes, fmt.Sprintf("รายการเดิมลง %s ร้านกำหนดให้ใช้ %s ตั้งแต่ %s", r.Data.FromAccount, r.Data.ToAccount, r.Data.EffectiveDate))
		}
	}
	if len(used) > 0 {
		s.Booking.Accounts = lineSignature(s.Booking.Lines)
		for i := range s.Refs {
			same := refSignature(s.Refs[i]) == s.Booking.Accounts
			s.Refs[i].SameWay = &same
		}
	}
}

func refSignature(ref RefJournal) string {
	lines := []LineView{}
	for _, l := range ref.Lines {
		if l.Debit != 0 {
			lines = append(lines, LineView{AccountCode: l.AccountCode, Side: "D"})
		}
		if l.Credit != 0 {
			lines = append(lines, LineView{AccountCode: l.AccountCode, Side: "C"})
		}
	}
	return lineSignature(lines)
}

func ruleShape(d KnowledgeData) Shape {
	lines := make([]LineView, 0, len(d.Lines))
	for _, l := range d.Lines {
		lines = append(lines, LineView{AccountCode: l.AccountCode, Side: l.Side})
	}
	return Shape{Accounts: lineSignature(lines), VAT: d.VAT, WHT: d.WHT}
}

func refMatchesRule(ref RefJournal, d KnowledgeData) bool {
	if refSignature(ref) != ruleShape(d).Accounts || (len(ref.Vats) == 0) != (d.VAT == nil) || (len(ref.Taxes) == 0) != (d.WHT == nil) {
		return false
	}
	return (d.VAT == nil || ref.Vats[0].VatRate == d.VAT.VatRate) && (d.WHT == nil || len(ref.Taxes[0].Lines) > 0 && ref.Taxes[0].Lines[0].TaxRate == d.WHT.TaxRate)
}

func knowledgeBillForEntry(e entry) knowledgeBill {
	billType := ""
	if e.src != SourceStatement {
		billType = billKindForParty(e.party.Kind)
	}
	return knowledgeBill{TaxID: validTaxID(e.party.TaxID), PartyCode: e.party.Code, PartyKind: e.party.Kind, Description: e.j.AccountDescription, DocFormat: e.j.DocFormat, BillType: billType, Date: dateText(e.j.DocDate)}
}

func (b *Brain) fillRuleRefs(s *Suggestion, rule *KnowledgeItem) {
	match := rule.Data
	match.EffectiveDate = ""
	for i := len(b.entries) - 1; i >= 0; i-- {
		e := b.entries[i]
		if matchesRule(match, knowledgeBillForEntry(e)) {
			s.RefsTotal++
			if len(s.Refs) < refsPerAnswer {
				s.Refs = append(s.Refs, b.refJournal(e))
			}
		}
	}
}

type KnowledgeWarning struct {
	KnowledgeID string `json:"knowledge_id"`
	DocNo       string `json:"docno"`
	Guid        string `json:"guidfixed"`
	Message     string `json:"message"`
}

// knowledgeWarnings shows the latest conflict for each effective item.
func (b *Brain) knowledgeWarnings(items []KnowledgeItem, only map[string]bool) []KnowledgeWarning {
	pending := map[string]bool{}
	for _, item := range items {
		if item.Active && (item.Kind == "rule" || item.Kind == "remap") && (only == nil || only[item.ID]) {
			pending[item.ID] = true
		}
	}
	out := []KnowledgeWarning{}
	if len(pending) == 0 {
		return out
	}
	record := func(item *KnowledgeItem, e entry, message string) {
		if pending[item.ID] {
			out = append(out, KnowledgeWarning{KnowledgeID: item.ID, DocNo: e.j.DocNo, Guid: e.j.GuidFixed, Message: fmt.Sprintf("%s %s", e.j.DocNo, message)})
			delete(pending, item.ID)
		}
	}
	remapsByDate := map[string]map[string]*KnowledgeItem{}
	for i := len(b.entries) - 1; i >= 0 && len(pending) > 0; i-- {
		e := b.entries[i]
		bill := knowledgeBillForEntry(e)
		if bill.Date == "-" {
			continue
		}
		rule, _ := chooseRule(items, bill)
		if rule != nil {
			if pending[rule.ID] && e.shape.fullKey() != ruleShape(rule.Data).fullKey() {
				record(rule, e, "ลงบัญชีต่างจากกฎที่มีผลในวันที่บิล")
			}
			continue
		}
		remaps, exists := remapsByDate[bill.Date]
		if !exists {
			remaps = remapsFor(items, bill.Date)
			remapsByDate[bill.Date] = remaps
		}
		for _, line := range e.lines {
			if r := remaps[line.code]; r != nil {
				record(r, e, fmt.Sprintf("ยังใช้บัญชี %s หลังวันที่เปลี่ยนเป็น %s", r.Data.FromAccount, r.Data.ToAccount))
			}
		}
	}
	return out
}
