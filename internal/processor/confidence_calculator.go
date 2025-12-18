// confidence_calculator.go - Weighted Confidence Score Calculator
//
// คำนวณคะแนนความน่าเชื่อถือแบบถ่วงน้ำหนักตามปัจจัยต่างๆ
// เพื่อให้ได้ confidence score ที่สะท้อนคุณภาพข้อมูลจริง

package processor

import (
	"math"

	"github.com/bosocmputer/account_ocr_gemini/internal/common"
)

// ConfidenceFactors เก็บคะแนนของแต่ละปัจจัย
type ConfidenceFactors struct {
	TemplateMatch     float64 `json:"template_match"`     // คะแนนจากการจับคู่ template (0-100)
	PartyMatch        float64 `json:"party_match"`        // คะแนนจากการจับคู่คู่ค้า (vendor/debtor) (0-100)
	DataCompleteness  float64 `json:"data_completeness"`  // คะแนนจากความสมบูรณ์ของข้อมูล (0-100)
	FieldValidation   float64 `json:"field_validation"`   // คะแนนจากการ validate ฟิลด์ต่างๆ (0-100)
	BalanceValidation float64 `json:"balance_validation"` // คะแนนจากการตรวจสอบ Debit = Credit (0-100)
}

// ConfidenceWeights น้ำหนักของแต่ละปัจจัย (รวมต้องเท่ากับ 1.0)
type ConfidenceWeights struct {
	TemplateMatch     float64
	PartyMatch        float64
	DataCompleteness  float64
	FieldValidation   float64
	BalanceValidation float64
}

// DefaultWeights น้ำหนักมาตรฐานที่ใช้ในการคำนวณ
var DefaultWeights = ConfidenceWeights{
	TemplateMatch:     0.30, // 30% - Template matching มีความสำคัญมาก
	PartyMatch:        0.25, // 25% - Party matching (vendor/debtor) สำคัญรองลงมา
	DataCompleteness:  0.20, // 20% - ความสมบูรณ์ของข้อมูล
	FieldValidation:   0.15, // 15% - การ validate ฟิลด์ต่างๆ
	BalanceValidation: 0.10, // 10% - การตรวจสอบยอด Debit = Credit
}

// ConfidenceResult ผลลัพธ์การคำนวณ confidence
type ConfidenceResult struct {
	OverallScore   float64           `json:"overall_score"`   // คะแนนรวม (0-100)
	OverallLevel   string            `json:"overall_level"`   // ระดับความน่าเชื่อถือ
	RequiresReview bool              `json:"requires_review"` // ต้องตรวจสอบเพิ่มเติมหรือไม่
	Factors        ConfidenceFactors `json:"factors"`         // รายละเอียดคะแนนแต่ละปัจจัย
	Breakdown      map[string]string `json:"breakdown"`       // คำอธิบายแต่ละปัจจัย
}

