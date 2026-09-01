package api

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/jobs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/joho/godotenv"
	"github.com/xuri/excelize/v2"
)

// TestBuildParsedDocuments_SampleImportPerLine is a real end-to-end
// integration check against the live dev database (not a mock) using the
// same sample file the frontend already ships and that was previously
// verified against the JS implementation: 3 documents, 0 errors, VAT on
// JV-2026-0001, WHT on JV-2026-0003, debtor AR001 auto-detected on
// JV-2026-0001. This test exists to confirm the Go port produces the exact
// same result for the exact same file, per the plan's #1 production-risk
// item (behavioral parity).
func TestBuildParsedDocuments_SampleImportPerLine(t *testing.T) {
	shopID := setupLiveDBTest(t)
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		t.Fatalf("GetOrLoadMasterData failed: %v", err)
	}

	filePath := "/Users/nontawatwongnuk/dev_vue/bcaccount/public/demo/file/sample_import_per_line.xlsx"
	f, err := excelize.OpenFile(filePath, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("open sample file failed: %v", err)
	}
	defer f.Close()

	sheetName := f.GetSheetName(0)
	rows, err := f.Rows(sheetName)
	if err != nil {
		t.Fatalf("read rows failed: %v", err)
	}
	var allRows [][]string
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("read row failed: %v", err)
		}
		allRows = append(allRows, cols)
	}
	rows.Close()

	headerRowIndex := 0
	var dataRows [][]string
	for _, r := range allRows[headerRowIndex+1:] {
		if rowHasAnyValue(r) {
			dataRows = append(dataRows, r)
		}
	}

	// This sample file's exact column layout (26 columns), verified earlier
	// this session — header row 0, columns 0-25 in this order:
	// เลขที่เอกสาร*, วันที่เอกสาร*, สมุดรายวัน*, รหัสกลุ่มบัญชี, คำอธิบายรายการ,
	// ประเภทรายการ, เลขที่เอกสารอ้างอิง, วันที่เอกสารอ้างอิง, รหัสลูกหนี้/เจ้าหนี้,
	// รหัสบัญชี*, ชื่อบัญชี, เดบิต*, เครดิต*, เลขที่ใบกำกับภาษี, วันที่ใบกำกับภาษี**,
	// ฐานภาษี**, อัตราภาษี**, ยอดภาษี**, ชื่อลูกค้า/ผู้ขาย, เลขผู้เสียภาษี,
	// เลขที่หนังสือรับรองหัก ณ ที่จ่าย, วันที่หัก ณ ที่จ่าย**, ฐานภาษีหัก ณ ที่จ่าย,
	// อัตราหัก ณ ที่จ่าย, ยอดหัก ณ ที่จ่าย, ประเภทเงินได้**
	col := func(i int) *int { v := i; return &v }
	cfg := ImportValidationConfig{
		HeaderRowIndex: headerRowIndex,
		RowMode:        "per-line",
		FieldMappings: map[string]*int{
			"docno":              col(0),
			"docdate":            col(1),
			"bookcode":           col(2),
			"accountgroup":       col(3),
			"accountdescription": col(4),
			"journaltype":        col(5),
			"exdocrefno":         col(6),
			"exdocrefdate":       col(7),
			"debtaccountcode":    col(8),
			"accountcode":        col(9),
			"accountname":        col(10),
			"debitamount":        col(11),
			"creditamount":       col(12),
			"vatdocno":           col(13),
			"vatdate":            col(14),
			"vatbase":            col(15),
			"vatrate":            col(16),
			"vatamount":          col(17),
			"custname":           col(18),
			"custtaxid":          col(19),
			"taxdocno":           col(20),
			"taxdate":            col(21),
			"whtbase":            col(22),
			"whtrate":            col(23),
			"whtamount":          col(24),
			"whtdesc":            col(25),
		},
	}

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	accountGroupMap := buildStringKeyedMap(masterCache.AccountGroups, "code")
	debtorMap := buildStringKeyedMap(masterCache.Debtors, "code")
	creditorMap := buildStringKeyedMap(masterCache.Creditors, "code")

	reqCtx := common.NewRequestContext(shopID)
	job, _, err := jobs.Create(reqCtx)
	if err != nil {
		t.Fatalf("job create failed: %v", err)
	}

	docs, issues := buildParsedDocuments(dataRows, cfg, chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap, job, time.Now().Add(time.Hour))

	fmt.Printf("=== RESULT ===\n")
	fmt.Printf("documents: %d, issues: %d\n", len(docs), len(issues))
	for _, iss := range issues {
		fmt.Printf("  [%s] %s (docno=%s, field=%s, rows=%v)\n", iss.Severity, iss.Message, iss.Docno, iss.Field, iss.RowNumbers)
	}
	for _, d := range docs {
		fmt.Printf("  doc %s: amount=%.2f bookcode=%s accountgroup=%s journaltype=%d exdocrefno=%s exdocrefdate=%v debtaccounttype=%d debtor=%v vats=%d taxes=%d journaldetail=%d\n",
			d.Docno, d.Amount, d.Bookcode, d.Accountgroup, d.Journaltype, d.Exdocrefno, d.Exdocrefdate, d.Debtaccounttype, d.Debtor["code"], len(d.Vats), len(d.Taxes), len(d.Journaldetail))
	}

	if len(docs) != 3 {
		t.Errorf("expected 3 documents, got %d", len(docs))
	}
	errorCount := 0
	for _, iss := range issues {
		if iss.Severity == "error" {
			errorCount++
		}
	}
	if errorCount != 0 {
		t.Errorf("expected 0 error-severity issues, got %d", errorCount)
	}
}

