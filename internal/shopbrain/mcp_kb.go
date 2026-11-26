package shopbrain

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The knowledge-base side of the MCP server: every answer carries the shop's
// own saved journals as references, written out in full (document number,
// date, party, every account line with its amount, VAT, withholding tax) so
// the owner can check them without opening anything. Amounts are computed
// here in satang; the AI only reads the bill and presents the result.

var bangkok = time.FixedZone("ICT", 7*3600) // Thailand has no daylight saving

const (
	refsPerAnswer = 5
	refsDefault   = 10
	refsMax       = 30
)

// ---------- money ----------

// satang rounds half away from zero on the decimal digits as written:
// 4.515 is 4.51499… as a float, so v*100 would round down. Shifting the
// decimal point in the number's shortest text form ("4.515e2") does not.
func satang(v float64) int64 {
	f, err := strconv.ParseFloat(strconv.FormatFloat(v, 'g', -1, 64)+"e2", 64)
	if err != nil {
		f = v * 100
	}
	return int64(math.Round(f))
}

func baht(s int64) float64 { return float64(s) / 100 }

// money formats an amount as 1,234.56.
func money(v float64) string {
	s := satang(v)
	neg := s < 0
	if neg {
		s = -s
	}
	whole, frac := s/100, s%100
	digits := fmt.Sprintf("%d", whole)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s.%02d", b.String(), frac)
	if neg {
		out = "-" + out
	}
	return out
}

func dateText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(bangkok).Format("2006-01-02")
}

// parseDay reads YYYY-MM-DD (a Buddhist year such as 2569 is accepted).
func parseDay(s string) (time.Time, error) {
	raw := strings.TrimSpace(s)
	if len(raw) == len("2006-01-02") {
		if year, err := strconv.Atoi(raw[:4]); err == nil && year > 2400 {
			raw = fmt.Sprintf("%04d%s", year-543, raw[4:])
		}
	}
	t, err := time.ParseInLocation("2006-01-02", raw, bangkok)
	if err != nil {
		return time.Time{}, fmt.Errorf("วันที่ %q ต้องเป็นรูปแบบ YYYY-MM-DD", s)
	}
	return t, nil
}

func sourceText(s Source) string {
	switch s {
	case SourceImage:
		return "บิลมีรูป"
	case SourceStatement:
		return "Statement ธนาคาร"
	}
	return "บันทึกเอง (ไม่มีรูป)"
}

// ---------- reference journal ----------

type RefParty struct {
	Kind  string `json:"kind"` // creditor | debtor
	Code  string `json:"code"`
	Name  string `json:"name"`
	TaxID string `json:"taxid,omitempty"`
}

type RefLine struct {
	AccountCode string  `json:"accountcode"`
	AccountName string  `json:"accountname"`
	Debit       float64 `json:"debit,omitempty"`
	Credit      float64 `json:"credit,omitempty"`
}

type RefVat struct {
	VatMode   int     `json:"vatmode"`
	VatType   int     `json:"vattype"`
	VatRate   float64 `json:"vatrate"`
	VatBase   float64 `json:"vatbase"`
	VatAmount float64 `json:"vatamount"`
	ExceptVat float64 `json:"exceptvat,omitempty"`
	TaxID     string  `json:"taxid,omitempty"`
	Name      string  `json:"name,omitempty"`
}

type RefTaxLine struct {
	TaxBase     float64 `json:"taxbase"`
	TaxRate     float64 `json:"taxrate"`
	TaxAmount   float64 `json:"taxamount"`
	Description string  `json:"description,omitempty"`
}

type RefTax struct {
	TaxType          int          `json:"taxtype"`
	ConditionTaxType int          `json:"conditiontaxtype"`
	TaxID            string       `json:"taxid,omitempty"`
	TaxAmount        float64      `json:"taxamount"`
	Lines            []RefTaxLine `json:"lines,omitempty"`
}

// RefJournal is one saved journal as the owner would read it in the books.
type RefJournal struct {
	DocNo       string    `json:"docno"`
	DocDate     string    `json:"docdate"` // YYYY-MM-DD, Thai time
	GuidFixed   string    `json:"guidfixed"`
	DocFormat   string    `json:"docformat,omitempty"`
	BookCode    string    `json:"bookcode,omitempty"`
	Description string    `json:"description,omitempty"`
	Source      Source    `json:"source"`
	SourceText  string    `json:"source_text"`
	Party       *RefParty `json:"party,omitempty"`
	Lines       []RefLine `json:"lines"`
	Total       float64   `json:"total"` // sum of debits
	Vats        []RefVat  `json:"vats,omitempty"`
	Taxes       []RefTax  `json:"taxes,omitempty"`
	Images      int       `json:"images,omitempty"` // bill images kept with the journal
	SameWay     *bool     `json:"same_as_suggestion,omitempty"`
}

func round2(v float64) float64 { return baht(satang(v)) }

func (b *Brain) refJournal(e entry) RefJournal {
	r := RefJournal{
		DocNo: e.j.DocNo, DocDate: dateText(e.j.DocDate), GuidFixed: e.j.GuidFixed, DocFormat: e.j.DocFormat, BookCode: e.j.BookCode,
		Description: strings.TrimSpace(e.j.AccountDescription), Source: e.src, SourceText: sourceText(e.src), Total: round2(e.total),
	}
	if e.party.Code != "" || e.party.TaxID != "" {
		r.Party = &RefParty{Kind: e.party.Kind, Code: e.party.Code, Name: e.party.Name, TaxID: e.party.TaxID}
	}
	for _, d := range e.j.Details {
		if d.AccountCode == "" || (satang(d.Debit) == 0 && satang(d.Credit) == 0) {
			continue
		}
		name := b.accountName(d.AccountCode)
		if name == "" {
			name = d.AccountName
		}
		r.Lines = append(r.Lines, RefLine{AccountCode: d.AccountCode, AccountName: name, Debit: round2(d.Debit), Credit: round2(d.Credit)})
	}
	sort.SliceStable(r.Lines, func(a, c int) bool { return r.Lines[a].Debit > 0 && r.Lines[c].Debit == 0 })
	for _, v := range e.j.Vats {
		if satang(v.VatBase) == 0 && satang(v.VatAmount) == 0 {
			continue
		}
		r.Vats = append(r.Vats, RefVat{VatMode: v.VatMode, VatType: v.VatType, VatRate: v.VatRate, VatBase: round2(v.VatBase),
			VatAmount: round2(v.VatAmount), ExceptVat: round2(v.ExceptVat), TaxID: v.CustTaxID, Name: v.CustName})
	}
	for _, t := range e.j.Taxes {
		if satang(t.TaxAmount) == 0 {
			continue
		}
		rt := RefTax{TaxType: t.TaxType, ConditionTaxType: t.ConditionTaxType, TaxID: t.CustTaxID, TaxAmount: round2(t.TaxAmount)}
		for _, d := range t.Details {
			rt.Lines = append(rt.Lines, RefTaxLine{TaxBase: round2(d.TaxBase), TaxRate: d.TaxRate, TaxAmount: round2(d.TaxAmount), Description: d.Description})
		}
		r.Taxes = append(r.Taxes, rt)
	}
	if e.group != nil {
		for _, im := range e.group.ImageReferences {
			if im.ImageURI != "" {
				r.Images++
			}
		}
	}
	return r
}

