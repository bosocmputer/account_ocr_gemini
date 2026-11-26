package shopbrain

import (
	"testing"
	"time"
)

func TestKnowledgeRulePrecedence(t *testing.T) {
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, bangkok)
	rules := []KnowledgeItem{
		{ID: "format", Kind: "rule", Active: true, Data: KnowledgeData{DocFormat: "PV"}},
		{ID: "code", Kind: "rule", Active: true, Data: KnowledgeData{PartyCode: "C1"}},
		{ID: "tax", Kind: "rule", Active: true, Data: KnowledgeData{PartyTaxID: "0105500000001"}},
		{ID: "tax-purchase-old", Kind: "rule", Active: true, Data: KnowledgeData{PartyTaxID: "0105500000001", BillType: "purchase", EffectiveDate: "2026-11-01"}},
		{ID: "tax-purchase", Kind: "rule", Active: true, Data: KnowledgeData{PartyTaxID: "0105500000001", BillType: "purchase", EffectiveDate: "2026-11-01"}, UpdatedAt: at},
		{ID: "future", Kind: "rule", Active: true, Data: KnowledgeData{PartyTaxID: "0105500000001", BillType: "purchase", EffectiveDate: "2026-12-01"}},
	}
	got, overlap := chooseRule(rules, knowledgeBill{TaxID: "0105500000001", PartyCode: "C1", DocFormat: "PV", BillType: "purchase", Date: "2026-11-25"})
	if got == nil || got.ID != "tax-purchase" || !overlap {
		t.Fatalf("got %+v, overlap %v", got, overlap)
	}
	got, overlap = chooseRule(rules, knowledgeBill{TaxID: "0105500000001", BillType: "purchase", Date: "2026-10-30"})
	if got == nil || got.ID != "tax" || overlap {
		t.Fatalf("older bill got %+v, overlap %v", got, overlap)
	}
}

func TestKnowledgeValidateRejectsMissingAccountOrSide(t *testing.T) {
	chart := map[string]Account{"510": {}, "110": {}}
	data := KnowledgeData{DocFormat: "PV", EffectiveDate: "2026-11-26", Lines: []KnowledgeLine{{AccountCode: "510", Side: "D"}, {AccountCode: "110", Side: "C"}}}
	if err := validateKnowledge("rule", &data, chart); err != nil {
		t.Fatal(err)
	}
	data.Lines[1].AccountCode = "other"
	if validateKnowledge("rule", &data, chart) == nil {
		t.Fatal("accepted an unknown account")
	}
	data.Lines = data.Lines[:1]
	if validateKnowledge("rule", &data, chart) == nil {
		t.Fatal("accepted a rule with only debit")
	}
}

func TestKnowledgeBookingUsesBillDateAndPreservesEvidence(t *testing.T) {
	m, js, g := fixture()
	b := Build(m, js, g, 0, mechanicsOptions(day(10)))
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, bangkok)
	legacy := mcpSuggestInput{TaxID: "0105500000001", TotalAmount: 107, VatAmount: 7, DocDate: "2026-11-25"}
	rule := KnowledgeItem{ID: "r", Kind: "rule", Active: true, Data: KnowledgeData{PartyTaxID: legacy.TaxID, EffectiveDate: "2026-11-25", Lines: []KnowledgeLine{{AccountCode: "510", Side: "D"}, {AccountCode: "110", Side: "C"}}}}
	got, err := b.suggestWithKnowledge(legacy, []KnowledgeItem{rule}, now)
	if err != nil || got.Source != "shop_rule" || len(got.AppliedKnowledge) != 1 || got.Booking.Lines[0].AccountCode != "510" || len(got.Refs) == 0 || got.Refs[0].DocNo != "PVj3" {
		t.Fatalf("override or unchanged evidence: %+v, %v", got, err)
	}

	remap := KnowledgeItem{ID: "m", Kind: "remap", Active: true, Data: KnowledgeData{FromAccount: "51212", ToAccount: "510", EffectiveDate: "2026-11-25"}}
	got, err = b.suggestWithKnowledge(legacy, []KnowledgeItem{remap}, now)
	if err != nil || got.Source != "learned" || len(got.AppliedKnowledge) != 1 || got.Refs[0].Lines[0].AccountCode == "510" {
		t.Fatalf("remap or unchanged evidence: %+v, %v", got, err)
	}
	legacy.DocDate = "2026-11-24"
	got, err = b.suggestWithKnowledge(legacy, []KnowledgeItem{rule, remap}, now)
	if err != nil || got.Source != "learned" || len(got.AppliedKnowledge) != 0 {
		t.Fatalf("rule applied before effective date: %+v, %v", got, err)
	}
}

func TestKnowledgeBillHeadersAndWarnings(t *testing.T) {
	m, js, g := fixture()
	m.Shop.TaxID = "0105500000099"
	b := Build(m, js, g, 0, mechanicsOptions(day(10)))
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, bangkok)
	input := mcpSuggestInput{SellerTaxID: "0105500000001", BuyerTaxID: m.Shop.TaxID, DocDate: "2569-11-25", TotalAmount: 107}
	rule := KnowledgeItem{ID: "r", Kind: "rule", Active: true, Data: KnowledgeData{PartyTaxID: input.SellerTaxID, BillType: "purchase", EffectiveDate: "2020-01-01", Lines: []KnowledgeLine{{AccountCode: "510", Side: "D"}, {AccountCode: "110", Side: "C"}}}}
	got, err := b.suggestWithKnowledge(input, []KnowledgeItem{rule}, now)
	if err != nil || got.BillType != "purchase" || got.BillTypeMatchedBy != "taxid" || got.DocDate != "2026-11-25" || got.Source != "shop_rule" {
		t.Fatalf("classified suggestion: %+v, %v", got, err)
	}
	if len(b.knowledgeWarnings([]KnowledgeItem{rule}, nil)) != 1 {
		t.Fatal("expected a conflict with previously posted journals")
	}
	input.SellerTaxID, input.BuyerTaxID = input.BuyerTaxID, input.SellerTaxID
	got, err = b.suggestWithKnowledge(input, []KnowledgeItem{rule}, now)
	if err != nil || got.BillType != "sale" || got.Source == "shop_rule" {
		t.Fatalf("sale used purchase rule: %+v, %v", got, err)
	}
	input.BuyerTaxID = input.SellerTaxID
	got, err = b.suggestWithKnowledge(input, []KnowledgeItem{rule}, now)
	if err != nil || got.BillType != "unknown" || got.Known {
		t.Fatalf("ambiguous bill was guessed: %+v, %v", got, err)
	}
}