// CalculateWeightedConfidence คำนวณ confidence score แบบถ่วงน้ำหนัก
func CalculateWeightedConfidence(
	templateMatchResult *TemplateMatchResult,
	vendorMatchResult *VendorMatchResult,
	accountingEntry map[string]interface{},
	reqCtx *common.RequestContext,
) ConfidenceResult {

	// คำนวณคะแนนแต่ละปัจจัย
	factors := ConfidenceFactors{
		TemplateMatch:     getTemplateConfidenceScore(templateMatchResult),
		PartyMatch:        getPartyConfidenceScore(vendorMatchResult, accountingEntry),
		DataCompleteness:  calculateCompletenessScore(accountingEntry),
		FieldValidation:   calculateFieldValidationScore(accountingEntry),
		BalanceValidation: calculateBalanceScore(accountingEntry),
	}

	// คำนวณคะแนนรวมแบบถ่วงน้ำหนัก
	overallScore := (factors.TemplateMatch * DefaultWeights.TemplateMatch) +
		(factors.PartyMatch * DefaultWeights.PartyMatch) +
		(factors.DataCompleteness * DefaultWeights.DataCompleteness) +
		(factors.FieldValidation * DefaultWeights.FieldValidation) +
		(factors.BalanceValidation * DefaultWeights.BalanceValidation)

	// ปัดเศษเป็นทศนิยม 2 ตำแหน่ง
	overallScore = math.Round(overallScore*100) / 100

	// กำหนดระดับความน่าเชื่อถือ
	level := determineConfidenceLevel(overallScore)

	// กำหนดว่าต้องตรวจสอบเพิ่มเติมหรือไม่
	requiresReview := shouldRequireReview(overallScore, factors, vendorMatchResult)

	// สร้างคำอธิบาย breakdown
	breakdown := generateBreakdown(factors, vendorMatchResult, accountingEntry)

	// Log รายละเอียด
	if reqCtx != nil {
		reqCtx.LogInfo("📊 Confidence Calculation:")
		reqCtx.LogInfo("  ├─ Template Match: %.1f%% (weight: %.0f%%)", factors.TemplateMatch, DefaultWeights.TemplateMatch*100)
		reqCtx.LogInfo("  ├─ Party Match: %.1f%% (weight: %.0f%%)", factors.PartyMatch, DefaultWeights.PartyMatch*100)
		reqCtx.LogInfo("  ├─ Data Completeness: %.1f%% (weight: %.0f%%)", factors.DataCompleteness, DefaultWeights.DataCompleteness*100)
		reqCtx.LogInfo("  ├─ Field Validation: %.1f%% (weight: %.0f%%)", factors.FieldValidation, DefaultWeights.FieldValidation*100)
		reqCtx.LogInfo("  ├─ Balance Validation: %.1f%% (weight: %.0f%%)", factors.BalanceValidation, DefaultWeights.BalanceValidation*100)
		reqCtx.LogInfo("  └─ Overall: %.1f%% (%s) → Review: %v", overallScore, level, requiresReview)
	}

	return ConfidenceResult{
		OverallScore:   overallScore,
		OverallLevel:   level,
		RequiresReview: requiresReview,
		Factors:        factors,
		Breakdown:      breakdown,
	}
}

// getTemplateConfidenceScore คำนวณคะแนนจากการจับคู่ template
func getTemplateConfidenceScore(result *TemplateMatchResult) float64 {
	if result == nil {
		return 0.0
	}

	// ใช้คะแนน confidence จาก AI template matching
	return result.Confidence
}

// getPartyConfidenceScore คำนวณคะแนนจากการจับคู่คู่ค้า (vendor หรือ debtor)
func getPartyConfidenceScore(vendorResult *VendorMatchResult, accountingEntry map[string]interface{}) float64 {
	// ตรวจสอบว่าเป็นเอกสารขาย (มี debtor) หรือ ซื้อ (มี creditor)
	debtorCode := getStringFromInterface(accountingEntry["debtor_code"])
	creditorCode := getStringFromInterface(accountingEntry["creditor_code"])

	// ถ้าเป็นเอกสารขาย (มี debtor)
	if debtorCode != "" && debtorCode != "null" {
		// ใช้คะแนนจาก debtor matching
		// ถ้า debtor_code มีค่า แสดงว่าจับคู่สำเร็จ ให้คะแนน 80
		return 80.0
	}

	// ถ้าเป็นเอกสารซื้อ (มี creditor)
	if creditorCode != "" && creditorCode != "null" {
		// ถ้ามี creditor_code แล้ว แสดงว่าจับคู่สำเร็จ (จาก vendor_pre_matching หรือ AI Phase 3)
		// ใช้คะแนนจาก vendorResult ถ้ามี ไม่งั้นให้คะแนน 80 (matched)
		if vendorResult != nil && vendorResult.Found {
			return vendorResult.Similarity // ใช้คะแนนจาก vendor_pre_matching
		}
		return 80.0 // AI Phase 3 matched successfully
	}

	// ไม่มีทั้ง debtor และ creditor
	return 0.0
}

// getStringFromInterface แปลงค่าจาก interface{} เป็น string
func getStringFromInterface(val interface{}) string {
	if val == nil {
		return ""
	}
	if str, ok := val.(string); ok {
		return str
	}
	return ""
}

