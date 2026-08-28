// import_validation_sml.go - SML ERP fixed-format sales-journal import pipeline.
//
// A dedicated sibling pipeline to import_validation.go's generic
// column-mapping importer. This one parses two fixed-format exports from
// SML ERP: "รายงานข้อมูลรายวัน" (required, a block-structured daily journal
// report) and "รายงานภาษีขาย" (optional, a flat VAT sales report joined in
// by document number). Unlike the generic importer, there is no column
// mapping to configure — the file layout is a fixed contract with SML ERP —
// so parsing is a hand-written state machine over known column positions,
// not user-driven field mapping.
//
// Reuses the generic importer's output types (ImportDocument, ImportIssue,
// ImportVat, ImportTax, ImportJournalDetail) verbatim so the frontend's save
// step (POST /gl/journal/bulk) needs zero changes, and reuses its low-level
// helpers (buildStringKeyedMap, checkDocnosExistChunked, math_Abs, joinInts)
// from import_validation.go since both files share the same package.
//
// Known limitation, same as import_validation.go: the /billscan routes have
// no authentication.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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

// SmlImportConfig carries the one piece of shop-specific configuration this
// pipeline needs. Every shop numbers its chart of accounts differently, so
// the VAT-output-tax account code cannot be hardcoded or auto-derived —
// checked empirically against a real chartofaccounts collection (663
// accounts, no field anywhere marks "this is the VAT account") — it must be
// supplied explicitly by the user, once per shop, from the wizard's Step 1.
type SmlImportConfig struct {
	VatOutputAccountCode string `json:"vatOutputAccountCode"`
}

// ---------- Internal parse types ----------

type smlDailyDocHeader struct {
	Docno, DocdateRaw, BookcodeRaw, Description string
	FileAmount                                  float64
	RowNum                                      int
}

type smlDailyDetailLine struct {
	AccountCode   string
	Debit, Credit float64
	RowNum        int
}

type smlDailyDocGroup struct {
	Header  smlDailyDocHeader
	Lines   []smlDailyDetailLine
	RowNums []int
}

type smlVatRecord struct {
	Docno, VatDocNo, VatDateRaw, CustName string
	VatBase, VatAmount                    float64
	RowNum                                int
}

// ---------- Handler ----------

// SubmitSmlSalesImportValidationHandler accepts SML ERP's fixed-format sales
// journal export (required) and VAT sales report (optional), validates and
// joins them into ImportDocument-shaped records, and runs the same
// async-job pattern as SubmitImportValidationHandler.
func SubmitSmlSalesImportValidationHandler(c *gin.Context) {
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
	var cfg SmlImportConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		c.JSON(400, gin.H{"error": "invalid config JSON", "details": err.Error()})
		return
	}
	if strings.TrimSpace(cfg.VatOutputAccountCode) == "" {
		c.JSON(400, gin.H{"error": "invalid config", "message": "vatOutputAccountCode is required"})
		return
	}

	dailyFile, dailyHeader, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "file is required", "details": err.Error()})
		return
	}
	defer dailyFile.Close()
	if msg := validateSmlUploadedFile(dailyHeader); msg != "" {
		c.JSON(400, gin.H{"error": "invalid file", "message": msg})
		return
	}

	var vatFile multipart.File
	var vh *multipart.FileHeader
	vf, vfh, vatErr := c.Request.FormFile("filevat")
	hasVatFile := vatErr == nil
	if hasVatFile {
		vatFile = vf
		vh = vfh
		defer vatFile.Close()
		if msg := validateSmlUploadedFile(vh); msg != "" {
			c.JSON(400, gin.H{"error": "invalid file", "message": "ไฟล์รายงานภาษีขาย: " + msg})
			return
		}
	} else if vatErr != http.ErrMissingFile {
		c.JSON(400, gin.H{"error": "invalid filevat", "details": vatErr.Error()})
		return
	}

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
	vatAccountFound := false
	for _, acc := range masterCache.Accounts {
		if code, _ := acc["accountcode"].(string); code == cfg.VatOutputAccountCode {
			vatAccountFound = true
			break
		}
	}
	if !vatAccountFound {
		c.JSON(400, gin.H{
			"error":   "vat_account_not_found",
			"message": fmt.Sprintf(`ไม่พบรหัสบัญชี "%s" ในผังบัญชีของร้านค้านี้ กรุณาเลือกรหัสบัญชีภาษีขายให้ถูกต้อง`, cfg.VatOutputAccountCode),
		})
		return
	}

	dailyPath, err := saveSmlUploadToTemp(dailyFile, dailyHeader.Filename)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to save uploaded file", "details": err.Error()})
		return
	}

	var vatPath string
	if hasVatFile {
		vatPath, err = saveSmlUploadToTemp(vatFile, vh.Filename)
		if err != nil {
			os.Remove(dailyPath)
			c.JSON(500, gin.H{"error": "failed to save uploaded file", "details": err.Error()})
			return
		}
	}

	reqCtx := common.NewRequestContext(shopID)
	job, token, err := jobs.Create(reqCtx)
	if err != nil {
		os.Remove(dailyPath)
		if vatPath != "" {
			os.Remove(vatPath)
		}
		c.JSON(503, gin.H{
			"error":   "too_many_active_jobs",
			"message": "ระบบกำลังประมวลผลงานจำนวนมาก กรุณาลองใหม่อีกครั้งในอีกสักครู่",
		})
		return
	}

	go runSmlSalesImportPipeline(job, shopID, dailyPath, vatPath, cfg, masterCache)

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

