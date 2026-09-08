// import_validation.go - Excel journal-import validation pipeline.
//
// This is a faithful, rule-for-rule port of the client-side logic that used
// to live entirely in bcaccount's ImportAccountingEntries.vue
// (buildParsedDocuments() + runDuplicateCheck()). Every validation rule,
// error/warning message string (kept in Thai, verbatim), and edge case here
// must match that JS implementation exactly — this is not a redesign, and
// any behavioral difference from what users already see today is a
// regression, not an improvement, even where the new behavior might
// arguably be "better." Kept in its own file (not handlers.go, which is
// already 1800+ lines) since this is a distinct, self-contained pipeline.
//
// Known limitation, not addressed here: the /billscan routes (including the
// one that triggers this pipeline) have no authentication, unlike the main
// accounting API. Moving business-validation logic (account codes, amounts,
// customer/vendor data) onto this unauthenticated surface was a deliberate,
// explicitly-accepted tradeoff for this pass — flagged here so it isn't
// silently forgotten, not because it's considered acceptable long-term.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/jobs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
	"go.mongodb.org/mongo-driver/bson"
)

// ---------- Request/response types ----------

// ImportFieldSource mirrors debitSource/creditSource in ImportAccountingEntries.vue:
// { mode: 'column'|'fixed', column: number|null, accountcode: string|null }
type ImportFieldSource struct {
	Mode        string  `json:"mode"`
	Column      *int    `json:"column"`
	AccountCode *string `json:"accountcode"`
}

// ImportAmountColumnSource mirrors one auto-detected D_/C_ header-embedded
// amount column from the frontend's detectedAmountColumns computed —
// resolved client-side from header text (e.g. "D_111110" -> debit into
// accountcode "111110"), sent here already-resolved. Same division of
// labor as DebitSource/CreditSource: the frontend interprets column
// headers, this backend just applies the result. Unlike ImportFieldSource,
// there is no "fixed" mode — a D_/C_ header always names exactly one
// column, one accountcode, one side.
type ImportAmountColumnSource struct {
	Column      int    `json:"column"`
	AccountCode string `json:"accountcode"`
	Side        string `json:"side"` // "debit" | "credit"
}

// ImportValidationConfig mirrors the Vue component's Step 2/3 state exactly —
// serialized as-is from fieldMappings/rowMode/etc., no new client-side data
// model needed.
type ImportValidationConfig struct {
	HeaderRowIndex     int                        `json:"headerRowIndex"`
	RowMode            string                     `json:"rowMode"`         // "per-line" | "per-document-2-line" | "per-document-columns"
	AmountSplitMode    string                     `json:"amountSplitMode"` // "separate" | "signed-single-column"
	DebitSource        ImportFieldSource          `json:"debitSource"`
	CreditSource       ImportFieldSource          `json:"creditSource"`
	SignedAmountColumn *int                       `json:"signedAmountColumn"`
	DebitAmountColumn  *int                       `json:"debitAmountColumn"`
	CreditAmountColumn *int                       `json:"creditAmountColumn"`
	AmountColumns      []ImportAmountColumnSource `json:"amountColumns"` // "per-document-columns" only
	FieldMappings      map[string]*int            `json:"fieldMappings"`

	// Wizard-level VAT/WHT classification — one value for the whole import
	// batch, not per-row column mapping (restores fields dropped when this
	// importer moved off the old fixed-Excel-header VATTYPE/VATMODE/
	// CUSTTYPE/WHTMODE columns). Required only when the batch actually
	// carries VAT/WHT data (FieldMappings["vatdocno"]/["taxdocno"] set) —
	// see validateImportConfig. nil means "not provided", distinct from a
	// real 0 value, same as SignedAmountColumn above.
	VatMode          *int `json:"vatMode"`          // 0=ภาษีซื้อ, 1=ภาษีขาย
	VatType          *int `json:"vatType"`          // range depends on VatMode — see validateImportConfig
	VatOrganization  *int `json:"vatOrganization"`  // 0=สำนักงานใหญ่, 1=สาขา
	WhtTaxType       *int `json:"whtTaxType"`       // 0=ถูกหัก ณ ที่จ่าย, 1=หัก ณ ที่จ่าย
	WhtCustType      *int `json:"whtCustType"`      // 0-4, customer/WHT-form type
	WhtConditionType *int `json:"whtConditionType"` // 1-3, only meaningful when WhtTaxType == 1
}

// ImportIssue mirrors the JS `issues` array entries exactly.
type ImportIssue struct {
	Severity   string `json:"severity"` // "error" | "warning"
	RowNumbers []int  `json:"rowNumbers"`
	Docno      string `json:"docno"`
	Field      string `json:"field"`
	Message    string `json:"message"`
}

// ImportJournalDetail mirrors journaldetail[] entries.
type ImportJournalDetail struct {
	AccountCode  string  `json:"accountcode"`
	AccountName  string  `json:"accountname"`
	DebitAmount  float64 `json:"debitamount"`
	CreditAmount float64 `json:"creditamount"`
}

// ImportVat mirrors the vats[] entry shape sent to POST /gl/journal/bulk.
type ImportVat struct {
	VatDocNo     string  `json:"vatdocno"`
	VatType      int     `json:"vattype"`
	VatDate      string  `json:"vatdate"`
	VatPeriod    int     `json:"vatperiod"`
	VatYear      int     `json:"vatyear"`
	VatBase      float64 `json:"vatbase"`
	VatRate      float64 `json:"vatrate"`
	VatAmount    float64 `json:"vatamount"`
	ExceptVat    int     `json:"exceptvat"`
	VatMode      int     `json:"vatmode"`
	VatSubmit    bool    `json:"vatsubmit"`
	CustCode     string  `json:"custcode"`
	CustName     string  `json:"custname"`
	CustTaxID    string  `json:"custtaxid"`
	Organization int     `json:"organization"`
	BranchCode   string  `json:"branchcode"`
	Remark       string  `json:"remark"`
}

// ImportWhtDetail mirrors taxes[].details[] entries.
type ImportWhtDetail struct {
	Description string  `json:"description"`
	TaxBase     float64 `json:"taxbase"`
	TaxRate     float64 `json:"taxrate"`
	TaxAmount   float64 `json:"taxamount"`
}

// ImportTax mirrors the taxes[] entry shape sent to POST /gl/journal/bulk.
type ImportTax struct {
	TaxDocNo         string            `json:"taxdocno"`
	TaxDate          string            `json:"taxdate"`
	CustName         string            `json:"custname"`
	CustType         int               `json:"custtype"`
	CustTaxID        string            `json:"custtaxid"`
	TaxType          int               `json:"taxtype"`
	ConditionTaxType int               `json:"conditiontaxtype"`
	Address          string            `json:"address"`
	TaxAmount        float64           `json:"taxamount"`
	Details          []ImportWhtDetail `json:"details"`
}

// ImportDocument mirrors exactly what confirmSave()'s postData builder sends
// to POST /gl/journal/bulk today — the frontend's Step 5 save call is
// unchanged, so this shape must match its expectations field-for-field.
type ImportDocument struct {
	Docno              string                 `json:"docno"`
	Docdate            string                 `json:"docdate"`
	Bookcode           string                 `json:"bookcode"`
	Accountgroup       string                 `json:"accountgroup"`
	Accountdescription string                 `json:"accountdescription"`
	Accountperiod      int                    `json:"accountperiod"`
	Accountyear        int                    `json:"accountyear"`
	Journaltype        int                    `json:"journaltype"`
	Amount             float64                `json:"amount"`
	BatchID            string                 `json:"batchId"`
	Exdocrefno         string                 `json:"exdocrefno"`
	Exdocrefdate       *string                `json:"exdocrefdate"` // null, not "", when absent — the backend's own date parser rejects ""
	Journaldetail      []ImportJournalDetail  `json:"journaldetail"`
	Parid              string                 `json:"parid"`
	Vats               []ImportVat            `json:"vats"`
	Taxes              []ImportTax            `json:"taxes"`
	Debtaccounttype    int                    `json:"debtaccounttype"`
	Debtor             map[string]interface{} `json:"debtor"`
	Creditor           map[string]interface{} `json:"creditor"`
	RowNumbers         []int                  `json:"rowNumbers"` // for Step 4's issue-click-to-filter UI
}

