package shopbrain

import (
	"strings"
	"testing"
)

func TestKnowledgeDraftValidation(t *testing.T) {
	b := &Brain{chart: map[string]Account{
		"510": {Code: "510", Name: "ค่าขนส่ง"},
		"520": {Code: "520", Name: "ค่าใช้จ่ายเบ็ดเตล็ด"},
		"210": {Code: "210", Name: "เจ้าหนี้การค้า"},
	}, Master: MasterData{Creditors: []Party{{Kind: "creditor", Code: "C1", Name: "บริษัท เอบัญชี จำกัด"}}}}
	valid := func() draftProposal {
		return draftProposal{Kind: "rule", Data: KnowledgeData{PartyKind: "creditor", PartyCode: "C1", BillType: "purchase", Contains: "ค่าขนส่ง", EffectiveDate: "2026-12-01", Lines: []KnowledgeLine{{AccountCode: "510", Side: "D"}, {AccountCode: "210", Side: "C"}}}}
	}
	text := "ตั้งแต่ 1 ธ.ค. 2569 บิลซื้อจากบริษัท เอบัญชี จำกัด ที่มีคำว่า ค่าขนส่ง เดบิต 510 เครดิต 210"
	p := valid()
	result := b.prepareKnowledgeDraft(text, p)
	if result.Kind != "rule" || result.Question != "" || len(result.Data.Lines) != 2 {
		t.Fatalf("expected validated rule: %+v", result)
	}
	p.Data.Lines[0].AccountCode = "520"
	if result := b.prepareKnowledgeDraft(text, p); result.Kind != "" || result.Question == "" {
		t.Fatalf("accepted code absent from the user's text: %+v", result)
	}
	p = valid()
	p.Data.PartyCode = "C9"
	if result := b.prepareKnowledgeDraft(text, p); result.Kind != "" || result.Question == "" {
		t.Fatalf("accepted unknown party: %+v", result)
	}
	p = valid()
	p.Data.EffectiveDate = ""
	if result := b.prepareKnowledgeDraft(text, p); result.Kind != "" || !strings.Contains(result.Question, "วันที่") {
		t.Fatalf("expected effective date clarification: %+v", result)
	}
}

func TestKnowledgeDraftReusesOnlyUnambiguousHistory(t *testing.T) {
	m := MasterData{
		Accounts:  []Account{{Code: "510", Name: "ค่าขนส่ง"}, {Code: "520", Name: "ค่าใช้จ่ายเบ็ดเตล็ด"}, {Code: "210", Name: "เจ้าหนี้การค้า"}, {Code: "110", Name: "เงินสด"}},
		Creditors: []Party{{Kind: "creditor", Code: "C1", Name: "บริษัท เอบัญชี จำกัด"}},
	}
	first := purchase("draft-1", 1, creditor("C1", ""), "520", 100)
	first.AccountDescription = "ค่าขนส่งสินค้า"
	first.Vats = nil
	first.Details = []rawDetail{{AccountCode: "520", Debit: 100}, {AccountCode: "210", Credit: 100}}
	second := first
	second.GuidFixed, second.DocNo, second.DocDate = "draft-2", "PV-draft-2", day(2)
	b := Build(m, []Journal{first, second}, nil, 0, mechanicsOptions(day(10)))
	text := "ตั้งแต่ 1 ธ.ค. 2569 บิลซื้อค่าขนส่งของบริษัท เอบัญชี จำกัด ใช้ค่าขนส่งแทนค่าใช้จ่ายเบ็ดเตล็ด"
	p := draftProposal{Kind: "rule", ReplaceFrom: "520", ReplaceTo: "510", Data: KnowledgeData{PartyKind: "creditor", PartyCode: "C1", BillType: "purchase", Contains: "ค่าขนส่ง", EffectiveDate: "2026-12-01"}}
	result := b.prepareKnowledgeDraft(text, p)
	if result.Kind != "rule" || len(result.Before) != 2 || result.Data.Lines[0].AccountCode != "510" || result.Data.Lines[1].AccountCode != "210" {
		t.Fatalf("expected to reuse the matching shape: %+v", result)
	}
	second.Details = []rawDetail{{AccountCode: "520", Debit: 100}, {AccountCode: "110", Credit: 100}}
	b = Build(m, []Journal{first, second}, nil, 0, mechanicsOptions(day(10)))
	if result := b.prepareKnowledgeDraft(text, p); result.Kind != "" || result.Question == "" {
		t.Fatalf("accepted ambiguous history: %+v", result)
	}
}

func TestDraftErrorLogHidesGoogleKey(t *testing.T) {
	got := redactAPIKey("googleapi: Error 400: API key not valid: AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUvW")
	if strings.Contains(got, "SyA1b2") || !strings.Contains(got, "AIza***") {
		t.Errorf("key not hidden: %s", got)
	}
}