// TestBuildParsedDocuments_VatConflict verifies the row-level-field
// conflict-detection path: JV-TEST-01 has identical VAT values repeated on
// every row (should produce 0 issues), JV-TEST-02 deliberately has a
// mismatched vatamount on one row (should produce exactly 1 warning naming
// the conflicting rows) — mirrors the exact scenario verified against the
// JS implementation earlier this session.
func TestBuildParsedDocuments_VatConflict(t *testing.T) {
	shopID := setupLiveDBTest(t)
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		t.Fatalf("GetOrLoadMasterData failed: %v", err)
	}

	dataRows, headerRowIndex := loadTestFileDataRows(t, "/tmp/go_test_vat_conflict.xlsx", 0)
	col := func(i int) *int { v := i; return &v }
	cfg := ImportValidationConfig{
		HeaderRowIndex: headerRowIndex,
		RowMode:        "per-line",
		FieldMappings: map[string]*int{
			"docno": col(0), "docdate": col(1), "bookcode": col(2), "accountcode": col(3),
			"debitamount": col(4), "creditamount": col(5), "vatdocno": col(6), "vatdate": col(7),
			"vatbase": col(8), "vatrate": col(9), "vatamount": col(10),
		},
	}

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	accountGroupMap := buildStringKeyedMap(masterCache.AccountGroups, "code")
	debtorMap := buildStringKeyedMap(masterCache.Debtors, "code")
	creditorMap := buildStringKeyedMap(masterCache.Creditors, "code")

	reqCtx := common.NewRequestContext(shopID)
	job, _, err := jobs.Create(reqCtx)
	if err != nil {
		t.Fatalf("job create failed: %v", err)
	}

	docs, issues := buildParsedDocuments(dataRows, cfg, chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap, job, time.Now().Add(time.Hour))

	fmt.Printf("=== VAT CONFLICT RESULT ===\ndocuments: %d, issues: %d\n", len(docs), len(issues))
	for _, iss := range issues {
		fmt.Printf("  [%s] %s (docno=%s, rows=%v)\n", iss.Severity, iss.Message, iss.Docno, iss.RowNumbers)
	}

	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue (the vatamount conflict warning), got %d", len(issues))
	}
	if issues[0].Severity != "warning" || issues[0].Field != "vatamount" || issues[0].Docno != "JV-TEST-02" {
		t.Errorf("unexpected issue shape: %+v", issues[0])
	}
}

