package shopbrain

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type KnowledgeItem struct {
	ID        string        `json:"id" bson:"_id"`
	ShopID    string        `json:"shopid" bson:"shopid"`
	Kind      string        `json:"kind" bson:"kind"`
	Data      KnowledgeData `json:"data" bson:"data"`
	Note      string        `json:"note" bson:"note"`
	Active    bool          `json:"active" bson:"active"`
	CreatedAt time.Time     `json:"created_at" bson:"created_at"`
	CreatedBy string        `json:"created_by" bson:"created_by"`
	UpdatedAt time.Time     `json:"updated_at" bson:"updated_at"`
	UpdatedBy string        `json:"updated_by" bson:"updated_by"`
	Revision  int64         `json:"revision" bson:"revision"`
}

type KnowledgeLine struct {
	AccountCode string  `json:"accountcode" bson:"accountcode"`
	Side        string  `json:"side" bson:"side"`
	Basis       string  `json:"basis,omitempty" bson:"basis,omitempty"`
	Weight      float64 `json:"weight,omitempty" bson:"weight,omitempty"`
}

type KnowledgeData struct {
	Name          string          `json:"name,omitempty" bson:"name,omitempty"`
	Text          string          `json:"text,omitempty" bson:"text,omitempty"`
	PartyTaxID    string          `json:"party_taxid,omitempty" bson:"party_taxid,omitempty"`
	PartyCode     string          `json:"party_code,omitempty" bson:"party_code,omitempty"`
	PartyKind     string          `json:"party_kind,omitempty" bson:"party_kind,omitempty"`
	Contains      string          `json:"contains,omitempty" bson:"contains,omitempty"`
	DocFormat     string          `json:"docformat,omitempty" bson:"docformat,omitempty"`
	BillType      string          `json:"bill_type,omitempty" bson:"bill_type,omitempty"`
	EffectiveDate string          `json:"effective_date,omitempty" bson:"effective_date,omitempty"`
	Lines         []KnowledgeLine `json:"lines,omitempty" bson:"lines,omitempty"`
	VAT           *VatInfo        `json:"vat,omitempty" bson:"vat,omitempty"`
	WHT           *WhtInfo        `json:"wht,omitempty" bson:"wht,omitempty"`
	FromAccount   string          `json:"from_account,omitempty" bson:"from_account,omitempty"`
	ToAccount     string          `json:"to_account,omitempty" bson:"to_account,omitempty"`
}

func knowledgeAliases(items []KnowledgeItem) []string {
	out := []string{}
	for _, item := range items {
		if item.Active && item.Kind == "alias" {
			out = append(out, item.Data.Name)
		}
	}
	return out
}