// calculateCompletenessScore คำนวณคะแนนจากความสมบูรณ์ของข้อมูล
func calculateCompletenessScore(accountingEntry map[string]interface{}) float64 {
	if accountingEntry == nil {
		return 0.0
	}

	// ฟิลด์หลักที่ต้องมี (ไม่นับ debtor เพราะอาจเป็นผู้ซื้อทั่วไป)
	requiredFields := []string{
		"creditor_code", "creditor_name",
		"document_date", "reference_number",
		"journal_book_code",
	}

	filledCount := 0
	for _, field := range requiredFields {
		value, exists := accountingEntry[field]
		if exists && value != nil && value != "" {
			filledCount++
		}
	}

	// คำนวณเปอร์เซ็นต์ความสมบูรณ์
	score := (float64(filledCount) / float64(len(requiredFields))) * 100
	return math.Round(score*10) / 10
}

// calculateFieldValidationScore คำนวณคะแนนจากการ validate ฟิลด์ต่างๆ
func calculateFieldValidationScore(accountingEntry map[string]interface{}) float64 {
	if accountingEntry == nil {
		return 0.0
	}

	score := 100.0

	// ตรวจสอบ entries มีอยู่หรือไม่
	entriesRaw, exists := accountingEntry["entries"]
	if !exists {
		return 0.0
	}

	entries, ok := entriesRaw.([]interface{})
	if !ok || len(entries) == 0 {
		return 0.0
	}

	// ตรวจสอบแต่ละ entry
	invalidCount := 0
	for _, e := range entries {
		entryMap, ok := e.(map[string]interface{})
		if !ok {
			invalidCount++
			continue
		}

		// ตรวจสอบว่ามี account_code หรือไม่
		accountCode, exists := entryMap["account_code"]
		if !exists || accountCode == nil || accountCode == "" {
			invalidCount++
		}

		// ตรวจสอบว่ามี debit หรือ credit อย่างน้อย 1 อย่าง
		debit := getFloatFromInterface(entryMap["debit"])
		credit := getFloatFromInterface(entryMap["credit"])
		if debit == 0 && credit == 0 {
			invalidCount++
		}
	}

	// ลดคะแนนตามจำนวน entry ที่ผิดพลาด
	if invalidCount > 0 {
		penalty := float64(invalidCount) * 10.0
		score -= penalty
	}

	if score < 0 {
		score = 0
	}

	return math.Round(score*10) / 10
}

// calculateBalanceScore คำนวณคะแนนจากการตรวจสอบ Debit = Credit
func calculateBalanceScore(accountingEntry map[string]interface{}) float64 {
	if accountingEntry == nil {
		return 0.0
	}

	balanceCheck, exists := accountingEntry["balance_check"]
	if !exists {
		return 50.0 // ไม่มีข้อมูล balance_check ให้คะแนนกลางๆ
	}

	balanceMap, ok := balanceCheck.(map[string]interface{})
	if !ok {
		return 50.0
	}

	balanced, exists := balanceMap["balanced"]
	if !exists {
		return 50.0
	}

	balancedBool, ok := balanced.(bool)
	if !ok {
		return 50.0
	}

	if balancedBool {
		return 100.0 // Debit = Credit → 100 คะแนน
	}

	// ถ้าไม่ balance ให้คะแนนต่ำ
	return 20.0
}

// determineConfidenceLevel กำหนดระดับความน่าเชื่อถือตามคะแนน
func determineConfidenceLevel(score float64) string {
	if score >= 95 {
		return "very_high" // 95-100
	} else if score >= 85 {
		return "high" // 85-94
	} else if score >= 70 {
		return "medium" // 70-84
	} else if score >= 50 {
		return "low" // 50-69
	} else {
		return "very_low" // 0-49
	}
}