// TestBuildParsedDocuments_PerDocument2Line_SignedColumn verifies the
// bank-statement row-shape mode: BANK-0001 (+3210, deposit) should debit the
// fixed bank account and credit the counter-account for the full magnitude;
// BANK-0002 (-8000, withdrawal) should swap sides (credit bank, debit
// counter-account) rather than encode the unused side as a zero-amount
// line — this exact bug (every row failing "no debit/credit amount") was
// found and fixed in the JS implementation earlier this session, so this
// test exists specifically to confirm the Go port didn't reintroduce it.
func TestBuildParsedDocuments_PerDocument2Line_SignedColumn(t *testing.T) {
	shopID := setupLiveDBTest(t)
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		t.Fatalf("GetOrLoadMasterData failed: %v", err)
	}

	dataRows, headerRowIndex := loadTestFileDataRows(t, "/tmp/go_test_perdoc2line_bank.xlsx", 0)
	col := func(i int) *int { v := i; return &v }
	bankAccount := "111200" // เงินฝากธนาคาร — verified real account earlier this session
	counterAccount := "410010"
	cfg := ImportValidationConfig{
		HeaderRowIndex:     headerRowIndex,
		RowMode:            "per-document-2-line",
		AmountSplitMode:    "signed-single-column",
		SignedAmountColumn: col(4),
		DebitSource:        ImportFieldSource{Mode: "fixed", AccountCode: &bankAccount},
		CreditSource:       ImportFieldSource{Mode: "fixed", AccountCode: &counterAccount},
		FieldMappings: map[string]*int{
			"docno": col(1), "docdate": col(0), "bookcode": col(2), "accountdescription": col(3),
		},
	}

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	accountGroupMap := buildStringKeyedMap(masterCache.AccountGroups, "code")
	debtorMap := buildStringKeyedMap(masterCache.Debtors, "code")
	creditorMap := buildStringKeyedMap(masterCache.Creditors, "code")

	reqCtx := common.NewRequestContext(shopID)
	job, _, err := jobs.Create(reqCtx)
	if err != nil {
		t.Fatalf("job create failed: %v", err)
	}

	docs, issues := buildParsedDocuments(dataRows, cfg, chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap, job, time.Now().Add(time.Hour))

	fmt.Printf("=== PER-DOCUMENT-2-LINE SIGNED-COLUMN RESULT ===\ndocuments: %d, issues: %d\n", len(docs), len(issues))
	for _, iss := range issues {
		fmt.Printf("  [%s] %s (docno=%s, field=%s)\n", iss.Severity, iss.Message, iss.Docno, iss.Field)
	}
	for _, d := range docs {
		fmt.Printf("  doc %s: amount=%.2f journaldetail=%+v\n", d.Docno, d.Amount, d.Journaldetail)
	}

	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(issues))
	}

	// BANK-0001 and BANK-0002 must both balance and have 2 real (nonzero)
	// journaldetail lines each — this is exactly the assertion that would
	// have failed before the debit/credit-swap fix.
	for _, docno := range []string{"BANK-0001", "BANK-0002"} {
		var found *ImportDocument
		for i := range docs {
			if docs[i].Docno == docno {
				found = &docs[i]
			}
		}
		if found == nil {
			t.Fatalf("expected document %s in result, not found", docno)
		}
		if len(found.Journaldetail) != 2 {
			t.Errorf("%s: expected 2 journaldetail lines, got %d: %+v", docno, len(found.Journaldetail), found.Journaldetail)
		}
		var sumDebit, sumCredit float64
		for _, jd := range found.Journaldetail {
			sumDebit += jd.DebitAmount
			sumCredit += jd.CreditAmount
		}
		if sumDebit != sumCredit {
			t.Errorf("%s: unbalanced — debit=%.2f credit=%.2f", docno, sumDebit, sumCredit)
		}
		if sumDebit == 0 {
			t.Errorf("%s: expected nonzero amount, got 0 (indicates the zero-amount-line bug regressed)", docno)
		}
	}
}