// ---------- Handler ----------

func SubmitImportValidationHandler(c *gin.Context) {
	shopID := c.PostForm("shopid")
	if shopID == "" {
		c.JSON(400, gin.H{"error": "shopid is required"})
		return
	}

	configJSON := c.PostForm("config")
	if configJSON == "" {
		c.JSON(400, gin.H{"error": "config is required"})
		return
	}
	var cfg ImportValidationConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		c.JSON(400, gin.H{"error": "invalid config JSON", "details": err.Error()})
		return
	}
	if validationErr := validateImportConfig(&cfg); validationErr != "" {
		c.JSON(400, gin.H{"error": "invalid config", "message": validationErr})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "file is required", "details": err.Error()})
		return
	}
	defer file.Close()

	// Same content-type allow-list pattern as SubmitTestTemplateHandler.
	contentType := header.Header.Get("Content-Type")
	validTypes := map[string]bool{
		"application/vnd.ms-excel": true,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
	}
	lowerName := strings.ToLower(header.Filename)
	isValidExt := strings.HasSuffix(lowerName, ".xls") || strings.HasSuffix(lowerName, ".xlsx")
	if !validTypes[contentType] && !isValidExt {
		c.JSON(400, gin.H{"error": "invalid file type", "message": "กรุณาเลือกไฟล์ Excel (.xls หรือ .xlsx)"})
		return
	}

	maxBytes := int64(configs.EXCEL_IMPORT_MAX_FILE_SIZE_MB) * 1024 * 1024
	if header.Size > maxBytes {
		c.JSON(400, gin.H{
			"error":   "file too large",
			"message": fmt.Sprintf("ไฟล์มีขนาดใหญ่เกินไป (สูงสุด %d MB)", configs.EXCEL_IMPORT_MAX_FILE_SIZE_MB),
		})
		return
	}

	// Master-data availability is checked synchronously before a job is ever
	// created — mirrors SubmitAnalyzeReceiptHandler's own pattern of
	// surfacing master-data problems immediately rather than as an async
	// job failure.
	masterCache, err := storage.GetOrLoadMasterData(shopID)
	if err != nil {
		c.JSON(400, gin.H{"error": "master_data_load_failed", "message": err.Error()})
		return
	}
	if len(masterCache.Accounts) == 0 || len(masterCache.JournalBooks) == 0 {
		c.JSON(400, gin.H{
			"error":   "master_data_not_found",
			"message": "ไม่พบผังบัญชีหรือสมุดรายวันสำหรับร้านค้านี้",
		})
		return
	}

	// Save the multipart file to disk synchronously in this handler goroutine
	// — the multipart stream is only valid for this request, exact same
	// reasoning/pattern as SubmitTestTemplateHandler's temp-file handling.
	tempFilename := fmt.Sprintf("%s%s", uuid.New().String(), filepath.Ext(header.Filename))
	tempFilePath := filepath.Join(configs.UPLOAD_DIR, tempFilename)
	out, err := os.Create(tempFilePath)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to save uploaded file", "details": err.Error()})
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		os.Remove(tempFilePath)
		c.JSON(500, gin.H{"error": "failed to save uploaded file", "details": err.Error()})
		return
	}
	out.Close()

	reqCtx := common.NewRequestContext(shopID)
	job, token, err := jobs.Create(reqCtx)
	if err != nil {
		os.Remove(tempFilePath) // no goroutine will run to clean this up otherwise
		c.JSON(503, gin.H{
			"error":   "too_many_active_jobs",
			"message": "ระบบกำลังประมวลผลงานจำนวนมาก กรุณาลองใหม่อีกครั้งในอีกสักครู่",
		})
		return
	}

	go runImportValidationPipeline(job, shopID, tempFilePath, cfg, masterCache)

	c.JSON(202, gin.H{
		"status": "processing",
		"job": gin.H{
			"id":            job.ID,
			"token":         token,
			"progress":      job.Snapshot().Progress,
			"poll_after_ms": 2000,
		},
	})
}

// validateImportConfig duplicates the client-side rowModeConfigValid check
// server-side, so a job is never created for a config that can't possibly
// validate (e.g. per-document-2-line mode with neither debit nor credit
// source configured).
func validateImportConfig(cfg *ImportValidationConfig) string {
	if cfg.RowMode != "per-line" && cfg.RowMode != "per-document-2-line" && cfg.RowMode != "per-document-columns" {
		return "rowMode must be 'per-line', 'per-document-2-line', or 'per-document-columns'"
	}

	// VAT/WHT classification — applies regardless of rowMode, so it's
	// checked before the rowMode-specific branches below rather than inside
	// any one of them. Same unauthenticated-endpoint defense-in-depth
	// posture as the AmountColumns check further down: every enum value is
	// whitelisted against its real valid range, not trusted as-is, since an
	// out-of-range value would otherwise silently misclassify a document
	// into the wrong tax report (this is the exact bug being fixed here).
	if vatErr := validateVatClassification(cfg); vatErr != "" {
		return vatErr
	}
	if whtErr := validateWhtClassification(cfg); whtErr != "" {
		return whtErr
	}

	if cfg.RowMode == "per-document-columns" {
		// AmountColumns is a JSON-decoded slice arriving over an
		// unauthenticated endpoint (see this file's own header comment on
		// the /billscan auth gap) — validate every entry rather than
		// trusting it, since a bad Side value would otherwise silently
		// misclassify an amount to the wrong side of the ledger downstream.
		if len(cfg.AmountColumns) == 0 {
			return "amountColumns must contain at least one D_/C_ column when rowMode is 'per-document-columns'"
		}
		if len(cfg.AmountColumns) > 200 {
			return "amountColumns has too many entries (max 200)"
		}
		for i, ac := range cfg.AmountColumns {
			if ac.Side != "debit" && ac.Side != "credit" {
				return fmt.Sprintf("amountColumns[%d].side must be 'debit' or 'credit', got %q", i, ac.Side)
			}
			if strings.TrimSpace(ac.AccountCode) == "" {
				return fmt.Sprintf("amountColumns[%d].accountcode is required", i)
			}
			if ac.Column < 0 {
				return fmt.Sprintf("amountColumns[%d].column must be non-negative", i)
			}
		}
		return ""
	}

	if cfg.RowMode != "per-document-2-line" {
		return ""
	}
	debitOk := (cfg.DebitSource.Mode == "fixed" && cfg.DebitSource.AccountCode != nil && *cfg.DebitSource.AccountCode != "") ||
		(cfg.DebitSource.Mode == "column" && cfg.DebitSource.Column != nil)
	creditOk := (cfg.CreditSource.Mode == "fixed" && cfg.CreditSource.AccountCode != nil && *cfg.CreditSource.AccountCode != "") ||
		(cfg.CreditSource.Mode == "column" && cfg.CreditSource.Column != nil)
	if !debitOk || !creditOk {
		return "debitSource/creditSource must resolve to a fixed accountcode or a mapped column"
	}
	amountOk := false
	if cfg.AmountSplitMode == "signed-single-column" {
		amountOk = cfg.SignedAmountColumn != nil
	} else {
		amountOk = cfg.DebitAmountColumn != nil || cfg.CreditAmountColumn != nil
	}
	if !amountOk {
		return "amount column configuration is incomplete"
	}
	return ""
}