func validateSmlUploadedFile(header *multipart.FileHeader) string {
	contentType := header.Header.Get("Content-Type")
	validTypes := map[string]bool{
		"application/vnd.ms-excel": true,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
	}
	lowerName := strings.ToLower(header.Filename)
	isValidExt := strings.HasSuffix(lowerName, ".xls") || strings.HasSuffix(lowerName, ".xlsx")
	if !validTypes[contentType] && !isValidExt {
		return "กรุณาเลือกไฟล์ Excel (.xls หรือ .xlsx)"
	}
	maxBytes := int64(configs.EXCEL_IMPORT_MAX_FILE_SIZE_MB) * 1024 * 1024
	if header.Size > maxBytes {
		return fmt.Sprintf("ไฟล์มีขนาดใหญ่เกินไป (สูงสุด %d MB)", configs.EXCEL_IMPORT_MAX_FILE_SIZE_MB)
	}
	return ""
}

func saveSmlUploadToTemp(file io.Reader, filename string) (string, error) {
	tempFilename := fmt.Sprintf("%s%s", uuid.New().String(), filepath.Ext(filename))
	tempFilePath := filepath.Join(configs.UPLOAD_DIR, tempFilename)
	out, err := os.Create(tempFilePath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		os.Remove(tempFilePath)
		return "", err
	}
	out.Close()
	return tempFilePath, nil
}

// ---------- Pipeline ----------