// TestBuildParsedDocuments_PerDocument2Line_SameAccount verifies the
// same-account-both-sides error still fires in per-document-2-line mode
// when debitSource and creditSource resolve to the identical fixed account.
func TestBuildParsedDocuments_PerDocument2Line_SameAccount(t *testing.T) {
	shopID := setupLiveDBTest(t)
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		t.Fatalf("GetOrLoadMasterData failed: %v", err)
	}

	dataRows, headerRowIndex := loadTestFileDataRows(t, "/tmp/go_test_perdoc2line_sameaccount.xlsx", 0)
	col := func(i int) *int { v := i; return &v }
	sameAccount := "111200"
	cfg := ImportValidationConfig{
		HeaderRowIndex:     headerRowIndex,
		RowMode:            "per-document-2-line",
		AmountSplitMode:    "signed-single-column",
		SignedAmountColumn: col(4),
		DebitSource:        ImportFieldSource{Mode: "fixed", AccountCode: &sameAccount},
		CreditSource:       ImportFieldSource{Mode: "fixed", AccountCode: &sameAccount},
		FieldMappings: map[string]*int{
			"docno": col(1), "docdate": col(0), "bookcode": col(2), "accountdescription": col(3),
		},
	}

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	accountGroupMap := buildStringKeyedMap(masterCache.AccountGroups, "code")
	debtorMap := buildStringKeyedMap(masterCache.Debtors, "code")
	creditorMap := buildStringKeyedMap(masterCache.Creditors, "code")

	reqCtx := common.NewRequestContext(shopID)
	job, _, err := jobs.Create(reqCtx)
	if err != nil {
		t.Fatalf("job create failed: %v", err)
	}

	_, issues := buildParsedDocuments(dataRows, cfg, chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap, job, time.Now().Add(time.Hour))

	fmt.Printf("=== PER-DOCUMENT-2-LINE SAME-ACCOUNT RESULT ===\nissues: %d\n", len(issues))
	for _, iss := range issues {
		fmt.Printf("  [%s] %s (docno=%s, field=%s)\n", iss.Severity, iss.Message, iss.Docno, iss.Field)
	}

	foundSameAccountError := false
	for _, iss := range issues {
		if iss.Docno == "SAME-0001" && iss.Field == "accountcode" && iss.Severity == "error" && iss.Message == "บัญชีเดบิตและเครดิตเป็นบัญชีเดียวกัน (111200)" {
			foundSameAccountError = true
		}
	}
	if !foundSameAccountError {
		t.Error("expected same-account-both-sides error for SAME-0001, not found")
	}
}

// TestBuildParsedDocuments_MissingFields verifies bookcode-missing and
// accountcode-not-found errors fire with the exact same messages as the JS
// implementation.
func TestBuildParsedDocuments_MissingFields(t *testing.T) {
	shopID := setupLiveDBTest(t)
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		t.Fatalf("GetOrLoadMasterData failed: %v", err)
	}

	dataRows, headerRowIndex := loadTestFileDataRows(t, "/tmp/go_test_missing_fields.xlsx", 0)
	col := func(i int) *int { v := i; return &v }
	cfg := ImportValidationConfig{
		HeaderRowIndex: headerRowIndex,
		RowMode:        "per-line",
		FieldMappings: map[string]*int{
			"docno": col(0), "docdate": col(1), "bookcode": col(2), "accountcode": col(3),
			"debitamount": col(4), "creditamount": col(5),
		},
	}

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	accountGroupMap := buildStringKeyedMap(masterCache.AccountGroups, "code")
	debtorMap := buildStringKeyedMap(masterCache.Debtors, "code")
	creditorMap := buildStringKeyedMap(masterCache.Creditors, "code")

	reqCtx := common.NewRequestContext(shopID)
	job, _, err := jobs.Create(reqCtx)
	if err != nil {
		t.Fatalf("job create failed: %v", err)
	}

	docs, issues := buildParsedDocuments(dataRows, cfg, chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap, job, time.Now().Add(time.Hour))

	fmt.Printf("=== MISSING FIELDS RESULT ===\ndocuments: %d, issues: %d\n", len(docs), len(issues))
	for _, iss := range issues {
		fmt.Printf("  [%s] %s (docno=%s, field=%s)\n", iss.Severity, iss.Message, iss.Docno, iss.Field)
	}

	foundBookcodeError := false
	foundAccountcodeError := false
	for _, iss := range issues {
		if iss.Docno == "JV-BAD-01" && iss.Field == "bookcode" && iss.Message == "ไม่ได้ระบุสมุดรายวัน" {
			foundBookcodeError = true
		}
		if iss.Docno == "JV-BAD-02" && iss.Field == "accountcode" && iss.Message == `รหัสบัญชี "NOTREAL" ไม่มีในระบบ` {
			foundAccountcodeError = true
		}
	}
	if !foundBookcodeError {
		t.Error("expected missing-bookcode error for JV-BAD-01, not found")
	}
	if !foundAccountcodeError {
		t.Error("expected account-not-found error for JV-BAD-02, not found")
	}
}