func rateText(r float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", r), "0"), ".")
}

// refText writes a reference the way it is read in the books.
func refText(label string, r RefJournal) string {
	var b strings.Builder
	head := []string{r.DocNo, r.DocDate}
	if r.Party != nil {
		p := strings.TrimSpace(r.Party.Name)
		id := r.Party.Code
		if r.Party.TaxID != "" {
			id = strings.TrimSpace(id + " เลขผู้เสียภาษี " + r.Party.TaxID)
		}
		if id != "" {
			p = strings.TrimSpace(p + " (" + id + ")")
		}
		head = append(head, p)
	}
	head = append(head, r.SourceText)
	if r.Images > 0 {
		head = append(head, fmt.Sprintf("มีรูปบิล %d รูป", r.Images))
	}
	fmt.Fprintf(&b, "%s: %s\n", label, strings.Join(head, " • "))
	if r.Description != "" {
		fmt.Fprintf(&b, "  คำอธิบาย: %s\n", r.Description)
	}
	switch {
	case r.DocFormat != "" && r.BookCode != "":
		fmt.Fprintf(&b, "  ประเภทเอกสาร %s • สมุด %s\n", r.DocFormat, r.BookCode)
	case r.DocFormat != "":
		fmt.Fprintf(&b, "  ประเภทเอกสาร %s\n", r.DocFormat)
	case r.BookCode != "":
		fmt.Fprintf(&b, "  สมุด %s\n", r.BookCode)
	}
	for _, l := range r.Lines {
		if l.Debit != 0 {
			fmt.Fprintf(&b, "  Dr %s %s %s\n", l.AccountCode, l.AccountName, money(l.Debit))
		}
		if l.Credit != 0 {
			fmt.Fprintf(&b, "  Cr %s %s %s\n", l.AccountCode, l.AccountName, money(l.Credit))
		}
	}
	for _, v := range r.Vats {
		fmt.Fprintf(&b, "  VAT %s%% ฐาน %s ภาษี %s", rateText(v.VatRate), money(v.VatBase), money(v.VatAmount))
		if v.ExceptVat != 0 {
			fmt.Fprintf(&b, " ยกเว้น %s", money(v.ExceptVat))
		}
		b.WriteString("\n")
	}
	for _, t := range r.Taxes {
		parts := []string{}
		for _, d := range t.Lines {
			p := fmt.Sprintf("%s%% ฐาน %s ภาษี %s", rateText(d.TaxRate), money(d.TaxBase), money(d.TaxAmount))
			if d.Description != "" {
				p = d.Description + " " + p
			}
			parts = append(parts, p)
		}
		if len(parts) == 0 {
			parts = append(parts, "ภาษี "+money(t.TaxAmount))
		}
		fmt.Fprintf(&b, "  หัก ณ ที่จ่าย: %s\n", strings.Join(parts, "; "))
	}
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// precedents lists the journals learned under a key, newest first.
func (b *Brain) precedents(key string) []int {
	var out []int
	for i := len(b.entries) - 1; i >= 0; i-- {
		if hasKey(b.entries[i].keys, key) {
			out = append(out, i)
		}
	}
	return out
}

// ---------- suggest_booking ----------

type mcpSuggestInput struct {
	SellerName  string `json:"seller_name,omitempty" jsonschema:"ชื่อผู้ขายจากหัวบิล"`
	SellerTaxID string `json:"seller_taxid,omitempty" jsonschema:"เลขผู้เสียภาษีผู้ขายจากหัวบิล"`
	BuyerName   string `json:"buyer_name,omitempty" jsonschema:"ชื่อผู้ซื้อจากหัวบิล"`
	BuyerTaxID  string `json:"buyer_taxid,omitempty" jsonschema:"เลขผู้เสียภาษีผู้ซื้อจากหัวบิล"`
	DocDate     string `json:"doc_date,omitempty" jsonschema:"วันที่บนบิล YYYY-MM-DD รองรับปี พ.ศ. ถ้าไม่ระบุใช้วันที่ปัจจุบันเวลาไทย"`

	PartyName   string  `json:"party_name,omitempty" jsonschema:"ชื่อคู่ค้าตามที่อ่านจากบิล (ผู้ขายถ้าเป็นบิลซื้อ / ลูกค้าถ้าเป็นบิลขาย)"`
	TaxID       string  `json:"taxid,omitempty" jsonschema:"เลขผู้เสียภาษี 13 หลักของคู่ค้า (ไม่ใช่ของร้านเอง)"`
	PartyCode   string  `json:"party_code,omitempty" jsonschema:"รหัสคู่ค้า ถ้ารู้แล้วจาก search_parties"`
	PartyKind   string  `json:"party_kind,omitempty" jsonschema:"creditor (ผู้ขาย/เจ้าหนี้) หรือ debtor (ลูกค้า/ลูกหนี้)"`
	Description string  `json:"description,omitempty" jsonschema:"รายการสินค้า/บริการโดยย่อ เช่น ค่าไฟฟ้า ค่าขนส่ง หรือข้อความจาก Statement"`
	DocFormat   string  `json:"docformat,omitempty" jsonschema:"รหัสประเภทเอกสาร ถ้าไม่รู้ไม่ต้องใส่ ระบบจะลองประเภทที่ร้านเคยใช้กับคู่ค้านี้"`
	BookCode    string  `json:"bookcode,omitempty" jsonschema:"รหัสสมุดบัญชี (ถ้ารู้)"`
	TotalAmount float64 `json:"total_amount,omitempty" jsonschema:"ยอดรวมทั้งบิล รวม VAT ก่อนหัก ณ ที่จ่าย"`
	VatAmount   float64 `json:"vat_amount,omitempty" jsonschema:"ยอด VAT ตามบิล (ถ้ามี)"`
	WhtAmount   float64 `json:"wht_amount,omitempty" jsonschema:"ยอดภาษีหัก ณ ที่จ่าย (ถ้ามี)"`
	Direction   string  `json:"direction,omitempty" jsonschema:"เฉพาะ Statement ธนาคาร: in = เงินเข้า, out = เงินออก"`
}

type OtherWay struct {
	Shape ShapeView `json:"shape"`
	Count int       `json:"count"`
}

type Suggestion struct {
	BillType          string          `json:"bill_type,omitempty"`
	BillTypeReason    string          `json:"bill_type_reason,omitempty"`
	BillTypeMatchedBy string          `json:"bill_type_matched_by,omitempty"`
	DocDate           string          `json:"doc_date,omitempty"`
	Source            string          `json:"source,omitempty"`
	Shop              *ShopProfile    `json:"shop,omitempty"`
	AppliedKnowledge  []KnowledgeItem `json:"applied_knowledge"`

	Known           bool         `json:"known"`
	Level           Level        `json:"level,omitempty"`
	MatchedBy       string       `json:"matched_by,omitempty"` // the key in words
	PatternID       string       `json:"pattern_id,omitempty"`
	Seen            int          `json:"seen,omitempty"`        // journals under the key
	Consistency     float64      `json:"consistency,omitempty"` // share booked the winning way (recency-weighted)
	Party           *PartyMatch  `json:"party,omitempty"`
	PartyCandidates []PartyMatch `json:"party_candidates,omitempty"`
	DocFormat       string       `json:"docformat"`
	DocFormatsTried []string     `json:"docformats_tried,omitempty"`
	Booking         *ShapeView   `json:"booking,omitempty"`
	AmountNotes     []string     `json:"amount_notes,omitempty"`
	Warnings        []string     `json:"warnings,omitempty"`
	Reason          string       `json:"reason,omitempty"`
	Checks          []LevelCheck `json:"level_checks,omitempty"`
	OtherWays       []OtherWay   `json:"other_ways,omitempty"`
	RefsNote        string       `json:"refs_note"`
	RefsTotal       int          `json:"refs_total"`
	Refs            []RefJournal `json:"refs"`
	TopAccounts     []mcpAccount `json:"top_accounts,omitempty"`
}

type partyPicked struct{ kind, code, tax string }

// resolveParty turns what the AI read from the bill into a party code: a
// tax ID that belongs to one party, or a name that matches one clearly.
func (b *Brain) resolveParty(in mcpSuggestInput, s *Suggestion) partyPicked {
	pp := partyPicked{kind: strings.TrimSpace(in.PartyKind), code: strings.TrimSpace(in.PartyCode), tax: validTaxID(in.TaxID)}
	if pp.tax != "" && pp.tax == validTaxID(b.Master.Shop.TaxID) {
		s.Warnings = append(s.Warnings, "เลขผู้เสียภาษีที่ส่งมาเป็นของร้านเอง ไม่ใช่ของคู่ค้า จึงไม่ใช้ค้นหา — ส่งเลขของผู้ขาย (บิลซื้อ) หรือลูกค้า (บิลขาย)")
		pp.tax = ""
	}
	pick := func(m PartyMatch) {
		mm := m
		s.Party = &mm
		pp.kind, pp.code = m.Kind, m.Code
		if pp.tax == "" {
			pp.tax = validTaxID(m.TaxID)
		}
	}
	if pp.code != "" {
		for _, p := range append(append([]Party{}, b.Master.Creditors...), b.Master.Debtors...) {
			if p.Code == pp.code && (pp.kind == "" || p.Kind == pp.kind) {
				pick(PartyMatch{Kind: p.Kind, Code: p.Code, Name: p.Name, TaxID: p.TaxID, Score: 1, Reason: "รหัสที่ส่งมา"})
				return pp
			}
		}
		s.Warnings = append(s.Warnings, fmt.Sprintf("ไม่พบรหัสคู่ค้า %q ในทะเบียนของร้าน", pp.code))
		return pp
	}
	if pp.tax != "" {
		var same []PartyMatch
		for _, m := range MatchParties(b.Master, pp.tax, 10) {
			if m.Score >= 1 && (pp.kind == "" || m.Kind == pp.kind) {
				same = append(same, m)
			}
		}
		switch {
		case len(same) == 1:
			pick(same[0])
			return pp
		case len(same) > 1:
			s.PartyCandidates = same
			s.Warnings = append(s.Warnings, fmt.Sprintf("เลขผู้เสียภาษีนี้มีคู่ค้า %d รหัส จึงจำจากเลขผู้เสียภาษีอย่างเดียว ถ้ารู้ว่าเป็นรหัสไหน ส่ง party_code มาอีกครั้ง", len(same)))
			return pp
		}
	}
	if name := strings.TrimSpace(in.PartyName); name != "" {
		ms := MatchParties(b.Master, name, 5)
		if pp.kind != "" {
			filtered := ms[:0]
			for _, match := range ms {
				if match.Kind == pp.kind {
					filtered = append(filtered, match)
				}
			}
			ms = filtered
		}
		if len(ms) > 0 && ms[0].Score >= 0.95 && (len(ms) == 1 || ms[1].Score < ms[0].Score) {
			pick(ms[0])
			return pp
		}
		if len(ms) > 0 {
			s.PartyCandidates = ms
			s.Warnings = append(s.Warnings, "ชื่อคู่ค้าตรงกับทะเบียนไม่ชัด ดู party_candidates แล้วถามผู้ใช้ว่าเป็นรายไหน จากนั้นส่ง party_code มาอีกครั้ง")
		} else if pp.tax == "" {
			s.Warnings = append(s.Warnings, fmt.Sprintf("ไม่พบคู่ค้า %q ในทะเบียนของร้าน (อาจเป็นคู่ค้ารายใหม่)", name))
		}
	}
	return pp
}

// docFormatsFor lists the document formats worth trying when the AI does not
// know one: those the shop used with this party, else with this description,
// else its most used ones.
func (b *Brain) docFormatsFor(pp partyPicked, desc string) []string {
	weight := map[string]int{}
	add := func(p *Pattern) { weight[p.DocFormat] += p.Total }
	for _, p := range b.Patterns {
		if (pp.tax != "" && p.TaxID == pp.tax && p.Level == LevelTaxID) ||
			(pp.code != "" && p.Level == LevelParty && p.PartyCode == pp.code && (pp.kind == "" || p.PartyKind == pp.kind)) {
			add(p)
		}
	}
	if len(weight) == 0 && desc != "" {
		nd := normalizeDescription(desc)
		for _, p := range b.Patterns {
			if p.Level == LevelDesc && p.Description == nd {
				add(p)
			}
		}
	}
	if len(weight) == 0 {
		for _, p := range b.Patterns {
			if p.Level == LevelFormat {
				add(p)
			}
		}
	}
	out := make([]string, 0, len(weight))
	for f := range weight {
		out = append(out, f)
	}
	sort.Slice(out, func(a, c int) bool {
		if weight[out[a]] != weight[out[c]] {
			return weight[out[a]] > weight[out[c]]
		}
		return out[a] < out[c]
	})
	if len(out) > 4 {
		out = out[:4]
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func (b *Brain) suggestBooking(in mcpSuggestInput) Suggestion {
	var s Suggestion
	in.Direction = strings.ToLower(strings.TrimSpace(in.Direction))
	pp := b.resolveParty(in, &s)
	formats := []string{strings.TrimSpace(in.DocFormat)}
	if formats[0] == "" {
		formats = b.docFormatsFor(pp, in.Description)
		s.DocFormatsTried = formats
	}

	type tryRes struct {
		df   string
		keys []ladderKey
		ans  AskAnswer
	}
	var best *tryRes
	for _, df := range formats {
		ai := AskInput{PartyKind: pp.kind, PartyCode: pp.code, TaxID: pp.tax, DocFormat: df, BookCode: strings.TrimSpace(in.BookCode),
			Description: in.Description, Direction: in.Direction}
		keys := b.askLadder(ai)
		r := &tryRes{df: df, keys: keys, ans: b.askKeys(ai, keys)}
		switch {
		case best == nil:
			best = r
		case r.ans.Known && !best.ans.Known:
			best = r
		case r.ans.Known && best.ans.Known && (levelRank(r.ans.Level) < levelRank(best.ans.Level) ||
			(r.ans.Level == best.ans.Level && r.ans.Support > best.ans.Support)):
			best = r
		}
	}
	ans := best.ans
	s.DocFormat, s.Checks, s.Reason = best.df, ans.Checks, ans.Reason

	if ans.Known && ans.Pattern != nil {
		p := ans.Pattern
		s.Known, s.Level, s.PatternID, s.Seen, s.Consistency = true, ans.Level, p.ID, p.Total, round4(p.Consistency)
		s.MatchedBy = levelText(p.Level) + ": " + patternSubjectText(p)
		sv, notes := b.bookLines(p, in.TotalAmount, in.VatAmount, in.WhtAmount)
		s.Booking, s.AmountNotes = &sv, notes
		s.Warnings = append(s.Warnings, vatCheck(p.Winner.Shape, in.TotalAmount, in.VatAmount, in.WhtAmount)...)
		for _, o := range p.Others {
			if len(s.OtherWays) == 3 {
				break
			}
			s.OtherWays = append(s.OtherWays, OtherWay{Shape: b.variantView(o, 0), Count: o.Count})
		}
		b.fillRefs(&s, p, p.Winner.Shape.Accounts)
		s.RefsNote = fmt.Sprintf("รายการที่ร้านเคยบันทึกด้วย%s ล่าสุด %d จาก %d รายการ", s.MatchedBy, len(s.Refs), s.RefsTotal)
		return s
	}

	// unknown: show what the shop did before under the strongest key, as
	// reference only — the brain does not pick one
	for _, k := range best.keys {
		if p, _ := b.lookup(k.key, in.Direction); p != nil && p.Winner != nil {
			s.MatchedBy = levelText(p.Level) + ": " + patternSubjectText(p)
			s.Seen = p.Total
			s.OtherWays = append(s.OtherWays, OtherWay{Shape: b.variantView(p.Winner, 0), Count: p.Winner.Count})
			for _, o := range p.Others {
				if len(s.OtherWays) == 4 {
					break
				}
				s.OtherWays = append(s.OtherWays, OtherWay{Shape: b.variantView(o, 0), Count: o.Count})
			}
			b.fillRefs(&s, p, "")
			why := "แต่ลงหลายแบบ ไม่สม่ำเสมอพอ"
			if len(p.Others) == 0 {
				// one way only, but too few journals to pass the bar
				why = fmt.Sprintf("ลงแบบเดียวกันทุกครั้ง แต่จำนวนยังน้อยเกินกว่าจะมั่นใจ (เกณฑ์ความมั่นใจ %.0f%%)", b.Options.minConsistency(p.Level)*100)
			}
			s.RefsNote = fmt.Sprintf("เคยบันทึก%s %d รายการ %s (ไม่ผ่านเกณฑ์) — ใช้ดูประกอบเท่านั้น ห้ามเลือกแบบใดแบบหนึ่งแทนผู้ใช้", s.MatchedBy, s.RefsTotal, why)
			break
		}
	}
	if s.RefsNote == "" {
		s.RefsNote = "ไม่มีรายการอ้างอิง — ร้านยังไม่เคยบันทึกรายการแบบนี้"
		s.TopAccounts = searchAccounts(b, "", 10)
	}
	if s.Refs == nil {
		s.Refs = []RefJournal{}
	}
	return s
}

func (b *Brain) fillRefs(s *Suggestion, p *Pattern, winnerAccounts string) {
	idx := b.precedents(p.Key)
	s.RefsTotal = len(idx)
	for _, i := range idx {
		if len(s.Refs) == refsPerAnswer {
			break
		}
		r := b.refJournal(b.entries[i])
		if winnerAccounts != "" {
			same := b.entries[i].shape.Accounts == winnerAccounts
			r.SameWay = &same
		}
		s.Refs = append(s.Refs, r)
	}
}

// taxLines finds which line of a pattern holds the VAT and which the
// withholding tax: the line whose amount equals the journal's VAT (or WHT)
// in most of the journals under the key.
func (b *Brain) taxLines(key string) (vatLine, whtLine string) {
	vat, wht := map[string]int{}, map[string]int{}
	for _, i := range b.precedents(key) {
		e := b.entries[i]
		var va, wa int64
		if len(e.j.Vats) > 0 {
			va = satang(e.j.Vats[0].VatAmount)
		}
		if len(e.j.Taxes) > 0 {
			wa = satang(e.j.Taxes[0].TaxAmount)
		}
		for _, l := range e.lines {
			a := satang(l.amount)
			if va > 0 && a == va {
				vat[l.code+l.side]++
			}
			if wa > 0 && a == wa {
				wht[l.code+l.side]++
			}
		}
	}
	top := func(m map[string]int) string {
		best, n := "", 0
		for k, c := range m {
			if c > n || (c == n && k < best) {
				best, n = k, c
			}
		}
		return best
	}
	return top(vat), top(wht)
}

// bookLines prices the winning way for this bill in satang: the VAT and
// withholding lines take the amounts read from the bill, the other lines of
// each side share the rest by their average share in earlier journals, and
// any rounding goes to the side's largest line so Dr = Cr = total exactly.
func (b *Brain) bookLines(p *Pattern, total, vat, wht float64) (ShapeView, []string) {
	sv := b.variantView(p.Winner, 0)
	if satang(total) <= 0 {
		return sv, []string{"ไม่ได้ส่ง total_amount จึงแสดงเฉพาะบัญชี ไม่มียอดเงิน"}
	}
	ratio := map[string]float64{}
	for _, l := range p.Winner.Lines {
		ratio[l.AccountCode+l.Side] = l.RatioAvg
	}
	vatLine, whtLine := b.taxLines(p.Key)
	var notes []string
	switch {
	case satang(vat) > 0 && vatLine == "":
		notes = append(notes, "ไม่พบบรรทัด VAT ในรายการเดิม จึงแบ่งยอดตามสัดส่วนเฉลี่ย")
	case satang(vat) == 0 && vatLine != "":
		notes = append(notes, "รายการเดิมมีบรรทัด VAT แต่ไม่ได้ส่ง vat_amount ยอด VAT จึงคิดจากสัดส่วนเฉลี่ย อาจไม่ตรงบิล — ส่ง vat_amount ตามบิลเพื่อให้ตรง")
	}
	if satang(wht) > 0 && whtLine == "" {
		notes = append(notes, "ไม่พบบรรทัดหัก ณ ที่จ่ายในรายการเดิม จึงแบ่งยอดตามสัดส่วนเฉลี่ย")
	}
	amt := make([]int64, len(sv.Lines))
	for _, side := range []string{"D", "C"} {
		rest := satang(total)
		var free []int
		rsum := 0.0
		for i, l := range sv.Lines {
			if l.Side != side {
				continue
			}
			k := l.AccountCode + l.Side
			switch {
			case k == vatLine && satang(vat) > 0:
				amt[i] = satang(vat)
				rest -= amt[i]
			case k == whtLine && satang(wht) > 0:
				amt[i] = satang(wht)
				rest -= amt[i]
			default:
				free = append(free, i)
				rsum += ratio[k]
			}
		}
		if rest < 0 {
			return b.variantView(p.Winner, 0), append(notes, "VAT หรือหัก ณ ที่จ่ายมากกว่ายอดรวม — ตรวจตัวเลขที่อ่านจากบิลอีกครั้ง จึงไม่แสดงยอด")
		}
		if len(free) == 0 {
			if rest != 0 {
				notes = append(notes, fmt.Sprintf("ยอดด้าน %s ไม่ลงตัว ต่าง %s", sideText(side), money(baht(rest))))
			}
			continue
		}
		var used int64
		big := free[0]
		for _, i := range free {
			r := 1.0 / float64(len(free))
			if rsum > 0 {
				r = ratio[sv.Lines[i].AccountCode+side] / rsum
			}
			amt[i] = int64(math.Round(float64(rest) * r))
			used += amt[i]
			if amt[i] > amt[big] {
				big = i
			}
		}
		amt[big] += rest - used
	}
	for i := range sv.Lines {
		sv.Lines[i].Amount = baht(amt[i])
	}
	notes = append([]string{"ยอดแต่ละบรรทัดคำนวณโดยระบบ: VAT/หัก ณ ที่จ่ายใช้ยอดจากบิล บรรทัดอื่นแบ่งตามสัดส่วนเฉลี่ยของรายการเดิม และ Dr = Cr = ยอดรวม"}, notes...)
	return sv, notes
}

func sideText(side string) string {
	if side == "D" {
		return "Dr"
	}
	return "Cr"
}

// vatCheck compares the bill's VAT with the rate the shop used before.
func vatCheck(sh Shape, total, vat, wht float64) []string {
	var out []string
	hasVat := sh.VAT != nil && sh.VAT.VatRate > 0
	switch {
	case satang(vat) > 0 && !hasVat:
		out = append(out, "บิลนี้มี VAT แต่ร้านเคยลงรายการแบบนี้โดยไม่มี VAT — ถามผู้ใช้ก่อน")
	case satang(vat) == 0 && hasVat && satang(total) > 0:
		out = append(out, fmt.Sprintf("ร้านเคยลงรายการแบบนี้มี VAT %s%% แต่ไม่ได้ส่ง vat_amount — ตรวจว่าบิลมี VAT หรือไม่", rateText(sh.VAT.VatRate)))
	case satang(vat) > 0 && hasVat && satang(total) > 0:
		base := satang(total) - satang(vat)
		want := int64(math.Round(float64(base) * sh.VAT.VatRate / 100))
		if d := satang(vat) - want; d > 1 || d < -1 {
			out = append(out, fmt.Sprintf("VAT ตามบิล %s ไม่เท่ากับ %s%% ของยอดก่อน VAT %s (ควรเป็น %s) — ตรวจตัวเลขที่อ่านจากรูปอีกครั้ง",
				money(vat), rateText(sh.VAT.VatRate), money(baht(base)), money(baht(want))))
		}
	}
	if satang(wht) == 0 && sh.WHT != nil && satang(total) > 0 {
		out = append(out, "ร้านเคยหัก ณ ที่จ่ายกับรายการแบบนี้ แต่ไม่ได้ส่ง wht_amount — ตรวจว่ามีการหักหรือไม่")
	}
	return out
}

func shapeLinesText(sv *ShapeView) string {
	var b strings.Builder
	for _, l := range sv.Lines {
		fmt.Fprintf(&b, "  %s %s %s", sideText(l.Side), l.AccountCode, l.AccountName)
		if l.Amount != 0 {
			fmt.Fprintf(&b, " %s", money(l.Amount))
		}
		b.WriteString("\n")
	}
	if sv.VAT != nil {
		fmt.Fprintf(&b, "  มี VAT %s%%\n", rateText(sv.VAT.VatRate))
	}
	if sv.WHT != nil {
		w := "  หัก ณ ที่จ่าย"
		if sv.WHT.TaxRate > 0 {
			w += " " + rateText(sv.WHT.TaxRate) + "%"
		}
		if sv.WHT.IncomeType != "" {
			w += " (" + sv.WHT.IncomeType + ")"
		}
		b.WriteString(w + "\n")
	}
	return b.String()
}

func (s Suggestion) text() string {
	var b strings.Builder
	if s.BillType != "" {
		label := map[string]string{"sale": "บิลขาย", "purchase": "บิลซื้อ", "unknown": "ยังแยกบิลซื้อหรือขายไม่ได้"}[s.BillType]
		fmt.Fprintf(&b, "%s • %s\n", label, s.BillTypeReason)
	}

	if s.Party != nil {
		fmt.Fprintf(&b, "คู่ค้าในทะเบียน: %s (%s %s) — %s\n", s.Party.Name, s.Party.Kind, s.Party.Code, s.Party.Reason)
	}
	if s.Source == "shop_rule" {
		b.WriteString("วิธีลงบัญชีตามกฎของร้าน:\n")
		b.WriteString(shapeLinesText(s.Booking))
	} else if s.Known {
		fmt.Fprintf(&b, "วิธีลงบัญชีที่ร้านนี้ใช้ (จำจาก%s • เคยบันทึก %d รายการ • ลงแบบนี้ %.0f%%):\n", s.MatchedBy, s.Seen, s.Consistency*100)
		b.WriteString(shapeLinesText(s.Booking))
	} else {
		fmt.Fprintf(&b, "สมองยังตอบไม่ได้: %s\n", s.Reason)
		if len(s.OtherWays) > 0 {
			b.WriteString("วิธีที่ร้านเคยลง (ไม่ผ่านเกณฑ์ ให้ผู้ใช้เลือกเอง):\n")
			for n, o := range s.OtherWays {
				fmt.Fprintf(&b, " แบบ %d (%d ครั้ง):\n%s", n+1, o.Count, shapeLinesText(&o.Shape))
			}
		}
	}
	for _, item := range s.AppliedKnowledge {
		if item.Kind != "rule" {
			continue
		}
		fmt.Fprintf(&b, "กฎที่ใช้ %s • มีผลตั้งแต่ %s\n", item.ID, item.Data.EffectiveDate)
	}
	for _, n := range s.AmountNotes {
		b.WriteString("หมายเหตุยอด: " + n + "\n")
	}
	for _, w := range s.Warnings {
		b.WriteString("ข้อควรตรวจ: " + w + "\n")
	}
	if s.Known && len(s.OtherWays) > 0 {
		b.WriteString("วิธีอื่นที่ร้านเคยลงกับเรื่องนี้:\n")
		for _, o := range s.OtherWays {
			fmt.Fprintf(&b, " (%d ครั้ง) %s\n", o.Count, shapeText(&o.Shape))
		}
	}
	b.WriteString("\n" + s.RefsNote + "\n")
	for n, r := range s.Refs {
		label := fmt.Sprintf("อ้างอิง %d", n+1)
		if r.SameWay != nil && !*r.SameWay {
			label += " (ลงต่างจากวิธีที่เสนอ)"
		}
		b.WriteString(refText(label, r))
	}
	if len(s.TopAccounts) > 0 {
		b.WriteString("บัญชีที่ร้านใช้บ่อย: ")
		parts := make([]string, 0, len(s.TopAccounts))
		for _, a := range s.TopAccounts {
			parts = append(parts, fmt.Sprintf("%s %s (%d)", a.Code, a.Name, a.Used))
		}
		b.WriteString(strings.Join(parts, ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------- search_journals / summarize ----------

type journalQuery struct {
	DateFrom  string  `json:"date_from,omitempty" jsonschema:"ตั้งแต่วันที่ YYYY-MM-DD (รับปี พ.ศ. ได้)"`
	DateTo    string  `json:"date_to,omitempty" jsonschema:"ถึงวันที่ YYYY-MM-DD (รวมวันนั้น)"`
	Party     string  `json:"party,omitempty" jsonschema:"ชื่อ รหัส หรือเลขผู้เสียภาษีของคู่ค้า"`
	Account   string  `json:"account,omitempty" jsonschema:"รหัสบัญชี (ขึ้นต้นด้วย) หรือคำในชื่อบัญชี"`
	Text      string  `json:"text,omitempty" jsonschema:"คำในเลขที่เอกสารหรือคำอธิบายรายการ"`
	Source    string  `json:"source,omitempty" jsonschema:"image (บิลมีรูป), no_image (บันทึกเอง) หรือ statement"`
	MinAmount float64 `json:"min_amount,omitempty" jsonschema:"ยอดรวมเอกสารตั้งแต่"`
	MaxAmount float64 `json:"max_amount,omitempty" jsonschema:"ยอดรวมเอกสารไม่เกิน"`
}

type mcpSearchJournalsInput struct {
	journalQuery
	Limit  int `json:"limit,omitempty" jsonschema:"จำนวนรายการที่แสดง (ค่าเริ่ม 10, สูงสุด 30)"`
	Offset int `json:"offset,omitempty" jsonschema:"ข้ามกี่รายการ (หน้าถัดไป)"`
}

type mcpSummarizeInput struct {
	journalQuery
	GroupBy string `json:"group_by,omitempty" jsonschema:"account (ค่าเริ่ม), party, month หรือ docformat"`
	Limit   int    `json:"limit,omitempty" jsonschema:"จำนวนกลุ่มสูงสุด (ค่าเริ่ม 30, สูงสุด 100)"`
}

func (q journalQuery) accountMatch(b *Brain) func(code, name string) bool {
	a := strings.ToLower(strings.TrimSpace(q.Account))
	if a == "" {
		return nil
	}
	return func(code, name string) bool {
		if strings.HasPrefix(strings.ToLower(code), a) {
			return true
		}
		n := b.accountName(code)
		if n == "" {
			n = name
		}
		return strings.Contains(strings.ToLower(n), a)
	}
}

// filter turns the query into a test on learned journals.
func (q journalQuery) filter(b *Brain) (func(entry) bool, error) {
	var from, to time.Time
	var err error
	if strings.TrimSpace(q.DateFrom) != "" {
		if from, err = parseDay(q.DateFrom); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(q.DateTo) != "" {
		if to, err = parseDay(q.DateTo); err != nil {
			return nil, err
		}
		to = to.AddDate(0, 0, 1)
	}
	src := Source(strings.TrimSpace(q.Source))
	if src != "" && src != SourceImage && src != SourceNoImage && src != SourceStatement {
		return nil, errors.New("source ต้องเป็น image, no_image หรือ statement")
	}
	party := strings.TrimSpace(q.Party)
	ptax, pname := validTaxID(party), normalizeName(party)
	text := strings.ToLower(strings.TrimSpace(q.Text))
	acct := q.accountMatch(b)
	return func(e entry) bool {
		if !from.IsZero() && e.j.DocDate.Before(from) || !to.IsZero() && !e.j.DocDate.Before(to) {
			return false
		}
		if src != "" && e.src != src {
			return false
		}
		if q.MinAmount > 0 && satang(e.total) < satang(q.MinAmount) || q.MaxAmount > 0 && satang(e.total) > satang(q.MaxAmount) {
			return false
		}
		if party != "" {
			ok := (ptax != "" && e.party.TaxID == ptax) || strings.EqualFold(e.party.Code, party) ||
				(pname != "" && strings.Contains(normalizeName(e.party.Name), pname))
			if !ok {
				return false
			}
		}
		if text != "" && !strings.Contains(strings.ToLower(e.j.DocNo+" "+e.j.AccountDescription), text) {
			return false
		}
		if acct != nil {
			hit := false
			for _, d := range e.j.Details {
				if d.AccountCode != "" && acct(d.AccountCode, d.AccountName) {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		return true
	}, nil
}

type JournalSearch struct {
	Matched int          `json:"matched"`
	Total   float64      `json:"total_amount"` // sum of the matched journals' totals
	Offset  int          `json:"offset"`
	Refs    []RefJournal `json:"refs"`
}

func (b *Brain) searchJournals(in mcpSearchJournalsInput) (JournalSearch, error) {
	keep, err := in.filter(b)
	if err != nil {
		return JournalSearch{}, err
	}
	limit := clampLimit(in.Limit, refsDefault, refsMax)
	out := JournalSearch{Offset: max(in.Offset, 0), Refs: []RefJournal{}}
	var sum int64
	var hit []int
	for i, e := range b.entries {
		if keep(e) {
			hit = append(hit, i)
			sum += satang(e.total)
		}
	}
	// newest document date first (entries are in the order they were saved)
	sort.SliceStable(hit, func(a, c int) bool { return b.entries[hit[a]].j.DocDate.After(b.entries[hit[c]].j.DocDate) })
	out.Matched, out.Total = len(hit), baht(sum)
	for n := out.Offset; n < len(hit) && len(out.Refs) < limit; n++ {
		out.Refs = append(out.Refs, b.refJournal(b.entries[hit[n]]))
	}
	return out, nil
}

func (s JournalSearch) text() string {
	var b strings.Builder
	if s.Matched == 0 {
		return "ไม่พบรายการที่ตรงเงื่อนไข"
	}
	fmt.Fprintf(&b, "พบ %d รายการ ยอดรวม %s บาท", s.Matched, money(s.Total))
	if len(s.Refs) > 0 {
		fmt.Fprintf(&b, " • แสดงรายการที่ %d–%d (ใหม่สุดก่อน)", s.Offset+1, s.Offset+len(s.Refs))
	}
	b.WriteString("\n")
	for n, r := range s.Refs {
		b.WriteString(refText(fmt.Sprintf("อ้างอิง %d", s.Offset+n+1), r))
	}
	if more := s.Matched - s.Offset - len(s.Refs); more > 0 {
		fmt.Fprintf(&b, "ยังมีอีก %d รายการ (ใช้ offset %d)\n", more, s.Offset+len(s.Refs))
	}
	return strings.TrimRight(b.String(), "\n")
}

type SummaryGroup struct {
	Key      string  `json:"key"`
	Name     string  `json:"name,omitempty"`
	Journals int     `json:"journals"`
	Debit    float64 `json:"debit,omitempty"`  // group_by account
	Credit   float64 `json:"credit,omitempty"` // group_by account
	Net      float64 `json:"net,omitempty"`    // debit − credit (group_by account)
	Total    float64 `json:"total,omitempty"`  // sum of journal totals (other groupings)
}

type Summary struct {
	GroupBy  string         `json:"group_by"`
	Journals int            `json:"journals"`
	Total    float64        `json:"total_amount"`
	Groups   []SummaryGroup `json:"groups"`
	More     int            `json:"more_groups,omitempty"`
}

func (b *Brain) summarize(in mcpSummarizeInput) (Summary, error) {
	keep, err := in.filter(b)
	if err != nil {
		return Summary{}, err
	}
	by := strings.ToLower(strings.TrimSpace(in.GroupBy))
	if by == "" {
		by = "account"
	}
	if by != "account" && by != "party" && by != "month" && by != "docformat" {
		return Summary{}, errors.New("group_by ต้องเป็น account, party, month หรือ docformat")
	}
	type acc struct {
		name          string
		n             int
		dr, cr, total int64
	}
	groups := map[string]*acc{}
	get := func(k, name string) *acc {
		g := groups[k]
		if g == nil {
			g = &acc{name: name}
			groups[k] = g
		}
		return g
	}
	acct := in.accountMatch(b)
	out := Summary{GroupBy: by}
	var all int64
	for _, e := range b.entries {
		if !keep(e) {
			continue
		}
		out.Journals++
		all += satang(e.total)
		switch by {
		case "account":
			seen := map[string]bool{}
			for _, d := range e.j.Details {
				if d.AccountCode == "" || (acct != nil && !acct(d.AccountCode, d.AccountName)) {
					continue
				}
				name := b.accountName(d.AccountCode)
				if name == "" {
					name = d.AccountName
				}
				g := get(d.AccountCode, name)
				g.dr += satang(d.Debit)
				g.cr += satang(d.Credit)
				if !seen[d.AccountCode] {
					seen[d.AccountCode] = true
					g.n++
				}
			}
		case "party":
			k, name := "-", "ไม่ระบุคู่ค้า"
			if e.party.Code != "" || e.party.TaxID != "" {
				k, name = strings.Trim(e.party.Kind+":"+e.party.Code, ":"), e.party.Name
				if e.party.Code == "" {
					k = "tax:" + e.party.TaxID
				}
			}
			g := get(k, name)
			g.n++
			g.total += satang(e.total)
		case "month":
			g := get(e.j.DocDate.In(bangkok).Format("2006-01"), "")
			g.n++
			g.total += satang(e.total)
		case "docformat":
			g := get(orDash(e.j.DocFormat), "")
			g.n++
			g.total += satang(e.total)
		}
	}
	out.Total = baht(all)
	for k, g := range groups {
		sg := SummaryGroup{Key: k, Name: g.name, Journals: g.n}
		if by == "account" {
			sg.Debit, sg.Credit, sg.Net = baht(g.dr), baht(g.cr), baht(g.dr-g.cr)
		} else {
			sg.Total = baht(g.total)
		}
		out.Groups = append(out.Groups, sg)
	}
	size := func(g SummaryGroup) int64 { return satang(g.Debit) + satang(g.Credit) + satang(g.Total) }
	sort.Slice(out.Groups, func(a, c int) bool {
		ga, gc := out.Groups[a], out.Groups[c]
		if by == "month" {
			return ga.Key < gc.Key
		}
		if size(ga) != size(gc) {
			return size(ga) > size(gc)
		}
		return ga.Key < gc.Key
	})
	if limit := clampLimit(in.Limit, 30, 100); len(out.Groups) > limit {
		out.More = len(out.Groups) - limit
		out.Groups = out.Groups[:limit]
	}
	if out.Groups == nil {
		out.Groups = []SummaryGroup{}
	}
	return out, nil
}

func (s Summary) text() string {
	if s.Journals == 0 {
		return "ไม่พบรายการที่ตรงเงื่อนไข"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "สรุปจาก %d รายการ ยอดรวมเอกสาร %s บาท (แยกตาม %s)\n", s.Journals, money(s.Total), s.GroupBy)
	for _, g := range s.Groups {
		name := strings.TrimSpace(g.Key + " " + g.Name)
		if s.GroupBy == "account" {
			fmt.Fprintf(&b, "- %s: Dr %s • Cr %s • สุทธิ %s (%d รายการ)\n", name, money(g.Debit), money(g.Credit), money(g.Net), g.Journals)
		} else {
			fmt.Fprintf(&b, "- %s: %s บาท (%d รายการ)\n", name, money(g.Total), g.Journals)
		}
	}
	if s.More > 0 {
		fmt.Fprintf(&b, "ยังมีอีก %d กลุ่ม (เพิ่ม limit)\n", s.More)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------- get_journal ----------

func (b *Brain) getJournal(in mcpJournalInput) (string, map[string]any, error) {
	i := journalIndex(b, in.DocNo, in.GuidFixed)
	if i < 0 {
		return "", nil, errors.New("ไม่พบรายการนี้ในข้อมูลที่สมองเรียน (ตรวจเลขที่เอกสาร หรือรายการไม่มีบรรทัดบัญชี)")
	}
	e, g := b.entries[i], b.guesses[i]
	ref := b.refJournal(e)
	gv := b.guessView(g)
	match := g.Known() && g.Accounts == e.shape.Accounts
	out := map[string]any{"journal": ref, "brain_guess": gv, "brain_matches_saved": match, "evidence": b.evidence(i, g)}
	sum := refText("รายการ", ref)
	if g.Known() {
		sum += fmt.Sprintf("สมองจะทาย (รู้เฉพาะรายการก่อนหน้า) จาก%s เคยเห็น %d ครั้ง: %s • %s",
			levelText(g.Level), g.Support, shapeText(gv.Shape), map[bool]string{true: "ตรงกับที่บันทึกไว้", false: "ไม่ตรงกับที่บันทึกไว้"}[match])
	} else {
		sum += "สมองจะตอบไม่ได้ (รู้เฉพาะรายการก่อนหน้า): " + gv.Reason
	}
	return sum, out, nil
}