// validateVatClassification checks VatMode/VatType/VatOrganization: required
// together when the batch is mapped to carry VAT data (fieldMappings has a
// vatdocno column), each whitelisted against its real valid range. VatType's
// valid range depends on VatMode (mirrors JournalTaxInfoTab.vue's
// getVatTypeOptions(vatmode) on the frontend) — vatmode=0 (ภาษีซื้อ) allows
// {0,1,2}, vatmode=1 (ภาษีขาย) allows {0,1}.
//
// Each of VatMode/VatType/VatOrganization can alternatively come from a
// mapped "vatmode"/"vattype"/"vatorganization" column instead of this
// wizard-level value — in that case the real per-row value isn't known until
// buildParsedDocuments scans the source rows, so the wizard-level
// requirement/range-check for that one field is skipped here;
// buildParsedDocuments re-validates the actual resolved value per document
// instead, so a bad per-row cell still can't silently default to 0.
func validateVatClassification(cfg *ImportValidationConfig) string {
	hasVatColumn := cfg.FieldMappings["vatdocno"] != nil
	if !hasVatColumn {
		return ""
	}
	hasVatModeColumn := cfg.FieldMappings["vatmode"] != nil
	hasVatTypeColumn := cfg.FieldMappings["vattype"] != nil
	hasVatOrganizationColumn := cfg.FieldMappings["vatorganization"] != nil
	if !hasVatOrganizationColumn && cfg.VatOrganization == nil {
		return "vatOrganization is required when a vatdocno column is mapped and no vatorganization column is mapped"
	}
	if !hasVatModeColumn && cfg.VatMode == nil {
		return "vatMode is required when a vatdocno column is mapped and no vatmode column is mapped"
	}
	if !hasVatTypeColumn && cfg.VatType == nil {
		return "vatType is required when a vatdocno column is mapped and no vattype column is mapped"
	}
	if !hasVatModeColumn {
		if *cfg.VatMode != 0 && *cfg.VatMode != 1 {
			return fmt.Sprintf("vatMode must be 0 or 1, got %d", *cfg.VatMode)
		}
	}
	if !hasVatModeColumn && !hasVatTypeColumn {
		if *cfg.VatMode == 0 {
			if *cfg.VatType < 0 || *cfg.VatType > 2 {
				return fmt.Sprintf("vatType must be 0-2 when vatMode is 0 (ภาษีซื้อ), got %d", *cfg.VatType)
			}
		} else {
			if *cfg.VatType < 0 || *cfg.VatType > 1 {
				return fmt.Sprintf("vatType must be 0-1 when vatMode is 1 (ภาษีขาย), got %d", *cfg.VatType)
			}
		}
	}
	if !hasVatOrganizationColumn {
		if *cfg.VatOrganization != 0 && *cfg.VatOrganization != 1 {
			return fmt.Sprintf("vatOrganization must be 0 or 1, got %d", *cfg.VatOrganization)
		}
	}
	return ""
}

// validateWhtClassification checks WhtTaxType/WhtCustType/WhtConditionType:
// the first two required together when the batch is mapped to carry WHT
// data (fieldMappings has a taxdocno column); WhtConditionType is only
// meaningful when WhtTaxType is 1 (หัก ณ ที่จ่าย) — mirrors the display-side
// gating in JournalDetailPanel.vue (`v-if="tax.taxtype === 1"` around its
// conditiontaxtype tag).
func validateWhtClassification(cfg *ImportValidationConfig) string {
	hasWhtColumn := cfg.FieldMappings["taxdocno"] != nil
	if !hasWhtColumn {
		return ""
	}
	if cfg.WhtTaxType == nil || cfg.WhtCustType == nil {
		return "whtTaxType and whtCustType are required when a taxdocno column is mapped"
	}
	if *cfg.WhtTaxType != 0 && *cfg.WhtTaxType != 1 {
		return fmt.Sprintf("whtTaxType must be 0 or 1, got %d", *cfg.WhtTaxType)
	}
	if *cfg.WhtCustType < 0 || *cfg.WhtCustType > 4 {
		return fmt.Sprintf("whtCustType must be 0-4, got %d", *cfg.WhtCustType)
	}
	if cfg.WhtConditionType != nil {
		if *cfg.WhtTaxType != 1 {
			return "whtConditionType is only valid when whtTaxType is 1"
		}
		if *cfg.WhtConditionType < 1 || *cfg.WhtConditionType > 3 {
			return fmt.Sprintf("whtConditionType must be 1-3, got %d", *cfg.WhtConditionType)
		}
	}
	return ""
}

// ---------- Pipeline ----------

func runImportValidationPipeline(job *jobs.Job, shopID, tempFilePath string, cfg ImportValidationConfig, masterCache *storage.MasterDataCache) {
	defer os.Remove(tempFilePath) // fires on every exit path, including job.Fail — same discipline as runTestTemplatePipeline

	deadline := time.Now().Add(time.Duration(configs.EXCEL_IMPORT_TIMEOUT_SEC) * time.Second)

	job.UpdateProgress(2, "parsing", "กำลังอ่านไฟล์ Excel")

	// RawCellValue is essential here, not optional: without it, a native
	// Excel date cell comes back as whatever display string that cell's
	// number format happens to produce (verified empirically — e.g.
	// "08-01-26" for an m-d-yy format), which is unpredictable across
	// real-world files and impossible to parse reliably. With it, a
	// number-formatted date cell returns its raw serial number instead
	// (parsed via excelize.ExcelDateToTime below), while a cell that's
	// genuinely text (e.g. a date typed as a plain string like
	// "2026-08-01") is unaffected and still comes back as that string —
	// so this one option correctly covers both real-world cases.
	f, err := excelize.OpenFile(tempFilePath, excelize.Options{RawCellValue: true})
	if err != nil {
		job.Fail("file_parse_failed", fmt.Sprintf("ไม่สามารถอ่านไฟล์ได้ กรุณาตรวจสอบรูปแบบไฟล์: %v", err))
		return
	}
	defer f.Close()

	sheetName := f.GetSheetName(0)
	if sheetName == "" {
		job.Fail("file_parse_failed", "ไม่พบข้อมูลในไฟล์")
		return
	}

	// Streaming row reader — GetRows() would load the entire sheet into
	// memory as [][]string up front, which is exactly the memory blowup this
	// migration exists to avoid for files up to EXCEL_IMPORT_MAX_ROWS rows.
	rows, err := f.Rows(sheetName)
	if err != nil {
		job.Fail("file_parse_failed", fmt.Sprintf("ไม่สามารถอ่านข้อมูลในไฟล์ได้: %v", err))
		return
	}
	defer rows.Close()

	var allRows [][]string
	rowIdx := -1
	for rows.Next() {
		rowIdx++
		cols, err := rows.Columns()
		if err != nil {
			job.Fail("file_parse_failed", fmt.Sprintf("ไม่สามารถอ่านแถวที่ %d ได้: %v", rowIdx+1, err))
			return
		}
		allRows = append(allRows, cols)
		if len(allRows) > configs.EXCEL_IMPORT_MAX_ROWS+cfg.HeaderRowIndex+1 {
			job.Fail("file_too_large", fmt.Sprintf("ไฟล์มีจำนวนแถวมากเกินไป (สูงสุด %d แถว)", configs.EXCEL_IMPORT_MAX_ROWS))
			return
		}
	}

	if cfg.HeaderRowIndex >= len(allRows) {
		job.Fail("file_parse_failed", "แถวหัวตารางที่เลือกไม่มีอยู่ในไฟล์")
		return
	}

	// dataRows: everything after the header row, filtering out fully-blank
	// rows — matches dataRows computed() exactly.
	var dataRows [][]string
	for _, r := range allRows[cfg.HeaderRowIndex+1:] {
		if rowHasAnyValue(r) {
			dataRows = append(dataRows, r)
		}
	}

	job.UpdateProgress(10, "loading_masterdata", "กำลังเตรียมข้อมูลผังบัญชี")

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	accountGroupMap := buildStringKeyedMap(masterCache.AccountGroups, "code")
	debtorMap := buildStringKeyedMap(masterCache.Debtors, "code")
	creditorMap := buildStringKeyedMap(masterCache.Creditors, "code")

	var issues []ImportIssue
	docs, issues := buildParsedDocuments(dataRows, cfg, chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap, job, deadline)
	if docs == nil && issues == nil {
		// buildParsedDocuments already called job.Fail (deadline exceeded mid-loop)
		return
	}

	job.UpdateProgress(75, "checking_duplicates", "กำลังตรวจสอบเลขที่เอกสารซ้ำ")

	docnos := make([]string, 0, len(docs))
	for _, d := range docs {
		docnos = append(docnos, d.Docno)
	}
	existing, err := checkDocnosExistChunked(shopID, docnos)
	if err != nil {
		// Best-effort, non-blocking — same philosophy as the old client-side
		// duplicate check: a failed check must never prevent the user from
		// proceeding, only skip its own warning.
		existing = map[string]bool{}
	}
	rowNumbersByDocno := make(map[string][]int, len(docs))
	for _, d := range docs {
		rowNumbersByDocno[d.Docno] = d.RowNumbers
	}
	for docno := range existing {
		issues = append(issues, ImportIssue{
			Severity:   "warning",
			RowNumbers: rowNumbersByDocno[docno],
			Docno:      docno,
			Field:      "docno",
			Message:    fmt.Sprintf(`เลขที่เอกสาร "%s" มีอยู่ในระบบแล้ว จะถูกสร้างซ้ำหากดำเนินการต่อ`, docno),
		})
	}

	errorCount, warningCount := 0, 0
	for _, iss := range issues {
		if iss.Severity == "error" {
			errorCount++
		} else {
			warningCount++
		}
	}

	job.Complete(gin.H{
		"status":    "success",
		"documents": docs,
		"issues":    issues,
		"summary": gin.H{
			"documentCount": len(docs),
			"errorCount":    errorCount,
			"warningCount":  warningCount,
		},
	})
}

