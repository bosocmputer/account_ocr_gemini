package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"go.mongodb.org/mongo-driver/bson"
)

// Sample files paths — same real files the frontend ships and the user
// provided directly during planning.
const (
	smlSampleDailyFile = "/Users/nontawatwongnuk/dev_vue/bcaccount/public/demo/data/รายงานข้อมูลรายวัน.xlsx"
	smlSampleVatFile   = "/Users/nontawatwongnuk/dev_vue/bcaccount/public/demo/data/รายงานภาษีขาย.xlsx"
)

// ensureSmlTestConfig sets the one config var readAllSmlRows depends on,
// without requiring the full configs.LoadConfig() (which fatals on missing
// unrelated env vars like GEMINI_API_KEY, same reasoning as setupLiveDBTest
// in import_validation_test.go). These SML parser tests are pure-function
// tests over real sample files — they don't touch the database at all.
func ensureSmlTestConfig(t *testing.T) {
	t.Helper()
	if configs.EXCEL_IMPORT_MAX_ROWS == 0 {
		configs.EXCEL_IMPORT_MAX_ROWS = 20000
	}
}

// TestParseSmlDailyBlocks_RealSampleFile verifies the block-parsing state
// machine against the real SML export: 116 documents, every one balanced,
// exactly one 2140170 line each, no structural issues, subtotal/grand-total
// rows correctly skipped. These exact counts were independently verified
// with a throwaway Python script during planning — this test exists to
// confirm the Go port reproduces them.
func TestParseSmlDailyBlocks_RealSampleFile(t *testing.T) {
	ensureSmlTestConfig(t)
	rows, err := readAllSmlRows(smlSampleDailyFile, "")
	if err != nil {
		t.Fatalf("readAllSmlRows failed: %v", err)
	}

	parsed := parseSmlDailyBlocks(rows, time.Now().Add(time.Hour))
	if parsed.TimedOut {
		t.Fatal("unexpected timeout")
	}
	groups, issues := parsed.Groups, parsed.Issues

	fmt.Printf("=== SML DAILY BLOCKS RESULT ===\ndocuments: %d, structural issues: %d\n", len(groups), len(issues))
	for _, iss := range issues {
		fmt.Printf("  [%s] %s\n", iss.Severity, iss.Message)
	}

	if len(groups) != 116 {
		t.Errorf("expected 116 documents, got %d", len(groups))
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 structural issues, got %d: %+v", len(issues), issues)
	}
	if parsed.CompanyName != "บริษัท แปดริ้วเครื่องเย็นเชียงใหม่ (1994) จำกัด" {
		t.Errorf("unexpected parsed company name: %q", parsed.CompanyName)
	}

	vatCode := "2140170"
	for _, g := range groups {
		var sumDebit, sumCredit, vatLineCount float64
		for _, l := range g.Lines {
			sumDebit += l.Debit
			sumCredit += l.Credit
			if l.AccountCode == vatCode {
				vatLineCount++
			}
		}
		if math_Abs(sumDebit-sumCredit) > 0.01 {
			t.Errorf("doc %s: unbalanced debit=%.2f credit=%.2f", g.Header.Docno, sumDebit, sumCredit)
		}
		if vatLineCount != 1 {
			t.Errorf("doc %s: expected exactly 1 VAT line, got %.0f", g.Header.Docno, vatLineCount)
		}
	}
}

// TestParseSmlVatReport_RealSampleFile verifies the flat VAT report parser
// and the join-key overlap independently confirmed during planning: all 116
// daily-journal docnos must be present among the parsed VAT records.
func TestParseSmlVatReport_RealSampleFile(t *testing.T) {
	ensureSmlTestConfig(t)
	rows, err := readAllSmlRows(smlSampleVatFile, "ExportExcel")
	if err != nil {
		t.Fatalf("readAllSmlRows failed: %v", err)
	}

	records := parseSmlVatReport(rows)
	fmt.Printf("=== SML VAT REPORT RESULT ===\nrecords: %d\n", len(records))

	if len(records) != 125 {
		t.Errorf("expected 125 VAT records, got %d", len(records))
	}

	byDocno := make(map[string]smlVatRecord, len(records))
	for _, r := range records {
		byDocno[r.Docno] = r
	}

	dailyRows, err := readAllSmlRows(smlSampleDailyFile, "")
	if err != nil {
		t.Fatalf("readAllSmlRows (daily) failed: %v", err)
	}
	groups := parseSmlDailyBlocks(dailyRows, time.Now().Add(time.Hour)).Groups
	for _, g := range groups {
		if _, ok := byDocno[g.Header.Docno]; !ok {
			t.Errorf("daily docno %s not found in VAT report — join would silently fall back for it", g.Header.Docno)
		}
	}
}