func validateKnowledge(kind string, data *KnowledgeData, chart map[string]Account) error {
	for _, field := range []*string{&data.Name, &data.Text, &data.PartyTaxID, &data.PartyCode, &data.PartyKind, &data.Contains, &data.DocFormat, &data.BillType, &data.EffectiveDate, &data.FromAccount, &data.ToAccount} {
		*field = strings.TrimSpace(*field)
		if utf8.RuneCountInString(*field) > 1000 {
			return errors.New("ข้อมูลแต่ละช่องต้องไม่เกิน 1,000 ตัวอักษร")
		}
	}
	switch kind {
	case "alias":
		if normalizeName(data.Name) == "" || utf8.RuneCountInString(data.Name) > 250 {
			return errors.New("กรุณาระบุชื่อเรียกอื่นไม่เกิน 250 ตัวอักษร")
		}
		*data = KnowledgeData{Name: data.Name}
		return nil
	case "note":
		if data.Text == "" {
			return errors.New("กรุณาระบุหมายเหตุนโยบาย")
		}
		*data = KnowledgeData{Text: data.Text}
		return nil
	case "rule", "remap":
		day, err := parseDay(data.EffectiveDate)
		if err != nil {
			return errors.New("กรุณาระบุวันที่มีผลในรูปแบบ YYYY-MM-DD")
		}
		data.EffectiveDate = dateText(day)
	default:
		return errors.New("ประเภทความรู้ไม่ถูกต้อง")
	}
	if kind == "remap" {
		_, from := chart[data.FromAccount]
		_, to := chart[data.ToAccount]
		if !from || !to || data.FromAccount == data.ToAccount {
			return errors.New("บัญชีเดิมและบัญชีใหม่ต้องต่างกันและมีในผังบัญชีของร้าน")
		}
		*data = KnowledgeData{FromAccount: data.FromAccount, ToAccount: data.ToAccount, EffectiveDate: data.EffectiveDate}
		return nil
	}

	data.Name, data.Text, data.FromAccount, data.ToAccount = "", "", "", ""
	if data.PartyTaxID != "" {
		data.PartyTaxID = validTaxID(data.PartyTaxID)
		if data.PartyTaxID == "" {
			return errors.New("เลขผู้เสียภาษีคู่ค้าต้องมี 13 หลัก")
		}
	}
	if (data.PartyKind != "" && data.PartyKind != "creditor" && data.PartyKind != "debtor") || (data.PartyKind != "" && data.PartyCode == "") {
		return errors.New("กรุณาระบุประเภทและรหัสคู่ค้าให้ถูกต้อง")
	}
	if data.BillType != "" && data.BillType != "purchase" && data.BillType != "sale" {
		return errors.New("ประเภทบิลต้องเป็นบิลซื้อหรือบิลขาย")
	}
	if ruleSpecificity(*data) == 0 {
		return errors.New("กรุณาระบุเงื่อนไขของกฎอย่างน้อยหนึ่งข้อ")
	}
	if len(data.Lines) > 40 {
		return errors.New("กฎมีได้ไม่เกิน 40 บรรทัดบัญชี")
	}
	seen, taxLines, remainder := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range data.Lines {
		line := &data.Lines[i]
		line.AccountCode, line.Side, line.Basis = strings.TrimSpace(line.AccountCode), strings.ToUpper(strings.TrimSpace(line.Side)), strings.TrimSpace(line.Basis)
		if _, ok := chart[line.AccountCode]; !ok || (line.Side != "D" && line.Side != "C") {
			return errors.New("ทุกบรรทัดต้องใช้บัญชีในผังร้านและระบุเดบิตหรือเครดิต")
		}
		key := line.AccountCode + line.Side
		if seen[key] {
			return errors.New("บัญชีและด้านเดียวกันต้องไม่ซ้ำในกฎ")
		}
		seen[key] = true
		switch line.Basis {
		case "", "remainder":
			if math.IsNaN(line.Weight) || line.Weight < 0 || line.Weight > 100000 {
				return errors.New("สัดส่วนบรรทัดบัญชีไม่ถูกต้อง")
			}
			remainder[line.Side] = true
		case "vat", "wht":
			if taxLines[line.Basis] {
				return errors.New("ระบุบรรทัด VAT และหัก ณ ที่จ่ายได้อย่างละหนึ่งบรรทัด")
			}
			taxLines[line.Basis] = true
		default:
			return errors.New("วิธีคิดยอดบรรทัดบัญชีไม่ถูกต้อง")
		}
	}
	if !remainder["D"] || !remainder["C"] {
		return errors.New("กฎต้องมีบรรทัดรับยอดคงเหลือทั้งเดบิตและเครดิต")
	}
	if data.VAT != nil && (data.VAT.VatRate < 0 || data.VAT.VatRate > 100 || math.IsNaN(data.VAT.VatRate)) {
		return errors.New("อัตรา VAT ไม่ถูกต้อง")
	}
	if data.WHT != nil && (data.WHT.TaxRate < 0 || data.WHT.TaxRate > 100 || math.IsNaN(data.WHT.TaxRate) || utf8.RuneCountInString(data.WHT.IncomeType) > 250) {
		return errors.New("ข้อมูลหัก ณ ที่จ่ายไม่ถูกต้อง")
	}
	return nil
}

type knowledgeBill struct {
	TaxID, PartyCode, PartyKind, Description, DocFormat, BillType, Date string
}

func ruleSpecificity(d KnowledgeData) int {
	score := 0
	for i, value := range []string{d.PartyTaxID, d.PartyCode, d.Contains, d.DocFormat, d.BillType} {
		if value != "" {
			score |= 16 >> i
		}
	}
	return score
}

func matchesRule(d KnowledgeData, bill knowledgeBill) bool {
	return d.EffectiveDate <= bill.Date &&
		(d.PartyTaxID == "" || d.PartyTaxID == bill.TaxID) &&
		(d.PartyCode == "" || d.PartyCode == bill.PartyCode) &&
		(d.PartyKind == "" || d.PartyKind == bill.PartyKind) &&
		(d.Contains == "" || strings.Contains(normalizeDescription(bill.Description), normalizeDescription(d.Contains))) &&
		(d.DocFormat == "" || strings.EqualFold(d.DocFormat, bill.DocFormat)) &&
		(d.BillType == "" || d.BillType == bill.BillType)
}

func laterKnowledge(a, b *KnowledgeItem) bool {
	if a.Data.EffectiveDate != b.Data.EffectiveDate {
		return a.Data.EffectiveDate > b.Data.EffectiveDate
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}

func chooseRule(items []KnowledgeItem, bill knowledgeBill) (*KnowledgeItem, bool) {
	var best *KnowledgeItem
	bestScore, overlap := -1, false
	for i := range items {
		item := &items[i]
		if !item.Active || item.Kind != "rule" || !matchesRule(item.Data, bill) {
			continue
		}
		score := ruleSpecificity(item.Data)
		switch {
		case score > bestScore:
			best, bestScore, overlap = item, score, false
		case score == bestScore:
			overlap = true
			if laterKnowledge(item, best) {
				best = item
			}
		}
	}
	return best, overlap
}

func remapsFor(items []KnowledgeItem, day string) map[string]*KnowledgeItem {
	out := map[string]*KnowledgeItem{}
	for i := range items {
		item := &items[i]
		if !item.Active || item.Kind != "remap" || item.Data.EffectiveDate > day {
			continue
		}
		from := item.Data.FromAccount
		if prev := out[from]; prev == nil || laterKnowledge(item, prev) {
			out[from] = item
		}
	}
	return out
}