// checkDocnosExistChunked calls storage.CheckJournalDocnosExist in chunks of
// 500 (same size the frontend already chunks at for the HTTP-facing
// CheckDocnosExistHandler, kept consistent here even though this calls the
// storage function directly and isn't bound by that handler's 1000-per-call
// cap) to bound single-query cost for very large unique-docno counts.
func checkDocnosExistChunked(shopID string, docnos []string) (map[string]bool, error) {
	const chunkSize = 500
	result := make(map[string]bool)
	for i := 0; i < len(docnos); i += chunkSize {
		end := i + chunkSize
		if end > len(docnos) {
			end = len(docnos)
		}
		chunk := docnos[i:end]
		found, err := storage.CheckJournalDocnosExist(shopID, chunk)
		if err != nil {
			return result, err
		}
		for _, docno := range found {
			result[docno] = true
		}
	}
	return result, nil
}

// buildStringKeyedMap builds a map[string]bson.M keyed by the given field —
// O(1) per-row lookups instead of a linear scan repeated per row, mirroring
// the JS side's Map-building discipline exactly (the JS version already
// does this; a naive Go port that re-scanned a slice per row across
// thousands of rows × 5 lookup types would be far slower server-side than
// the old client-side version ever was).
func buildStringKeyedMap(docs []bson.M, keyField string) map[string]bson.M {
	m := make(map[string]bson.M, len(docs))
	for _, d := range docs {
		if key, ok := d[keyField].(string); ok && key != "" {
			m[key] = d
		}
	}
	return m
}

func rowHasAnyValue(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return true
		}
	}
	return false
}

func cellAt(row []string, colIndex *int) string {
	if colIndex == nil || *colIndex < 0 || *colIndex >= len(row) {
		return ""
	}
	return row[*colIndex]
}

// cellText mirrors cellText(row, colIndex) exactly.
func cellText(row []string, colIndex *int) string {
	return strings.TrimSpace(cellAt(row, colIndex))
}

// cellNumber mirrors cellNumber(row, colIndex) exactly — returns nil (not a
// pointer to 0) when blank or unparseable, matching JS null.
func cellNumber(row []string, colIndex *int) *float64 {
	raw := cellAt(row, colIndex)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return nil
	}
	return &n
}

func numberOrZero(n *float64) float64 {
	if n == nil {
		return 0
	}
	return *n
}

// getDateTimeFromDate mirrors the JS helper: excelize already returns date
// cells as formatted date strings (not raw serials) when the column has a
// date number format, so this mainly needs to parse a handful of common
// layouts and fall back to "" (JS's own empty-string sentinel) on failure —
// the caller then decides whether "" should become nil for the payload
// (only exdocrefdate does; docdate/vatdate/taxdate are validated as
// required-if-applicable before ever reaching the save payload, exactly
// like the JS side).
// getDateTimeFromDate mirrors the JS helper's contract (return "" on
// anything unparseable, an ISO string otherwise). The file is opened with
// excelize.Options{RawCellValue: true}, so a genuine Excel date cell always
// arrives here as a raw numeric serial (verified empirically — a cell
// formatted m-d-yy would otherwise come back as "08-01-26", an ambiguous,
// unparseable display string that varies per file); only a cell that's
// actually typed as literal text (e.g. a user typed "2026-08-01" as a
// string, not a real date value) arrives as one of the string layouts
// below. Numeric-serial is checked first since that's the primary path for
// real-world Excel files built by an actual spreadsheet date picker.
func getDateTimeFromDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if serial, err := strconv.ParseFloat(raw, 64); err == nil {
		if t, err := excelize.ExcelDateToTime(serial, false); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"01/02/2006",
		"02/01/2006",
		"2006/01/02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}
	return ""
}

func excelRowNumber(dataRowIdx, headerRowIndex int) int {
	return dataRowIdx + headerRowIndex + 2
}

// ---------- buildParsedDocuments — the Go port of the JS function of the
// same name (ImportAccountingEntries.vue). Structure and ordering follow
// the JS source exactly: Phase A (raw line construction per row), Phase B
// (group into documents by docno), then per-document validation in the same
// order the JS does it. Every message string is kept verbatim in Thai. ----------

type rawLine struct {
	AccountCode  string
	DebitAmount  float64
	CreditAmount float64
	RowNum       int
}

type rawLineItem struct {
	RowNum  int
	Docno   string
	Docdate string
	Row     []string
	Lines   []rawLine
}

type docGroup struct {
	RowNums    []int
	Docdate    string
	Lines      []rawLine
	SourceRow  []string
	SourceRows []sourceRowEntry
}

type sourceRowEntry struct {
	Row    []string
	RowNum int
}

type collectedField struct {
	Value       string
	NumberValue *float64
	HasValue    bool
	HasConflict bool
	RowNums     []int
}