func runSmlSalesImportPipeline(job *jobs.Job, shopID, dailyPath, vatPath string, cfg SmlImportConfig, masterCache *storage.MasterDataCache) {
	defer os.Remove(dailyPath)
	if vatPath != "" {
		defer os.Remove(vatPath)
	}

	deadline := time.Now().Add(time.Duration(configs.EXCEL_IMPORT_TIMEOUT_SEC) * time.Second)

	job.UpdateProgress(2, "parsing_daily", "กำลังอ่านไฟล์รายงานข้อมูลรายวัน")

	dailyRows, err := readAllSmlRows(dailyPath, "")
	if err != nil {
		job.Fail("file_parse_failed", fmt.Sprintf("ไม่สามารถอ่านไฟล์รายงานข้อมูลรายวันได้: %v", err))
		return
	}

	dailyParse := parseSmlDailyBlocks(dailyRows, deadline)
	if dailyParse.TimedOut {
		job.Fail("PROCESSING_TIMEOUT", "การประมวลผลใช้เวลานานเกินกำหนด กรุณาลองใหม่กับไฟล์ที่มีจำนวนแถวน้อยลง")
		return
	}
	docGroups, structIssues := dailyParse.Groups, dailyParse.Issues

	var vatByDocno map[string]smlVatRecord
	if vatPath != "" {
		job.UpdateProgress(30, "parsing_vat", "กำลังอ่านไฟล์รายงานภาษีขาย")
		vatRows, err := readAllSmlRows(vatPath, "ExportExcel")
		if err != nil {
			job.Fail("file_parse_failed", fmt.Sprintf("ไม่สามารถอ่านไฟล์รายงานภาษีขายได้: %v", err))
			return
		}
		vatRecords := parseSmlVatReport(vatRows)
		vatByDocno = make(map[string]smlVatRecord, len(vatRecords))
		for _, r := range vatRecords {
			vatByDocno[r.Docno] = r
		}
	} else {
		job.UpdateProgress(30, "parsing_vat", "ไม่มีไฟล์รายงานภาษีขาย ข้ามขั้นตอนนี้")
	}

	job.UpdateProgress(50, "loading_masterdata", "กำลังเตรียมข้อมูลผังบัญชี")

	chartOfAccountsMap := buildStringKeyedMap(masterCache.Accounts, "accountcode")
	journalBookCodeMap := buildStringKeyedMap(masterCache.JournalBooks, "code")
	journalBookNameMap := buildStringKeyedMap(masterCache.JournalBooks, "name1")

	job.UpdateProgress(55, "joining", "กำลังรวมข้อมูลภาษีขาย")

	docs := make([]ImportDocument, 0, len(docGroups))
	issues := append([]ImportIssue{}, structIssues...)

	// Wrong-company sanity check: compare the file's own row-1 company name
	// against the shop's registered name. Non-blocking — surfaced as a
	// warning so the frontend can render a prominent banner rather than bury
	// it in the per-document issue list.
	if masterCache.ShopProfile != nil {
		fileCompanyName := dailyParse.CompanyName
		shopName := masterCache.ShopProfile.GetCompanyName()
		if fileCompanyName != "" && shopName != "" && !strings.Contains(shopName, fileCompanyName) && !strings.Contains(fileCompanyName, shopName) {
			issues = append(issues, ImportIssue{
				Severity: "warning",
				Field:    "company",
				Message:  fmt.Sprintf(`ชื่อบริษัทในไฟล์ ("%s") ไม่ตรงกับชื่อร้านค้าปัจจุบัน ("%s") — โปรดตรวจสอบว่าเลือกไฟล์ถูกต้อง`, fileCompanyName, shopName),
			})
		}
	}

	total := len(docGroups)
	for idx, g := range docGroups {
		if idx%500 == 0 && time.Now().After(deadline) {
			job.Fail("PROCESSING_TIMEOUT", "การประมวลผลใช้เวลานานเกินกำหนด กรุณาลองใหม่กับไฟล์ที่มีจำนวนแถวน้อยลง")
			return
		}
		if idx%50 == 0 {
			pct := 60 + int(float64(idx)/float64(total)*25)
			job.UpdateProgress(pct, "building_documents", fmt.Sprintf("กำลังตรวจสอบข้อมูล (%d/%d เอกสาร)", idx, total))
		}

		var vatRec *smlVatRecord
		if vatByDocno != nil {
			if r, ok := vatByDocno[g.Header.Docno]; ok {
				vatRec = &r
			}
		}

		doc, docIssues := buildSmlSalesDocument(g, cfg.VatOutputAccountCode, vatRec, chartOfAccountsMap, journalBookCodeMap, journalBookNameMap)
		docs = append(docs, doc)
		issues = append(issues, docIssues...)
	}

	job.UpdateProgress(90, "checking_duplicates", "กำลังตรวจสอบเลขที่เอกสารซ้ำ")

	docnos := make([]string, 0, len(docs))
	for _, d := range docs {
		docnos = append(docnos, d.Docno)
	}
	existing, err := checkDocnosExistChunked(shopID, docnos)
	if err != nil {
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

// ---------- File 1 parsing: block state machine ----------

// smlDailyParseResult bundles parseSmlDailyBlocks's return values, including
// the file's own row-1 company name (used for the wrong-company sanity
// check) — returned rather than stashed in a package-level variable, since
// multiple import jobs (different users/shops) can run their pipeline
// goroutines concurrently and a shared var would be a data race.
type smlDailyParseResult struct {
	Groups      []smlDailyDocGroup
	Issues      []ImportIssue
	TimedOut    bool
	CompanyName string
}

// parseSmlDailyBlocks runs the row-classification state machine over the
// daily journal report's raw rows (0-indexed) and returns the parsed
// document groups plus any structural issues. Row roles are distinguished
// purely by column contents — see the plan's "File structures" section for
// the exact rules empirically verified against a real sample file.
func parseSmlDailyBlocks(rows [][]string, deadline time.Time) smlDailyParseResult {
	var groups []smlDailyDocGroup
	var issues []ImportIssue
	var companyName string

	if len(rows) > 0 {
		companyName = strings.TrimSpace(smlCell(rows[0], 0))
	}

	var current *smlDailyDocGroup
	for i, row := range rows {
		if i < 5 {
			continue // company/title/print-date/two header-caption rows
		}
		if i%500 == 0 && time.Now().After(deadline) {
			return smlDailyParseResult{TimedOut: true}
		}
		if !rowHasAnyValue(row) {
			continue
		}

		rowNum := i + 1
		colA := strings.TrimSpace(smlCell(row, 0))
		colC := strings.TrimSpace(smlCell(row, 2))

		switch {
		case strings.Contains(colC, "ยอดรวมทั้งสิ้น"):
			// grand-total sentinel — end of data
			current = nil
			continue
		case strings.Contains(colC, "ยอดรวมวันที่"):
			// subtotal row — skip
			current = nil
			continue
		case colC != "" && strings.Contains(colC, "/"):
			// document-header row
			amt, _ := strconv.ParseFloat(strings.TrimSpace(smlCell(row, 3)), 64)
			hdr := smlDailyDocHeader{
				DocdateRaw:  colA,
				Docno:       strings.TrimSpace(smlCell(row, 1)),
				BookcodeRaw: colC,
				FileAmount:  amt,
				Description: strings.TrimSpace(smlCell(row, 4)),
				RowNum:      rowNum,
			}
			groups = append(groups, smlDailyDocGroup{Header: hdr, RowNums: []int{rowNum}})
			current = &groups[len(groups)-1]
		case colA != "":
			// detail row
			if current == nil {
				issues = append(issues, ImportIssue{
					Severity:   "error",
					RowNumbers: []int{rowNum},
					Field:      "structure",
					Message:    fmt.Sprintf("พบแถวรายละเอียดที่แถวที่ %d โดยไม่มีแถวเอกสารนำหน้า", rowNum),
				})
				continue
			}
			debit, _ := strconv.ParseFloat(strings.TrimSpace(smlCell(row, 3)), 64)
			credit, _ := strconv.ParseFloat(strings.TrimSpace(smlCell(row, 4)), 64)
			current.Lines = append(current.Lines, smlDailyDetailLine{
				AccountCode: colA,
				Debit:       debit,
				Credit:      credit,
				RowNum:      rowNum,
			})
			current.RowNums = append(current.RowNums, rowNum)
		default:
			issues = append(issues, ImportIssue{
				Severity:   "error",
				RowNumbers: []int{rowNum},
				Field:      "structure",
				Message:    fmt.Sprintf("ไม่สามารถจำแนกประเภทแถวที่ %d ได้", rowNum),
			})
		}
	}

	return smlDailyParseResult{Groups: groups, Issues: issues, CompanyName: companyName}
}

// ---------- File 2 parsing: flat VAT report ----------

func parseSmlVatReport(rows [][]string) []smlVatRecord {
	var records []smlVatRecord
	for i, row := range rows {
		if i < 6 {
			continue // title/company/address rows + header row
		}
		if !rowHasAnyValue(row) {
			continue
		}
		docno := strings.TrimSpace(smlCell(row, 3))
		lamdap := strings.TrimSpace(smlCell(row, 0))
		if lamdap == "" && docno == "" {
			continue // grand-total row
		}
		vatbase, _ := strconv.ParseFloat(strings.TrimSpace(smlCell(row, 6)), 64)
		vatamount, _ := strconv.ParseFloat(strings.TrimSpace(smlCell(row, 7)), 64)
		records = append(records, smlVatRecord{
			Docno:      docno,
			VatDocNo:   strings.TrimSpace(smlCell(row, 2)),
			VatDateRaw: strings.TrimSpace(smlCell(row, 1)),
			CustName:   strings.TrimSpace(smlCell(row, 5)),
			VatBase:    vatbase,
			VatAmount:  vatamount,
			RowNum:     i + 1,
		})
	}
	return records
}

// ---------- Document building ----------

// buildSmlSalesDocument validates one parsed document group and builds its
// ImportDocument. Debtor/creditor are always left empty for this file
// type — the source file carries no code, only a customer name embedded in
// free text, and fuzzy name-matching to a debtor was explicitly ruled out.
func buildSmlSalesDocument(
	g smlDailyDocGroup,
	vatOutputAccountCode string,
	vatRec *smlVatRecord,
	chartOfAccountsMap, journalBookCodeMap, journalBookNameMap map[string]bson.M,
) (ImportDocument, []ImportIssue) {
	var issues []ImportIssue
	docno := g.Header.Docno

	docdate, ok := parseThaiBEDate(g.Header.DocdateRaw)
	if !ok {
		issues = append(issues, ImportIssue{
			Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "docdate",
			Message: fmt.Sprintf(`ไม่สามารถแปลงวันที่เอกสาร "%s" ได้ (คาดรูปแบบ D/M/YYYY พ.ศ.)`, g.Header.DocdateRaw),
		})
	}

	bookcode, bookIssue := resolveSmlBookcode(g.Header.BookcodeRaw, journalBookCodeMap, journalBookNameMap)
	if bookIssue != "" {
		issues = append(issues, ImportIssue{Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "bookcode", Message: bookIssue})
	}

	journaldetail := make([]ImportJournalDetail, 0, len(g.Lines))
	sumDebit, sumCredit := 0.0, 0.0
	vatLineCount := 0
	vatLineCredit := 0.0
	for _, line := range g.Lines {
		if line.AccountCode == "" {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "accountcode", Message: "ไม่ได้ระบุรหัสบัญชีสำหรับแถวนี้"})
			continue
		}
		account, found := chartOfAccountsMap[line.AccountCode]
		if !found {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "accountcode", Message: fmt.Sprintf(`รหัสบัญชี "%s" ไม่มีในระบบ`, line.AccountCode)})
		}
		if line.Debit > 0 && line.Credit > 0 {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "debitamount", Message: "แถวนี้มีทั้งเดบิตและเครดิต กรุณาระบุเพียงด้านเดียว"})
			continue
		}
		if line.Debit == 0 && line.Credit == 0 {
			issues = append(issues, ImportIssue{Severity: "error", RowNumbers: []int{line.RowNum}, Docno: docno, Field: "debitamount", Message: "ไม่ได้ระบุจำนวนเงินเดบิตหรือเครดิต"})
			continue
		}
		accountname := ""
		if found {
			if n, ok := account["accountname"].(string); ok {
				accountname = n
			}
		}
		journaldetail = append(journaldetail, ImportJournalDetail{
			AccountCode:  line.AccountCode,
			AccountName:  accountname,
			DebitAmount:  line.Debit,
			CreditAmount: line.Credit,
		})
		sumDebit += line.Debit
		sumCredit += line.Credit
		if line.AccountCode == vatOutputAccountCode {
			vatLineCount++
			vatLineCredit += line.Credit
		}
	}

	// A document with zero VAT lines is valid — not every daily-journal entry
	// carries VAT (deposits, non-taxable revenue, etc.), so this only errors
	// when there's genuine ambiguity (more than one line coded as the VAT
	// account), not when VAT is legitimately absent.
	if vatLineCount > 1 {
		issues = append(issues, ImportIssue{
			Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "vatamount",
			Message: fmt.Sprintf(`เอกสารนี้มีรายการภาษีขาย (%s) มากกว่า 1 รายการ`, vatOutputAccountCode),
		})
	}

	if math_Abs(sumDebit-sumCredit) > 0.01 {
		issues = append(issues, ImportIssue{
			Severity: "error", RowNumbers: g.RowNums, Docno: docno, Field: "balance",
			Message: fmt.Sprintf("ยอดเดบิต (%.2f) ไม่เท่ากับเครดิต (%.2f) ผลต่าง %.2f", sumDebit, sumCredit, math_Abs(sumDebit-sumCredit)),
		})
	}

	vats := []ImportVat{}
	if vatLineCount == 0 && vatRec != nil {
		// The VAT report says this docno has VAT, but the journal file itself
		// has no line coded as the VAT account — surfaced as a warning since
		// it signals the two files disagree, without blocking the import (the
		// journal file's own line composition remains authoritative).
		issues = append(issues, ImportIssue{
			Severity: "warning", RowNumbers: g.RowNums, Docno: docno, Field: "vatamount",
			Message: fmt.Sprintf(`เอกสาร "%s" มีข้อมูลในไฟล์รายงานภาษีขาย (ยอดภาษี %.2f) แต่ไม่พบรายการภาษีขาย (%s) ในไฟล์รายงานข้อมูลรายวัน — จะนำเข้าโดยไม่มีข้อมูลภาษี`, docno, vatRec.VatAmount, vatOutputAccountCode),
		})
	}
	if vatLineCount >= 1 {
		var vatEntry ImportVat
		if vatRec != nil {
			vatDate, vatDateOk := parseThaiBEDate(vatRec.VatDateRaw)
			if !vatDateOk {
				vatDate = docdate
			}
			if math_Abs(vatRec.VatAmount-vatLineCredit) > 0.01 {
				issues = append(issues, ImportIssue{
					Severity: "warning", RowNumbers: g.RowNums, Docno: docno, Field: "vatamount",
					Message: fmt.Sprintf(`เอกสาร "%s" ยอดภาษีในไฟล์รายงานภาษีขาย (%.2f) ไม่ตรงกับยอดภาษีในไฟล์รายงานข้อมูลรายวัน (%.2f) — ใช้ค่าจากไฟล์รายงานข้อมูลรายวัน`, docno, vatRec.VatAmount, vatLineCredit),
				})
			}
			vatEntry = ImportVat{
				VatDocNo:   vatRec.VatDocNo,
				VatDate:    vatDate,
				VatBase:    vatRec.VatBase,
				VatAmount:  vatLineCredit, // file 1 (journal) is authoritative per confirmed decision
				CustName:   vatRec.CustName,
				BranchCode: "00000",
			}
		} else {
			vatEntry = ImportVat{
				VatDocNo:   docno,
				VatDate:    docdate,
				VatBase:    sumDebit - vatLineCredit,
				VatAmount:  vatLineCredit,
				CustName:   "",
				BranchCode: "00000",
			}
		}
		if docdate != "" {
			if t, err := time.Parse("2006-01-02T15:04:05.000Z", vatEntry.VatDate); err == nil {
				vatEntry.VatPeriod = int(t.Month())
				vatEntry.VatYear = t.Year() + 543
			}
		}
		vats = append(vats, vatEntry)
	}

	accountperiod, accountyear := 0, 0
	if docdate != "" {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", docdate); err == nil {
			accountperiod = int(t.Month())
			accountyear = t.Year() + 543
		}
	}

	return ImportDocument{
		Docno:              docno,
		Docdate:            docdate,
		Bookcode:           bookcode,
		Accountgroup:       "",
		Accountdescription: g.Header.Description,
		Accountperiod:      accountperiod,
		Accountyear:        accountyear,
		Journaltype:        0,
		Amount:             sumDebit,
		BatchID:            "",
		Exdocrefno:         "",
		Exdocrefdate:       nil,
		Journaldetail:      journaldetail,
		Parid:              "0000000",
		Vats:               vats,
		Taxes:              []ImportTax{},
		Debtaccounttype:    0,
		Debtor:             map[string]interface{}{},
		Creditor:           map[string]interface{}{},
		RowNumbers:         g.RowNums,
	}, issues
}