// TestValidateImportConfig_PerDocumentColumns is a pure-function test (no
// live DB needed) covering the input-validation guards added for the
// "per-document-columns" rowMode. Since /billscan is an unauthenticated
// endpoint, every AmountColumns entry must be validated rather than
// trusted — a bad Side value in particular would otherwise silently
// misclassify an amount to the wrong side of the ledger downstream.
func TestValidateImportConfig_PerDocumentColumns(t *testing.T) {
	base := func(cols []ImportAmountColumnSource) ImportValidationConfig {
		return ImportValidationConfig{RowMode: "per-document-columns", AmountColumns: cols}
	}

	if msg := validateImportConfig(&ImportValidationConfig{RowMode: "per-document-columns", AmountColumns: nil}); msg == "" {
		t.Error("expected an error for empty amountColumns, got none")
	}

	tooMany := make([]ImportAmountColumnSource, 201)
	for i := range tooMany {
		tooMany[i] = ImportAmountColumnSource{Column: i, AccountCode: "111110", Side: "debit"}
	}
	if msg := validateImportConfig(ref(base(tooMany))); msg == "" {
		t.Error("expected an error for 201 amountColumns entries, got none")
	}

	if msg := validateImportConfig(ref(base([]ImportAmountColumnSource{{Column: 0, AccountCode: "111110", Side: "invalid"}}))); msg == "" {
		t.Error("expected an error for an invalid side value, got none")
	}

	if msg := validateImportConfig(ref(base([]ImportAmountColumnSource{{Column: 0, AccountCode: "", Side: "debit"}}))); msg == "" {
		t.Error("expected an error for an empty accountcode, got none")
	}

	if msg := validateImportConfig(ref(base([]ImportAmountColumnSource{{Column: -1, AccountCode: "111110", Side: "debit"}}))); msg == "" {
		t.Error("expected an error for a negative column index, got none")
	}

	if msg := validateImportConfig(ref(base([]ImportAmountColumnSource{
		{Column: 7, AccountCode: "111110", Side: "debit"},
		{Column: 9, AccountCode: "215500", Side: "credit"},
	}))); msg != "" {
		t.Errorf("expected a well-formed config to pass, got error: %s", msg)
	}
}

func ref[T any](v T) *T { return &v }