func buildParsedDocuments(
	dataRows [][]string,
	cfg ImportValidationConfig,
	chartOfAccountsMap, journalBookMap, accountGroupMap, debtorMap, creditorMap map[string]bson.M,
	job *jobs.Job,
	deadline time.Time,
) ([]ImportDocument, []ImportIssue) {
	var issues []ImportIssue
	fm := cfg.FieldMappings

	// Phase A — raw line construction, one item per source row.
	rawLines := make([]rawLineItem, 0, len(dataRows))
	totalRows := len(dataRows)
	lastProgressReported := -1
	for idx, row := range dataRows {
		// Deadline checked inside the loop itself (not just at stage
		// boundaries) — this is the pipeline's dominant cost, so a
		// stage-boundary-only check would let a pathological run go
		// unbounded.
		if idx%500 == 0 && time.Now().After(deadline) {
			job.Fail("PROCESSING_TIMEOUT", fmt.Sprintf("การประมวลผลใช้เวลานานเกิน %d วินาที กรุณาลองใหม่ด้วยไฟล์ที่มีขนาดเล็กลง", configs.EXCEL_IMPORT_TIMEOUT_SEC))
			return nil, nil
		}

		// Progress within the dominant stage is computed from
		// processedRows/totalRows, not a fixed jump — otherwise the bar
		// sits at one number for most of the run, which reads as "stuck."
		if totalRows > 0 {
			percent := 15 + int(float64(idx)/float64(totalRows)*55) // 15%..70%
			if percent != lastProgressReported && idx%200 == 0 {
				job.UpdateProgress(percent, "building_documents", fmt.Sprintf("กำลังตรวจสอบข้อมูล (%d/%d แถว)", idx, totalRows))
				lastProgressReported = percent
			}
		}

		rowNum := excelRowNumber(idx, cfg.HeaderRowIndex)
		docno := cellText(row, fm["docno"])
		var docdate string
		if dc, ok := fm["docdate"]; ok && dc != nil {
			docdate = getDateTimeFromDate(cellAt(row, dc))
		}

		if cfg.RowMode == "per-line" {
			accountcode := cellText(row, fm["accountcode"])
			debitamount := numberOrZero(cellNumber(row, fm["debitamount"]))
			creditamount := numberOrZero(cellNumber(row, fm["creditamount"]))
			rawLines = append(rawLines, rawLineItem{
				RowNum: rowNum, Docno: docno, Docdate: docdate, Row: row,
				Lines: []rawLine{{AccountCode: accountcode, DebitAmount: debitamount, CreditAmount: creditamount, RowNum: rowNum}},
			})
			continue
		}

		if cfg.RowMode == "per-document-columns" {
			// One row = one document, but instead of a single fixed debit/
			// credit account, the account code is embedded in each amount
			// column's header text (e.g. "D_111110" -> debit 111110,
			// "C_410010" -> credit 410010) — already resolved client-side
			// into cfg.AmountColumns, so this just reads each column's cell
			// for this row. A blank cell means "no line for this account on
			// this document," not an error — only a row where EVERY
			// AmountColumns cell is blank is flagged, since an empty
			// document can't be saved.
			//
			// Known accepted tradeoff: if two rows happen to share the same
			// docno (e.g. a copy-paste mistake), Phase B below merges them
			// into one document exactly like per-document-2-line mode
			// already allows — not specially guarded here, since the same
			// docno-grouping mechanism is a required feature for per-line
			// mode's multi-row documents.
			var lines []rawLine
			for _, ac := range cfg.AmountColumns {
				col := ac.Column
				val := cellNumber(row, &col)
				if val == nil {
					continue
				}
				line := rawLine{AccountCode: ac.AccountCode, RowNum: rowNum}
				// Side is validated to be exactly "debit"/"credit" in
				// validateImportConfig before a job is ever created, so
				// treating "not debit" as "credit" here is safe, not an
				// unvalidated assumption.
				if ac.Side == "debit" {
					line.DebitAmount = *val
				} else {
					line.CreditAmount = *val
				}
				lines = append(lines, line)
			}
			if len(lines) == 0 {
				issues = append(issues, ImportIssue{
					Severity: "error", RowNumbers: []int{rowNum}, Docno: docno, Field: "amountColumns",
					Message: "ไม่ได้ระบุจำนวนเงินในคอลัมน์เดบิตหรือเครดิตใดเลยสำหรับแถวนี้",
				})
			}
			rawLines = append(rawLines, rawLineItem{RowNum: rowNum, Docno: docno, Docdate: docdate, Row: row, Lines: lines})
			continue
		}

		// per-document-2-line — both lines of the pair always carry the same
		// magnitude (one full amount debited, the same amount credited);
		// only the amount-split mode differs in *how* that single magnitude
		// is read off the row.
		debitCode := ""
		if cfg.DebitSource.Mode == "fixed" {
			if cfg.DebitSource.AccountCode != nil {
				debitCode = *cfg.DebitSource.AccountCode
			}
		} else {
			debitCode = cellText(row, cfg.DebitSource.Column)
		}
		creditCode := ""
		if cfg.CreditSource.Mode == "fixed" {
			if cfg.CreditSource.AccountCode != nil {
				creditCode = *cfg.CreditSource.AccountCode
			}
		} else {
			creditCode = cellText(row, cfg.CreditSource.Column)
		}

		var amount float64
		if cfg.AmountSplitMode == "signed-single-column" {
			signed := numberOrZero(cellNumber(row, cfg.SignedAmountColumn))
			if signed >= 0 {
				amount = signed
			} else {
				// Negative amount means the transaction runs the other way
				// (money out) — swap which account sits on debit vs credit
				// rather than trying to encode it as a zero-amount line,
				// since both lines must carry the real magnitude.
				amount = math_Abs(signed)
				debitCode, creditCode = creditCode, debitCode
			}
		} else {
			// Separate debit/credit amount columns: exactly one is expected
			// to be filled per row — read whichever is present. If both are
			// filled, prefer debit and let the per-line "both sides filled"
			// check downstream catch the ambiguity explicitly.
			debitVal := cellNumber(row, cfg.DebitAmountColumn)
			creditVal := cellNumber(row, cfg.CreditAmountColumn)
			if debitVal != nil {
				amount = *debitVal
			} else if creditVal != nil {
				amount = *creditVal
				debitCode, creditCode = creditCode, debitCode
			}
		}

		// Same-account check happens here, per row, rather than after
		// grouping by accountcode — once both sides share a code, they
		// merge into a single journaldetail entry and the "same account
		// both sides" signal is lost.
		if debitCode != "" && debitCode == creditCode {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{rowNum}, Docno: docno, Field: "accountcode", Message: fmt.Sprintf("บัญชีเดบิตและเครดิตเป็นบัญชีเดียวกัน (%s)", debitCode)})
		}

		rawLines = append(rawLines, rawLineItem{
			RowNum: rowNum, Docno: docno, Docdate: docdate, Row: row,
			Lines: []rawLine{
				{AccountCode: debitCode, DebitAmount: amount, CreditAmount: 0, RowNum: rowNum},
				{AccountCode: creditCode, DebitAmount: 0, CreditAmount: amount, RowNum: rowNum},
			},
		})
	}

	// Phase B — group into documents by docno.
	groups := make(map[string]*docGroup)
	groupOrder := make([]string, 0)
	for _, item := range rawLines {
		key := item.Docno
		if key == "" {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{item.RowNum}, Docno: "", Field: "docno", Message: "ไม่ได้ระบุเลขที่เอกสาร"})
			continue
		}
		g, exists := groups[key]
		if !exists {
			g = &docGroup{Docdate: item.Docdate, SourceRow: item.Row}
			groups[key] = g
			groupOrder = append(groupOrder, key)
		}
		g.RowNums = append(g.RowNums, item.RowNum)
		g.SourceRows = append(g.SourceRows, sourceRowEntry{Row: item.Row, RowNum: item.RowNum})
		g.Lines = append(g.Lines, item.Lines...)
		if g.Docdate == "" && item.Docdate != "" {
			g.Docdate = item.Docdate
		}
	}

	// collectRowLevelField mirrors the JS helper exactly: scans every row of
	// a document, collects non-blank values for fieldKey, returns the first
	// one found plus a conflict flag if any other row disagreed.
	collectRowLevelField := func(sourceRows []sourceRowEntry, fieldKey string, numeric bool) collectedField {
		type seenEntry struct {
			text   string
			num    *float64
			rowNum int
		}
		var seen []seenEntry
		colIndex := fm[fieldKey]
		for _, sr := range sourceRows {
			if numeric {
				v := cellNumber(sr.Row, colIndex)
				if v != nil {
					seen = append(seen, seenEntry{num: v, rowNum: sr.RowNum})
				}
			} else {
				v := cellText(sr.Row, colIndex)
				if v != "" {
					seen = append(seen, seenEntry{text: v, rowNum: sr.RowNum})
				}
			}
		}
		if len(seen) == 0 {
			return collectedField{HasValue: false}
		}
		rowNums := make([]int, len(seen))
		hasConflict := false
		if numeric {
			first := *seen[0].num
			for i, s := range seen {
				rowNums[i] = s.rowNum
				if *s.num != first {
					hasConflict = true
				}
			}
			return collectedField{NumberValue: seen[0].num, HasValue: true, HasConflict: hasConflict, RowNums: rowNums}
		}
		first := seen[0].text
		for i, s := range seen {
			rowNums[i] = s.rowNum
			if s.text != first {
				hasConflict = true
			}
		}
		return collectedField{Value: seen[0].text, HasValue: true, HasConflict: hasConflict, RowNums: rowNums}
	}
	// Date fields use the text path (getDateTimeFromDate applied to the raw cell).
	collectRowLevelDateField := func(sourceRows []sourceRowEntry, fieldKey string) collectedField {
		type seenEntry struct {
			value  string
			rowNum int
		}
		var seen []seenEntry
		colIndex := fm[fieldKey]
		for _, sr := range sourceRows {
			raw := cellAt(sr.Row, colIndex)
			v := getDateTimeFromDate(raw)
			if v != "" {
				seen = append(seen, seenEntry{value: v, rowNum: sr.RowNum})
			}
		}
		if len(seen) == 0 {
			return collectedField{HasValue: false}
		}
		rowNums := make([]int, len(seen))
		hasConflict := false
		first := seen[0].value
		for i, s := range seen {
			rowNums[i] = s.rowNum
			if s.value != first {
				hasConflict = true
			}
		}
		return collectedField{Value: seen[0].value, HasValue: true, HasConflict: hasConflict, RowNums: rowNums}
	}

	docs := make([]ImportDocument, 0, len(groupOrder))
	for _, docno := range groupOrder {
		g := groups[docno]
		row := g.SourceRow

		if g.Docdate == "" {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "docdate", Message: "ไม่ได้ระบุวันที่เอกสาร หรือรูปแบบวันที่ไม่ถูกต้อง"})
		}

		bookcode := cellText(row, fm["bookcode"])
		if bookcode == "" {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "bookcode", Message: "ไม่ได้ระบุสมุดรายวัน"})
		} else if _, ok := journalBookMap[bookcode]; !ok {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "bookcode", Message: fmt.Sprintf(`ไม่พบรหัสสมุดรายวัน "%s" ในระบบ`, bookcode)})
		}

		accountgroup := cellText(row, fm["accountgroup"])
		if accountgroup != "" {
			if _, ok := accountGroupMap[accountgroup]; !ok {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: g.RowNums, Docno: docno, Field: "accountgroup", Message: fmt.Sprintf(`ไม่พบรหัสกลุ่มบัญชี "%s" ในระบบ (จะไม่ถูกระบุกลุ่มบัญชี)`, accountgroup)})
			}
		}

		// Debtor/creditor — auto-detected by checking which masterdata list
		// (debtors or creditors) the mapped code actually belongs to,
		// rather than requiring the user to pre-declare a file-wide type.
		debtor := map[string]interface{}{}
		creditor := map[string]interface{}{}
		debtaccounttype := 0
		debtField := collectRowLevelField(g.SourceRows, "debtaccountcode", false)
		if debtField.HasValue && debtField.Value != "" {
			debtCode := debtField.Value
			if debtField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: debtField.RowNums, Docno: docno, Field: "debtaccountcode", Message: fmt.Sprintf(`เอกสารนี้มีรหัสลูกหนี้/เจ้าหนี้ไม่ตรงกันหลายแถว (แถวที่ %s) — รองรับได้ 1 รหัสต่อเอกสาร`, joinInts(debtField.RowNums))})
			}
			foundDebtor, hasDebtor := debtorMap[debtCode]
			foundCreditor, hasCreditor := creditorMap[debtCode]
			switch {
			case hasDebtor && hasCreditor:
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "debtaccountcode", Message: fmt.Sprintf(`รหัส "%s" มีอยู่ทั้งในรายชื่อลูกหนี้และเจ้าหนี้ ระบบไม่สามารถระบุประเภทได้อัตโนมัติ กรุณาแก้ไขรหัสให้ไม่ซ้ำกัน`, debtCode)})
			case hasDebtor:
				debtor = bsonToDisplayMap(foundDebtor)
				debtaccounttype = 0
			case hasCreditor:
				creditor = bsonToDisplayMap(foundCreditor)
				debtaccounttype = 1
			default:
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "debtaccountcode", Message: fmt.Sprintf(`ไม่พบรหัส "%s" ในรายชื่อลูกหนี้หรือเจ้าหนี้`, debtCode)})
			}
		}

		// Per-line checks + build journaldetail, grouped/summed by accountcode.
		byAccountOrder := make([]string, 0)
		byAccount := make(map[string]*ImportJournalDetail)
		for _, line := range g.Lines {
			if line.AccountCode == "" {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "accountcode", Message: "ไม่ได้ระบุรหัสบัญชีสำหรับแถวนี้"})
				continue
			}
			account, accountFound := chartOfAccountsMap[line.AccountCode]
			if !accountFound {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "accountcode", Message: fmt.Sprintf(`รหัสบัญชี "%s" ไม่มีในระบบ`, line.AccountCode)})
			}
			if line.DebitAmount > 0 && line.CreditAmount > 0 {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "debitamount", Message: "แถวนี้มีทั้งเดบิตและเครดิต กรุณาระบุเพียงด้านเดียว"})
				continue
			}
			if line.DebitAmount == 0 && line.CreditAmount == 0 {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "debitamount", Message: "ไม่ได้ระบุจำนวนเงินเดบิตหรือเครดิต"})
				continue
			}
			existing, ok := byAccount[line.AccountCode]
			if !ok {
				accountname := ""
				if accountFound {
					if n, ok := account["accountname"].(string); ok {
						accountname = n
					}
				}
				existing = &ImportJournalDetail{AccountCode: line.AccountCode, AccountName: accountname}
				byAccount[line.AccountCode] = existing
				byAccountOrder = append(byAccountOrder, line.AccountCode)
			}
			existing.DebitAmount += line.DebitAmount
			existing.CreditAmount += line.CreditAmount
		}

		journaldetail := make([]ImportJournalDetail, 0, len(byAccountOrder))
		var sumDebit, sumCredit float64
		for _, code := range byAccountOrder {
			d := byAccount[code]
			journaldetail = append(journaldetail, *d)
			sumDebit += d.DebitAmount
			sumCredit += d.CreditAmount
		}

		if math_Abs(sumDebit-sumCredit) > 0.01 {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "balance", Message: fmt.Sprintf("ยอดเดบิต (%.2f) ไม่เท่ากับเครดิต (%.2f) ผลต่าง %.2f", sumDebit, sumCredit, math_Abs(sumDebit-sumCredit))})
		}

		accountdescription := cellText(row, fm["accountdescription"])
		journaltype := 0
		if jt, ok := fm["journaltype"]; ok && jt != nil {
			if v, err := strconv.Atoi(cellText(row, jt)); err == nil {
				journaltype = v
			}
		}
		exdocrefno := cellText(row, fm["exdocrefno"])
		// Unlike docdate/vatdate/taxdate, exdocrefdate has no required-if-
		// present check gating it, so an empty result must become nil (not
		// "") before reaching the save payload — the backend's own date
		// parser rejects "" (this pipeline IS that backend now, so this is
		// the fix's source, not a consumer of it).
		var exdocrefdate *string
		if ed, ok := fm["exdocrefdate"]; ok && ed != nil {
			if v := getDateTimeFromDate(cellAt(row, ed)); v != "" {
				exdocrefdate = &v
			}
		}

		accountperiod, accountyear := 0, 0
		if g.Docdate != "" {
			if t, err := time.Parse("2006-01-02T15:04:05.000Z", g.Docdate); err == nil {
				accountperiod = int(t.Month())
				accountyear = t.Year() + 543
			}
		}

		// VAT block — only built/validated if vatdocno is non-empty
		// somewhere in this document.
		vatdocnoField := collectRowLevelField(g.SourceRows, "vatdocno", false)
		vats := []ImportVat{} // never nil — marshals to [] not null, matching the old client-side default
		if vatdocnoField.HasValue && vatdocnoField.Value != "" {
			vatdocno := vatdocnoField.Value
			if vatdocnoField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: vatdocnoField.RowNums, Docno: docno, Field: "vatdocno", Message: fmt.Sprintf(`เอกสารนี้มีเลขที่ใบกำกับภาษีไม่ตรงกันหลายแถว (แถวที่ %s) — รองรับใบกำกับภาษีได้ 1 ใบต่อเอกสาร`, joinInts(vatdocnoField.RowNums))})
			}
			vatdateField := collectRowLevelDateField(g.SourceRows, "vatdate")
			vatbaseField := collectRowLevelField(g.SourceRows, "vatbase", true)
			vatrateField := collectRowLevelField(g.SourceRows, "vatrate", true)
			vatamountField := collectRowLevelField(g.SourceRows, "vatamount", true)

			if !vatdateField.HasValue {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "vatdate", Message: "ไม่ได้ระบุวันที่ใบกำกับภาษี"})
			}
			if !vatbaseField.HasValue {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "vatbase", Message: "ไม่ได้ระบุฐานภาษี"})
			}
			if !vatrateField.HasValue {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "vatrate", Message: "ไม่ได้ระบุอัตราภาษี"})
			}
			if !vatamountField.HasValue {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "vatamount", Message: "ไม่ได้ระบุยอดภาษี"})
			}
			if vatdateField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: vatdateField.RowNums, Docno: docno, Field: "vatdate", Message: fmt.Sprintf("วันที่ใบกำกับภาษีไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", vatdateField.RowNums[0])})
			}
			if vatbaseField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: vatbaseField.RowNums, Docno: docno, Field: "vatbase", Message: fmt.Sprintf("ฐานภาษีไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", vatbaseField.RowNums[0])})
			}
			if vatrateField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: vatrateField.RowNums, Docno: docno, Field: "vatrate", Message: fmt.Sprintf("อัตราภาษีไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", vatrateField.RowNums[0])})
			}
			if vatamountField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: vatamountField.RowNums, Docno: docno, Field: "vatamount", Message: fmt.Sprintf("ยอดภาษีไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", vatamountField.RowNums[0])})
			}

			vatdate := vatdateField.Value
			vatperiod, vatyear := 0, 0
			if vatdate != "" {
				if t, err := time.Parse("2006-01-02T15:04:05.000Z", vatdate); err == nil {
					vatperiod = int(t.Month())
					vatyear = t.Year() + 543
				}
			}
			branchcodeField := collectRowLevelField(g.SourceRows, "branchcode", false)
			branchcode := branchcodeField.Value
			if branchcode == "" {
				branchcode = "00000"
			}
			vatsubmitField := collectRowLevelField(g.SourceRows, "vatsubmit", false)
			vatsubmit := vatsubmitField.Value == "1" || strings.EqualFold(vatsubmitField.Value, "true")

			// VatMode/VatType/VatOrganization can each additionally come from
			// a mapped column per row (e.g. a source file that already
			// carries its own VATMODE/VATTYPE/ORGTYPE columns per line) — the
			// mapped column wins when the row has a value, falling back to
			// the wizard-level cfg value (defaulting to 0 when that's also
			// unset — buildParsedDocuments is called directly from tests
			// without going through validateImportConfig's non-nil gate, same
			// defensive posture as numOrZeroField for the other optional
			// numeric fields in this func) when that row's cell is
			// blank/unmapped. Expects the same numeric codes the wizard-level
			// SelectButtons use (vatmode 0/1, vattype 0-2 or 0-1 depending on
			// vatmode, vatorganization 0/1), not free text.
			vatModeField := collectRowLevelField(g.SourceRows, "vatmode", true)
			vatTypeField := collectRowLevelField(g.SourceRows, "vattype", true)
			vatOrganizationField := collectRowLevelField(g.SourceRows, "vatorganization", true)
			vatMode := intOrZero(cfg.VatMode)
			if vatModeField.HasValue {
				vatMode = int(numOrZeroField(vatModeField))
			}
			vatType := intOrZero(cfg.VatType)
			if vatTypeField.HasValue {
				vatType = int(numOrZeroField(vatTypeField))
			}
			vatOrganization := intOrZero(cfg.VatOrganization)
			if vatOrganizationField.HasValue {
				vatOrganization = int(numOrZeroField(vatOrganizationField))
			}
			if vatModeField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: vatModeField.RowNums, Docno: docno, Field: "vatmode", Message: fmt.Sprintf("ภาษีซื้อ/ภาษีขายไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", vatModeField.RowNums[0])})
			}
			if vatTypeField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: vatTypeField.RowNums, Docno: docno, Field: "vattype", Message: fmt.Sprintf("ประเภทภาษีไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", vatTypeField.RowNums[0])})
			}
			if vatOrganizationField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: vatOrganizationField.RowNums, Docno: docno, Field: "vatorganization", Message: fmt.Sprintf("สำนักงานใหญ่/สาขาไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", vatOrganizationField.RowNums[0])})
			}
			// A per-row vatmode/vattype/vatorganization value that falls
			// outside the same range the wizard-level SelectButton enforces
			// (e.g. a stray "2" in a VATMODE column that should only ever be
			// 0/1) is exactly the silent-misclassification failure mode this
			// whole classification requirement exists to prevent — surface it
			// as a blocking error rather than saving a document into the
			// wrong VAT report bucket. Falls back to g.RowNums (the whole
			// document) when the bad value came from the wizard-level config
			// rather than a specific mapped-column cell, since
			// vatModeField/vatTypeField/vatOrganizationField.RowNums is empty
			// in that case.
			vatModeRowNums, vatTypeRowNums, vatOrganizationRowNums := vatModeField.RowNums, vatTypeField.RowNums, vatOrganizationField.RowNums
			if len(vatModeRowNums) == 0 {
				vatModeRowNums = g.RowNums
			}
			if len(vatTypeRowNums) == 0 {
				vatTypeRowNums = g.RowNums
			}
			if len(vatOrganizationRowNums) == 0 {
				vatOrganizationRowNums = g.RowNums
			}
			if vatMode != 0 && vatMode != 1 {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: vatModeRowNums, Docno: docno, Field: "vatmode", Message: fmt.Sprintf("ภาษีซื้อ/ภาษีขายต้องเป็น 0 หรือ 1 แต่พบ %d", vatMode)})
			} else if vatMode == 0 && (vatType < 0 || vatType > 2) {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: vatTypeRowNums, Docno: docno, Field: "vattype", Message: fmt.Sprintf("ประเภทภาษีต้องเป็น 0-2 เมื่อเป็นภาษีซื้อ แต่พบ %d", vatType)})
			} else if vatMode == 1 && (vatType < 0 || vatType > 1) {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: vatTypeRowNums, Docno: docno, Field: "vattype", Message: fmt.Sprintf("ประเภทภาษีต้องเป็น 0-1 เมื่อเป็นภาษีขาย แต่พบ %d", vatType)})
			}
			if vatOrganization != 0 && vatOrganization != 1 {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: vatOrganizationRowNums, Docno: docno, Field: "vatorganization", Message: fmt.Sprintf("สำนักงานใหญ่/สาขาต้องเป็น 0 หรือ 1 แต่พบ %d", vatOrganization)})
			}

			vats = []ImportVat{{
				VatDocNo: vatdocno, VatType: vatType, VatDate: vatdate, VatPeriod: vatperiod, VatYear: vatyear,
				VatBase: numOrZeroField(vatbaseField), VatRate: numOrZeroField(vatrateField), VatAmount: numOrZeroField(vatamountField),
				ExceptVat: int(numOrZeroField(collectRowLevelField(g.SourceRows, "exceptvat", true))), VatMode: vatMode, VatSubmit: vatsubmit, CustCode: "",
				CustName:     collectRowLevelField(g.SourceRows, "custname", false).Value,
				CustTaxID:    collectRowLevelField(g.SourceRows, "custtaxid", false).Value,
				Organization: vatOrganization, BranchCode: branchcode, Remark: "",
			}}
		}

		// WHT block — same all-rows scan + conflict detection as VAT above.
		taxdocnoField := collectRowLevelField(g.SourceRows, "taxdocno", false)
		taxes := []ImportTax{} // never nil — marshals to [] not null, matching the old client-side default
		if taxdocnoField.HasValue && taxdocnoField.Value != "" {
			taxdocno := taxdocnoField.Value
			if taxdocnoField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: taxdocnoField.RowNums, Docno: docno, Field: "taxdocno", Message: fmt.Sprintf(`เอกสารนี้มีเลขที่หนังสือรับรองหัก ณ ที่จ่ายไม่ตรงกันหลายแถว (แถวที่ %s) — รองรับได้ 1 ใบต่อเอกสาร`, joinInts(taxdocnoField.RowNums))})
			}
			taxdateField := collectRowLevelDateField(g.SourceRows, "taxdate")
			whtdescField := collectRowLevelField(g.SourceRows, "whtdesc", false)
			whtbaseField := collectRowLevelField(g.SourceRows, "whtbase", true)
			whtrateField := collectRowLevelField(g.SourceRows, "whtrate", true)
			whtamountField := collectRowLevelField(g.SourceRows, "whtamount", true)

			if taxdateField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: taxdateField.RowNums, Docno: docno, Field: "taxdate", Message: fmt.Sprintf("วันที่หัก ณ ที่จ่ายไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", taxdateField.RowNums[0])})
			}
			if whtdescField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: whtdescField.RowNums, Docno: docno, Field: "whtdesc", Message: fmt.Sprintf("ประเภทเงินได้ไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", whtdescField.RowNums[0])})
			}
			if whtbaseField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: whtbaseField.RowNums, Docno: docno, Field: "whtbase", Message: fmt.Sprintf("ฐานภาษีหัก ณ ที่จ่ายไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", whtbaseField.RowNums[0])})
			}
			if whtrateField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: whtrateField.RowNums, Docno: docno, Field: "whtrate", Message: fmt.Sprintf("อัตราหัก ณ ที่จ่ายไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", whtrateField.RowNums[0])})
			}
			if whtamountField.HasConflict {
				issues = append(issues, ImportIssue{Severity: "warning", RowNumbers: whtamountField.RowNums, Docno: docno, Field: "whtamount", Message: fmt.Sprintf("ยอดหัก ณ ที่จ่ายไม่ตรงกันหลายแถว จะใช้ค่าจากแถวที่ %d", whtamountField.RowNums[0])})
			}
			if !taxdateField.HasValue {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "taxdate", Message: "ไม่ได้ระบุวันที่หัก ณ ที่จ่าย"})
			}
			if !whtdescField.HasValue || whtdescField.Value == "" {
				issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "whtdesc", Message: "ไม่ได้ระบุรายละเอียดหัก ณ ที่จ่าย"})
			}

			whtamount := numOrZeroField(whtamountField)
			// WhtCustType/WhtTaxType/WhtConditionType are wizard-level (constant
			// for the whole batch), not per-row — see the VAT block's comment
			// above for why intOrZero (not a raw deref) is used here too.
			taxes = []ImportTax{{
				TaxDocNo: taxdocno, TaxDate: taxdateField.Value,
				CustName:  collectRowLevelField(g.SourceRows, "custname", false).Value,
				CustType:  intOrZero(cfg.WhtCustType),
				CustTaxID: collectRowLevelField(g.SourceRows, "custtaxid", false).Value,
				TaxType:   intOrZero(cfg.WhtTaxType), ConditionTaxType: intOrZero(cfg.WhtConditionType), Address: collectRowLevelField(g.SourceRows, "address", false).Value,
				TaxAmount: whtamount,
				Details: []ImportWhtDetail{{
					Description: whtdescField.Value, TaxBase: numOrZeroField(whtbaseField), TaxRate: numOrZeroField(whtrateField), TaxAmount: whtamount,
				}},
			}}
		}

		finalAccountGroup := ""
		if _, ok := accountGroupMap[accountgroup]; ok {
			finalAccountGroup = accountgroup
		}

		docs = append(docs, ImportDocument{
			Docno: docno, Docdate: g.Docdate, Bookcode: bookcode, Accountgroup: finalAccountGroup,
			Accountdescription: accountdescription, Accountperiod: accountperiod, Accountyear: accountyear,
			Journaltype: journaltype, Amount: sumDebit, BatchID: "",
			Exdocrefno: exdocrefno, Exdocrefdate: exdocrefdate,
			Journaldetail: journaldetail, Parid: "0000000",
			Vats: vats, Taxes: taxes,
			Debtaccounttype: debtaccounttype, Debtor: debtor, Creditor: creditor,
			RowNumbers: g.RowNums,
		})
	}

	job.UpdateProgress(70, "building_documents", fmt.Sprintf("ตรวจสอบข้อมูลเสร็จสิ้น (%d เอกสาร)", len(docs)))

	return docs, issues
}