// TestParseThaiBEDate verifies the Buddhist-era date parser against real
// sample values and rejects garbage input rather than silently returning a
// wrong date.
func TestParseThaiBEDate(t *testing.T) {
	cases := []struct {
		raw     string
		wantOk  bool
		wantISO string
	}{
		{"1/8/2569", true, "2026-08-01T00:00:00.000Z"},
		{"31/12/2569", true, "2026-12-31T00:00:00.000Z"},
		{"", false, ""},
		{"not-a-date", false, ""},
		{"32/13/2569", false, ""}, // invalid day/month
	}
	for _, c := range cases {
		got, ok := parseThaiBEDate(c.raw)
		if ok != c.wantOk {
			t.Errorf("parseThaiBEDate(%q): ok=%v, want %v", c.raw, ok, c.wantOk)
			continue
		}
		if ok && got != c.wantISO {
			t.Errorf("parseThaiBEDate(%q) = %q, want %q", c.raw, got, c.wantISO)
		}
	}
}

// TestResolveSmlBookcode_NameFallback verifies the code-then-name1 fallback
// strategy against the exact scenario found during planning: SML's "02"
// prefix does not match any real journalBooks.code, but the text after the
// slash matches name1.
func TestResolveSmlBookcode_NameFallback(t *testing.T) {
	codeMap := map[string]bson.M{
		"INV": {"code": "INV", "name1": "สมุดรายวันขาย"},
	}
	nameMap := map[string]bson.M{
		"สมุดรายวันขาย": {"code": "INV", "name1": "สมุดรายวันขาย"},
	}

	code, issue := resolveSmlBookcode("02/สมุดรายวันขาย", codeMap, nameMap)
	if issue != "" {
		t.Fatalf("expected no issue, got: %s", issue)
	}
	if code != "INV" {
		t.Errorf("expected resolved code INV, got %q", code)
	}

	// code-first hit
	codeMap2 := map[string]bson.M{"02": {"code": "02", "name1": "whatever"}}
	code2, issue2 := resolveSmlBookcode("02/สมุดรายวันขาย", codeMap2, map[string]bson.M{})
	if issue2 != "" || code2 != "02" {
		t.Errorf("expected code-first match to win: code=%q issue=%q", code2, issue2)
	}

	// neither matches
	_, issue3 := resolveSmlBookcode("99/ไม่มีจริง", map[string]bson.M{}, map[string]bson.M{})
	if issue3 == "" {
		t.Error("expected an issue when neither code nor name matches")
	}

	// malformed (no slash)
	_, issue4 := resolveSmlBookcode("no-slash-here", map[string]bson.M{}, map[string]bson.M{})
	if issue4 == "" {
		t.Error("expected an issue for a bookcode string with no '/'")
	}
}