// shouldRequireReview ตัดสินใจว่าต้องตรวจสอบเพิ่มเติมหรือไม่
func shouldRequireReview(
	overallScore float64,
	factors ConfidenceFactors,
	vendorMatchResult *VendorMatchResult,
) bool {

	// เงื่อนไขที่ต้องตรวจสอบเพิ่มเติม:

	// 1. Overall score ต่ำกว่า 85
	if overallScore < 85 {
		return true
	}

	// 2. Vendor ไม่เจอในระบบ
	if vendorMatchResult != nil && !vendorMatchResult.Found {
		return true
	}

	// 3. Data completeness ต่ำกว่า 80%
	if factors.DataCompleteness < 80 {
		return true
	}

	// 4. Balance ไม่ balance (คะแนนต่ำกว่า 90)
	if factors.BalanceValidation < 90 {
		return true
	}

	return false
}

// generateBreakdown สร้างคำอธิบาย breakdown ของแต่ละปัจจัย
func generateBreakdown(
	factors ConfidenceFactors,
	vendorMatchResult *VendorMatchResult,
	accountingEntry map[string]interface{},
) map[string]string {

	breakdown := make(map[string]string)

	// Template Match
	if factors.TemplateMatch >= 85 {
		breakdown["template_match"] = "Template match สำเร็จ (คะแนนสูง)"
	} else if factors.TemplateMatch > 0 {
		breakdown["template_match"] = "Template match ไม่แน่นอน (คะแนนปานกลาง)"
	} else {
		breakdown["template_match"] = "ไม่พบ template ที่ตรงกัน"
	}

	// Party Match (Vendor/Debtor)
	debtorCode := getStringFromInterface(accountingEntry["debtor_code"])
	creditorCode := getStringFromInterface(accountingEntry["creditor_code"])

	if debtorCode != "" && debtorCode != "null" {
		// เอกสารขาย - มี debtor
		breakdown["party_match"] = "พบลูกค้า (Debtor) ในระบบ"
	} else if creditorCode != "" && creditorCode != "null" {
		// เอกสารซื้อ - มี creditor
		breakdown["party_match"] = "พบผู้ขาย (Creditor) ในระบบ"
	} else if vendorMatchResult == nil {
		breakdown["party_match"] = "ไม่มีข้อมูล party matching"
	} else if !vendorMatchResult.Found {
		breakdown["party_match"] = "ไม่พบคู่ค้าในระบบ - ต้องตรวจสอบ"
	} else if vendorMatchResult.Method == "exact" || vendorMatchResult.Method == "tax_id" {
		breakdown["party_match"] = "พบคู่ค้าตรงกัน 100%"
	} else if vendorMatchResult.Method == "fuzzy" {
		breakdown["party_match"] = "พบคู่ค้าคล้ายกัน (fuzzy matching)"
	}

	// Data Completeness
	if factors.DataCompleteness >= 90 {
		breakdown["data_completeness"] = "ข้อมูลครบถ้วนสมบูรณ์"
	} else if factors.DataCompleteness >= 70 {
		breakdown["data_completeness"] = "ข้อมูลค่อนข้างครบ (มีบางฟิลด์ว่าง)"
	} else {
		breakdown["data_completeness"] = "ข้อมูลไม่ครบ - ต้องเพิ่มเติม"
	}

	// Field Validation
	if factors.FieldValidation >= 90 {
		breakdown["field_validation"] = "รูปแบบข้อมูลถูกต้องทั้งหมด"
	} else if factors.FieldValidation >= 70 {
		breakdown["field_validation"] = "รูปแบบข้อมูลส่วนใหญ่ถูกต้อง"
	} else {
		breakdown["field_validation"] = "พบข้อผิดพลาดในรูปแบบข้อมูล"
	}

	// Balance Validation
	if factors.BalanceValidation >= 90 {
		breakdown["balance_validation"] = "Debit = Credit (สมดุล)"
	} else {
		breakdown["balance_validation"] = "Debit ≠ Credit (ไม่สมดุล) - ต้องตรวจสอบ"
	}

	return breakdown
}

// getFloatFromInterface แปลงค่าจาก interface{} เป็น float64
func getFloatFromInterface(val interface{}) float64 {
	if val == nil {
		return 0.0
	}

	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0.0
	}
}