// resolveSmlBookcode splits "02/สมุดรายวันขาย" on the first "/" and resolves
// the actual journal-book code by trying two strategies in order: (1) the
// text before "/" against journalBooks.code (covers a shop that genuinely
// uses short codes matching what SML exports), (2) if that misses, the text
// after "/" against journalBooks.name1 (verified empirically against one
// real shop's data — SML's own bookcode prefix does not match any real
// shop's `code` field, but the name after the slash matched `name1`
// exactly). Only errors if both miss.
func resolveSmlBookcode(raw string, codeMap, nameMap map[string]bson.M) (string, string) {
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Sprintf(`รูปแบบรหัสสมุดรายวัน "%s" ไม่ถูกต้อง (ต้องมี "/" คั่น)`, raw)
	}
	prefix := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])

	if rec, ok := codeMap[prefix]; ok {
		if code, ok := rec["code"].(string); ok {
			return code, ""
		}
	}
	if rec, ok := nameMap[name]; ok {
		if code, ok := rec["code"].(string); ok {
			return code, ""
		}
	}
	return "", fmt.Sprintf(`ไม่พบสมุดรายวันที่ชื่อ "%s" ในระบบ`, name)
}

// ---------- Shared low-level helpers ----------

// smlCell returns the trimmed cell at colIndex, or "" if out of range —
// unlike cellText/cellAt in import_validation.go, this works on a plain
// positional index (this file format has fixed column positions, no
// user-configured column mapping), so it doesn't take a *int.
func smlCell(row []string, colIndex int) string {
	if colIndex < 0 || colIndex >= len(row) {
		return ""
	}
	return row[colIndex]
}

