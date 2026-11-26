package shopbrain

import (
	"strings"
	"testing"
)

func lineAmount(sv *ShapeView, code, side string) float64 {
	for _, l := range sv.Lines {
		if l.AccountCode == code && l.Side == side {
			return l.Amount
		}
	}
	return -1
}

func TestSuggestBookingPricesTheBillInSatang(t *testing.T) {
	b := build(t)
	// 1,234.56 incl. VAT 80.77: the VAT line takes the bill's VAT, the rest is the expense
	s := b.suggestBooking(mcpSuggestInput{TaxID: "0105500000001", TotalAmount: 1234.56, VatAmount: 80.77})
	if !s.Known || s.Booking == nil || s.DocFormat != "PV" {
		t.Fatalf("not known: %+v", s)
	}
	if got := lineAmount(s.Booking, "11541", "D"); got != 80.77 {
		t.Errorf("VAT line = %v", got)
	}
	if got := lineAmount(s.Booking, "51212", "D"); got != 1153.79 {
		t.Errorf("expense line = %v", got)
	}
	if got := lineAmount(s.Booking, "11111", "C"); got != 1234.56 {
		t.Errorf("credit line = %v", got)
	}
	// 80.77 is 7% of 1,153.79 → nothing to flag
	if len(s.Warnings) != 0 {
		t.Errorf("correct VAT flagged: %v", s.Warnings)
	}
	// 75.00 on 1,070 is not 7% of 995.00 (69.65) → flagged, amounts still add up
	bad := b.suggestBooking(mcpSuggestInput{TaxID: "0105500000001", TotalAmount: 1070, VatAmount: 75})
	if !strings.Contains(strings.Join(bad.Warnings, " "), "VAT ตามบิล 75.00") {
		t.Errorf("VAT mismatch not flagged: %v", bad.Warnings)
	}
	if lineAmount(bad.Booking, "51212", "D")+lineAmount(bad.Booking, "11541", "D") != 1070 {
		t.Errorf("Dr != total: %+v", bad.Booking.Lines)
	}
}

func TestSuggestBookingRefsAreTheRealJournals(t *testing.T) {
	b := build(t)
	s := b.suggestBooking(mcpSuggestInput{PartyName: "บริษัท ผู้ขาย C1 จำกัด", TotalAmount: 107, VatAmount: 7})
	if s.Party == nil || s.Party.Code != "C1" {
		t.Fatalf("party not resolved: %+v", s.Party)
	}
	if s.RefsTotal != 3 || len(s.Refs) != 3 || s.Refs[0].DocNo != "PVj3" || s.Refs[2].DocNo != "PVj1" {
		t.Fatalf("refs: %+v", s.Refs)
	}
	r := s.Refs[0]
	if r.Total != 321 || len(r.Lines) != 3 || len(r.Vats) != 1 || r.Vats[0].VatAmount != 21 || r.Party == nil || r.Party.TaxID != "0105500000001" {
		t.Errorf("ref detail: %+v", r)
	}
	txt := s.text()
	for _, want := range []string{"อ้างอิง 1: PVj3", "Dr 51212 ค่าขนส่ง 300.00", "Cr 11111 เงินสด 321.00", "VAT 7% ฐาน 300.00 ภาษี 21.00", "http"} {
		has := strings.Contains(txt, want)
		if want == "http" {
			if has {
				t.Errorf("refs must be details, not links:\n%s", txt)
			}
			continue
		}
		if !has {
			t.Errorf("text missing %q:\n%s", want, txt)
		}
	}
}

func TestSuggestBookingUnknownSaysSoWithoutRefs(t *testing.T) {
	b := build(t)
	s := b.suggestBooking(mcpSuggestInput{PartyName: "ร้านที่ไม่เคยซื้อ", Description: "ของใหม่ไม่เคยเห็น", DocFormat: "PV", TotalAmount: 100})
	if s.Known || s.Booking != nil || len(s.Refs) != 0 || !strings.Contains(s.RefsNote, "ไม่มีรายการอ้างอิง") || len(s.TopAccounts) == 0 {
		t.Errorf("unknown: %+v", s)
	}
	// the shop's own tax id is not the counterparty's
	own := b.suggestBooking(mcpSuggestInput{TaxID: b.Master.Shop.TaxID, Description: "x"})
	if b.Master.Shop.TaxID != "" && len(own.Warnings) == 0 {
		t.Errorf("own tax id not flagged")
	}
}

func TestSearchAndSummarize(t *testing.T) {
	b := build(t)
	res, err := b.searchJournals(mcpSearchJournalsInput{journalQuery: journalQuery{DateFrom: "2026-01-03", DateTo: "2026-01-05"}})
	// day(n) = 1 Jan + n: j2 (3 Jan), j3 (4 Jan), j4 (5 Jan); newest first
	if err != nil || res.Matched != 3 || res.Refs[0].DocNo != "JV001" {
		t.Errorf("date range: %v %+v", err, res)
	}
	// Buddhist year
	if res2, _ := b.searchJournals(mcpSearchJournalsInput{journalQuery: journalQuery{DateFrom: "2569-01-03", DateTo: "2569-01-05"}}); res2.Matched != 3 {
		t.Errorf("Buddhist year: %d", res2.Matched)
	}
	page, _ := b.searchJournals(mcpSearchJournalsInput{journalQuery: journalQuery{Party: "ผู้ขาย C1"}, Limit: 2, Offset: 2})
	if page.Matched != 3 || len(page.Refs) != 1 || page.Refs[0].DocNo != "PVj1" || page.Total != 642 {
		t.Errorf("party page: %+v", page)
	}
	sum, err := b.summarize(mcpSummarizeInput{journalQuery: journalQuery{Account: "53000"}})
	if err != nil || len(sum.Groups) != 1 || sum.Groups[0].Debit != 210 || sum.Groups[0].Journals != 3 {
		t.Errorf("summarize account: %v %+v", err, sum)
	}
	m, _ := b.summarize(mcpSummarizeInput{GroupBy: "month"})
	if len(m.Groups) != 1 || m.Groups[0].Key != "2026-01" {
		t.Errorf("summarize month: %+v", m)
	}
	if _, err := b.summarize(mcpSummarizeInput{GroupBy: "nope"}); err == nil {
		t.Errorf("bad group_by must fail")
	}
}

func TestSearchSortsByDocumentDate(t *testing.T) {
	b := build(t)
	b.entries[0].j.DocDate = day(30) // saved first, dated last
	res, _ := b.searchJournals(mcpSearchJournalsInput{})
	if res.Refs[0].GuidFixed != b.entries[0].j.GuidFixed {
		t.Errorf("first = %s, want the latest document date", res.Refs[0].DocNo)
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[float64]string{0: "0.00", 4.515: "4.52", 1234567.8: "1,234,567.80", -1000: "-1,000.00", 100: "100.00"} {
		if got := money(in); got != want {
			t.Errorf("money(%v) = %q, want %q", in, got, want)
		}
	}
}