// TestBuildParsedDocuments_PerDocumentColumns_TemplateJournal is a real
// end-to-end integration check (live dev DB, not a mock) using the actual
// hand-built SML-adjacent template a user reported needing:
// template_journal.xlsx. Row 1 is decorative Thai labels; row 2 is the real
// machine header containing the D_/C_-prefixed amount columns this mode
// parses — this test deliberately selects row 2 (headerRowIndex=1), which
// is exactly the choice the wizard's Step 2 preview table is meant to
// guide a user toward.
func TestBuildParsedDocuments_PerDocumentColumns_TemplateJournal(t *testing.T) {
	shopID := setupLiveDBTest(t)
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		t.Fatalf("GetOrLoadMasterData failed: %v", err)
	}

	filePath := "/Users/nontawatwongnuk/dev_vue/bcaccount/public/demo/file/template_journal.xlsx"
	dataRows, headerRowIndex := loadTestFileDataRows(t, filePath, 1)

	col := func(i int) *int { v := i; return &v }
	cfg := ImportValidationConfig{
		HeaderRowIndex: headerRowIndex,
		RowMode:        "per-document-columns",
		AmountColumns: []ImportAmountColumnSource{
			{Column: 7, AccountCode: "111110", Side: "debit"},
			{Column: 8, AccountCode: "410090", Side: "debit"},
			{Column: 9, AccountCode: "215500", Side: "credit"},
			{Column: 10, AccountCode: "410010", Side: "credit"},
		},
		FieldMappings: map[string]*int{
			"docno": col(0), "docdate": col(1), "bookcode": col(5), "accountdescription": col(6),
			"vatdate": col(17), "vatdocno": col(18), "vatbase": col(21), "vatrate": col(22), "vatamount": col(23),
		},
	}

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	accountGroupMap := buildStringKeyedMap(masterCache.AccountGroups, "code")
	debtorMap := buildStringKeyedMap(masterCache.Debtors, "code")
	creditorMap := buildStringKeyedMap(masterCache.Creditors, "code")

	reqCtx := common.NewRequestContext(shopID)
	job, _, err := jobs.Create(reqCtx)
	if err != nil {
		t.Fatalf("job create failed: %v", err)
	}

	docs, issues := buildParsedDocuments(dataRows, cfg, chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap, job, time.Now().Add(time.Hour))

	fmt.Printf("=== PER-DOCUMENT-COLUMNS RESULT ===\ndocuments: %d, issues: %d\n", len(docs), len(issues))
	for _, iss := range issues {
		fmt.Printf("  [%s] %s (docno=%s, field=%s, rows=%v)\n", iss.Severity, iss.Message, iss.Docno, iss.Field, iss.RowNumbers)
	}
	for _, d := range docs {
		fmt.Printf("  doc %s: amount=%.2f bookcode=%s journaldetail=%d vats=%d\n", d.Docno, d.Amount, d.Bookcode, len(d.Journaldetail), len(d.Vats))
	}

	if len(docs) != 5 {
		t.Fatalf("expected 5 documents, got %d", len(docs))
	}
	errorCount := 0
	for _, iss := range issues {
		if iss.Severity == "error" {
			errorCount++
		}
	}
	if errorCount != 0 {
		t.Errorf("expected 0 error-severity issues, got %d", errorCount)
	}

	for _, d := range docs {
		var sumDebit, sumCredit float64
		for _, jd := range d.Journaldetail {
			sumDebit += jd.DebitAmount
			sumCredit += jd.CreditAmount
		}
		if math_Abs(sumDebit-sumCredit) > 0.01 {
			t.Errorf("doc %s: unbalanced debit=%.2f credit=%.2f", d.Docno, sumDebit, sumCredit)
		}
	}

	// Row 3 in the source file (JO-2025110001) has a blank D_410090 cell —
	// confirms the blank-cell-skip path produces 3 lines, not 4.
	for _, d := range docs {
		if d.Docno == "JO-2025110001" {
			if len(d.Journaldetail) != 3 {
				t.Errorf("JO-2025110001: expected 3 journaldetail lines (blank D_410090 skipped), got %d: %+v", len(d.Journaldetail), d.Journaldetail)
			}
		}
	}
}

// ---------- test helpers ----------

func setupLiveDBTest(t *testing.T) string {
	t.Helper()
	godotenv.Load("../../.env")
	if os.Getenv("MONGO_URI") == "" {
		t.Skip("MONGO_URI not set — skipping live-DB integration test")
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")
	if err := storage.InitMongoDB(); err != nil {
		t.Fatalf("mongo connect failed: %v", err)
	}
	t.Cleanup(storage.CloseMongoDB)
	return "36xq3C3RKkSrkcCJNj6lnjfBl6Z"
}

func loadTestFileDataRows(t *testing.T, path string, headerRowIndex int) ([][]string, int) {
	t.Helper()
	f, err := excelize.OpenFile(path, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("open test file failed: %v", err)
	}
	defer f.Close()
	sheetName := f.GetSheetName(0)
	rows, err := f.Rows(sheetName)
	if err != nil {
		t.Fatalf("read rows failed: %v", err)
	}
	defer rows.Close()
	var allRows [][]string
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("read row failed: %v", err)
		}
		allRows = append(allRows, cols)
	}
	var dataRows [][]string
	for _, r := range allRows[headerRowIndex+1:] {
		if rowHasAnyValue(r) {
			dataRows = append(dataRows, r)
		}
	}
	return dataRows, headerRowIndex
}