// parseThaiBEDate parses a Thai Buddhist-era date string ("1/8/2569") into
// the same ISO UTC format getDateTimeFromDate produces elsewhere in this
// package. Tried first (not getDateTimeFromDate, whose Gregorian-only
// layouts would silently misparse a B.E. year as if it were Gregorian);
// getDateTimeFromDate is used only as a fallback for a stray
// non-conforming cell.
func parseThaiBEDate(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parts := strings.Split(raw, "/")
	if len(parts) == 3 {
		d, errD := strconv.Atoi(strings.TrimSpace(parts[0]))
		m, errM := strconv.Atoi(strings.TrimSpace(parts[1]))
		yBE, errY := strconv.Atoi(strings.TrimSpace(parts[2]))
		if errD == nil && errM == nil && errY == nil && m >= 1 && m <= 12 && d >= 1 && d <= 31 && yBE > 2400 {
			t := time.Date(yBE-543, time.Month(m), d, 0, 0, 0, 0, time.UTC)
			return t.Format("2006-01-02T15:04:05.000Z"), true
		}
	}
	if fallback := getDateTimeFromDate(raw); fallback != "" {
		return fallback, true
	}
	return "", false
}

// readAllSmlRows streams a workbook's rows into memory as [][]string,
// enforcing EXCEL_IMPORT_MAX_ROWS. If sheetName is empty, uses the first
// sheet (file 1's sheet name is stable but not load-bearing to select by
// name); if sheetName is non-empty, the sheet MUST match exactly (file 2's
// contract is a fixed sheet name "ExportExcel" — silently falling back to
// sheet 0 would read the wrong data with no clear error).
func readAllSmlRows(path, sheetName string) ([][]string, error) {
	f, err := excelize.OpenFile(path, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	defer f.Close()

	targetSheet := sheetName
	if targetSheet == "" {
		targetSheet = f.GetSheetName(0)
		if targetSheet == "" {
			return nil, fmt.Errorf("ไม่พบข้อมูลในไฟล์")
		}
	} else if idx, err := f.GetSheetIndex(targetSheet); err != nil || idx == -1 {
		return nil, fmt.Errorf(`ไม่พบชีทชื่อ "%s" ในไฟล์`, targetSheet)
	}

	rows, err := f.Rows(targetSheet)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var allRows [][]string
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		allRows = append(allRows, cols)
		if len(allRows) > configs.EXCEL_IMPORT_MAX_ROWS {
			return nil, fmt.Errorf("ไฟล์มีจำนวนแถวมากเกินไป (สูงสุด %d แถว)", configs.EXCEL_IMPORT_MAX_ROWS)
		}
	}
	return allRows, nil
}