// TestBuildSmlSalesDocument_EndToEnd runs buildSmlSalesDocument against the
// real sample file's parsed groups, using a synthetic masterdata map built
// from exactly the accountcodes/bookcode-name the file itself contains —
// this exercises the full per-document validation logic (accountcode
// lookup, bookcode resolution, balance check, VAT-line detection, vats[]
// construction) without depending on any particular live shop's chart of
// accounts matching this sample company's numbering.
func TestBuildSmlSalesDocument_EndToEnd(t *testing.T) {
	ensureSmlTestConfig(t)
	rows, err := readAllSmlRows(smlSampleDailyFile, "")
	if err != nil {
		t.Fatalf("readAllSmlRows failed: %v", err)
	}
	parsed := parseSmlDailyBlocks(rows, time.Now().Add(time.Hour))
	groups, structIssues := parsed.Groups, parsed.Issues
	if len(structIssues) != 0 {
		t.Fatalf("unexpected structural issues: %+v", structIssues)
	}

	chartOfAccountsMap := map[string]bson.M{}
	for _, g := range groups {
		for _, l := range g.Lines {
			chartOfAccountsMap[l.AccountCode] = bson.M{"accountcode": l.AccountCode, "accountname": "Test Account " + l.AccountCode}
		}
	}
	journalBookCodeMap := map[string]bson.M{}
	journalBookNameMap := map[string]bson.M{
		"สมุดรายวันขาย": {"code": "INV", "name1": "สมุดรายวันขาย"},
	}

	vatCode := "2140170"
	errorCount, warningCount, docCount := 0, 0, 0
	for _, g := range groups {
		doc, issues := buildSmlSalesDocument(g, vatCode, nil, chartOfAccountsMap, journalBookCodeMap, journalBookNameMap)
		docCount++
		for _, iss := range issues {
			if iss.Severity == "error" {
				errorCount++
				t.Errorf("doc %s: unexpected error [%s] %s", g.Header.Docno, iss.Field, iss.Message)
			} else {
				warningCount++
			}
		}
		if doc.Docno != g.Header.Docno {
			t.Errorf("doc mismatch: %s vs %s", doc.Docno, g.Header.Docno)
		}
		if doc.Bookcode != "INV" {
			t.Errorf("doc %s: expected resolved bookcode INV, got %q", doc.Docno, doc.Bookcode)
		}
		if len(doc.Vats) != 1 {
			t.Errorf("doc %s: expected exactly 1 vats[] entry, got %d", doc.Docno, len(doc.Vats))
		}
		if doc.Taxes == nil || len(doc.Taxes) != 0 {
			t.Errorf("doc %s: expected Taxes to be an empty non-nil slice, got %v", doc.Docno, doc.Taxes)
		}
		if doc.Exdocrefdate != nil {
			t.Errorf("doc %s: expected Exdocrefdate nil, got %v", doc.Docno, *doc.Exdocrefdate)
		}
		if doc.Debtor == nil || len(doc.Debtor) != 0 {
			t.Errorf("doc %s: expected empty non-nil Debtor map, got %v", doc.Docno, doc.Debtor)
		}
	}

	fmt.Printf("=== BUILD SML SALES DOCUMENT E2E RESULT ===\ndocs=%d errors=%d warnings=%d\n", docCount, errorCount, warningCount)
	if docCount != 116 {
		t.Errorf("expected 116 documents built, got %d", docCount)
	}
	if errorCount != 0 {
		t.Errorf("expected 0 errors across all documents, got %d", errorCount)
	}
}

// TestBuildSmlSalesDocument_VatBaseFallbackFormula verifies the
// sumDebit-vatamount fallback formula (used when no VAT-file match exists)
// against real sample documents, reproducing the zero-mismatch result
// independently confirmed with a throwaway Python script during planning.
func TestBuildSmlSalesDocument_VatBaseFallbackFormula(t *testing.T) {
	ensureSmlTestConfig(t)
	rows, err := readAllSmlRows(smlSampleDailyFile, "")
	if err != nil {
		t.Fatalf("readAllSmlRows failed: %v", err)
	}
	groups := parseSmlDailyBlocks(rows, time.Now().Add(time.Hour)).Groups

	vatCode := "2140170"
	mismatches := 0
	for _, g := range groups {
		var sumDebit, vatLineCredit, trueVatBase float64
		for _, l := range g.Lines {
			sumDebit += l.Debit
			if l.AccountCode == vatCode {
				vatLineCredit += l.Credit
			} else {
				trueVatBase += l.Credit
			}
		}
		formulaVatBase := sumDebit - vatLineCredit
		if math_Abs(trueVatBase-formulaVatBase) > 0.02 {
			mismatches++
			t.Errorf("doc %s: formula vatbase=%.2f, true=%.2f", g.Header.Docno, formulaVatBase, trueVatBase)
		}
	}
	if mismatches > 0 {
		t.Fatalf("%d/%d documents mismatched the fallback vatbase formula", mismatches, len(groups))
	}
}