func numOrZeroField(f collectedField) float64 {
	if f.NumberValue == nil {
		return 0
	}
	return *f.NumberValue
}

// intOrZero dereferences an optional *int config field, defaulting to 0 when
// nil — used for the wizard-level VAT/WHT classification fields, which the
// HTTP handler path always has validated/required (non-nil) by
// validateImportConfig before buildParsedDocuments runs, but this function
// is also called directly (e.g. from tests) without going through that gate.
func intOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func joinInts(nums []int) string {
	strs := make([]string, len(nums))
	for i, n := range nums {
		strs[i] = strconv.Itoa(n)
	}
	return strings.Join(strs, ", ")
}

func math_Abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// bsonToDisplayMap converts a bson.M masterdata record into the
// map[string]interface{} shape sent as debtor/creditor in the save payload,
// adding displayLabel exactly like the JS side does ("${code} ~ ${thName}").
func bsonToDisplayMap(record bson.M) map[string]interface{} {
	result := make(map[string]interface{}, len(record)+1)
	for k, v := range record {
		if k == "_id" {
			continue
		}
		result[k] = v
	}
	code, _ := record["code"].(string)
	thName := debtAccountThNameFromBson(record)
	result["displayLabel"] = fmt.Sprintf("%s ~ %s", code, thName)
	return result
}

// debtAccountThNameFromBson mirrors debtAccountThName() in the JS source:
// record.names?.find(n => n.code === 'th')?.name || record.name1 || ”
func debtAccountThNameFromBson(record bson.M) string {
	if names, ok := record["names"].(bson.A); ok {
		for _, n := range names {
			if nm, ok := n.(bson.M); ok {
				if code, _ := nm["code"].(string); code == "th" {
					if name, ok := nm["name"].(string); ok {
						return name
					}
				}
			}
		}
	}
	if name1, ok := record["name1"].(string); ok {
		return name1
	}
	return ""
}
