// prompts.go - Centralized prompt templates for AI analysis
package ai

import (
	"encoding/json"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
)

// ============================================================================
// 📋 SECTION 1: MASTER DATA FORMATTING
// ============================================================================

// MasterDataMode defines how much master data to send to AI
type MasterDataMode string

const (
	// TemplateOnlyMode - Send only the matched template (minimal tokens)
	TemplateOnlyMode MasterDataMode = "template_only"
	// FullMode - Send all master data (fallback when no template matches)
	FullMode MasterDataMode = "full"
)

// formatMasterDataWithMode formats master data based on template matching result
// This is the smart optimization: only send full data if template doesn't match
func formatMasterDataWithMode(mode MasterDataMode, matchedTemplate *bson.M, accounts []bson.M, journalBooks []bson.M, creditors []bson.M, debtors []bson.M, shopProfile interface{}, documentTemplates []bson.M) string {
	switch mode {
	case TemplateOnlyMode:
		// OPTIMIZED PATH: Send matched template + essential master data for vendor matching
		return formatTemplateOnly(matchedTemplate, journalBooks, creditors, debtors, shopProfile)
	case FullMode:
		// FALLBACK PATH: Send all master data (original behavior)
		return formatMasterData(accounts, journalBooks, creditors, debtors, shopProfile, documentTemplates)
	default:
		// Default to full mode for safety
		return formatMasterData(accounts, journalBooks, creditors, debtors, shopProfile, documentTemplates)
	}
}

// formatTemplateOnly creates minimal prompt with matched template + essential master data
// Includes Journal Books, Creditors, Debtors for vendor matching
// Still optimized: ~7,000-9,000 tokens vs ~30,000 in full mode
func formatTemplateOnly(matchedTemplate *bson.M, journalBooks []bson.M, creditors []bson.M, debtors []bson.M, shopProfile interface{}) string {
	if matchedTemplate == nil {
		return "⚠️ Error: No template provided in template-only mode"
	}

	// Extract template details
	templateJSON, _ := json.MarshalIndent(matchedTemplate, "  ", "  ")

	// Format business context (always needed for transaction role detection)
	businessContext := ""
	if shopProfile != nil {
		shopProfileJSON, _ := json.MarshalIndent(shopProfile, "  ", "  ")
		businessContext = fmt.Sprintf(`
📌 บริบทธุรกิจของเรา:
%s
`, string(shopProfileJSON))
	}

	// Use new formatting functions
	journalBooksSection := FormatJournalBooksSection(journalBooks)
	creditorsSection := FormatCreditorsSection(creditors)
	debtorsSection := FormatDebtorsSection(debtors)
	vendorMatchingGuidance := GetVendorMatchingGuidance()

	return fmt.Sprintf(`%s

🎯 TEMPLATE MATCHED - ใช้รูปแบบบัญชีที่กำหนดไว้แล้ว:
%s

%s

%s

%s

%s

⚡ โหมดประหยัด TOKEN - คุณกำลังอยู่ใน Template-Only Mode:
- AI จับคู่เจอ template ที่ตรงกับเอกสารนี้แล้ว (ความมั่นใจ ≥85%%)
- Template = ทางลัดที่ผู้ใช้กำหนดไว้ → ทำตาม template อย่างเคร่งครัด

%s

%s

⚠️ ข้อจำกัดสำคัญ:
- ไม่มี Chart of Accounts แบบเต็ม (เพื่อประหยัด tokens)
- ✅ มี Creditors/Debtors list - ให้จับคู่ชื่อผู้ขาย/ลูกค้า
- ✅ มี Journal Books list - ให้เลือกสมุดที่เหมาะสม
- ถ้าต้องการ Chart of Accounts เต็ม → ระบุ template_used = false (AI จะ retry พร้อม full master data)
`, businessContext, string(templateJSON), GetTemplateStrictModeRules(), GetTemplateAmountDistributionRules(), vendorMatchingGuidance, journalBooksSection, creditorsSection, debtorsSection)
}

// journalBooksSection, creditorsSection, debtorsSection are defined above

// DEPRECATED: Use formatMasterDataWithMode() instead
// Kept for backward compatibility
func formatMasterData(accounts []bson.M, journalBooks []bson.M, creditors []bson.M, debtors []bson.M, shopProfile interface{}, documentTemplates []bson.M) string {
	// Use new formatting functions
	businessContext := FormatBusinessContext(shopProfile)
	journalBooksSection := FormatJournalBooksSection(journalBooks)
	creditorsSection := FormatCreditorsSection(creditors)
	debtorsSection := FormatDebtorsSection(debtors)
	vendorMatchingGuidance := GetVendorMatchingGuidance()

	// Format templates section with matching rules
	templatesSection := ""
	if len(documentTemplates) > 0 {
		templatesSection = FormatTemplatesSection(documentTemplates) +
			GetTemplateMatchingAlgorithm() +
			GetTemplateStrictModeRules() +
			GetAmountRecordingRules() +
			GetNoTemplateMatchRules() +
			FormatFinalChecklist()

	}

	// Format accounts section (only if no templates)
	accountsSection := FormatAccountsSection(accounts)
	if len(documentTemplates) > 0 {
		accountsSection = "" // Don't send accounts if templates exist (save ~8,000 tokens)
	}

	return fmt.Sprintf(`%s%s%s%s%s%s%s`,
		accountsSection,
		businessContext,
		journalBooksSection,
		creditorsSection,
		debtorsSection,
		vendorMatchingGuidance,
		templatesSection)
}

// ============================================================================
// 📋 SECTION 3: ANALYSIS RULES (Moved to prompt_rules.go)
// ============================================================================
// Analysis rules are now in prompt_rules.go for better organization

// ============================================================================
// 📋 HELPER FUNCTIONS FOR CUSTOM PROMPTS
// ============================================================================

// extractShopContext extracts promptshopinfo from shop profile
func extractShopContext(shopProfile interface{}) string {
	if shopProfile == nil {
		return ""
	}

	// Try to extract from bson.M
	if shopMap, ok := shopProfile.(bson.M); ok {
		if promptInfo, exists := shopMap["promptshopinfo"]; exists {
			if promptStr, ok := promptInfo.(string); ok && promptStr != "" {
				return fmt.Sprintf(`
🏢 ข้อมูลธุรกิจของร้าน (SHOP CONTEXT):
%s

⚠️ ใช้ข้อมูลนี้เป็นหลักในการเลือกผังบัญชีและลงตัวเลข
`, promptStr)
			}
		}
	}

	return ""
}

// extractTemplateGuidance extracts promptdescription from matched template
func extractTemplateGuidance(matchedTemplate *bson.M) string {
	if matchedTemplate == nil {
		return ""
	}

	if promptDesc, exists := (*matchedTemplate)["promptdescription"]; exists {
		if promptStr, ok := promptDesc.(string); ok && promptStr != "" {
			return fmt.Sprintf(`
📋 คำแนะนำสำหรับ Template นี้ (TEMPLATE GUIDANCE):
%s

⚠️ ปฏิบัติตามคำแนะนำนี้อย่างเคร่งครัดในการลงตัวเลข
`, promptStr)
		}
	}

	return ""
}

// ============================================================================
// 📋 MAIN PROMPT BUILDER
// ============================================================================

// BuildMultiImageAccountingPrompt creates the complete prompt for multi-image accounting analysis
// Supports conditional master data loading based on template matching
// Accepts vendorMatchInfo to inform AI about pre-matched vendors
func BuildMultiImageAccountingPrompt(allResultsJSON string, mode MasterDataMode, matchedTemplate *bson.M, accounts []bson.M, journalBooks []bson.M, creditors []bson.M, debtors []bson.M, shopProfile interface{}, documentTemplates []bson.M, vendorMatchInfo string) string {
	masterData := formatMasterDataWithMode(mode, matchedTemplate, accounts, journalBooks, creditors, debtors, shopProfile, documentTemplates)

	// Extract shop context and template guidance
	shopContext := extractShopContext(shopProfile)
	templateGuidance := extractTemplateGuidance(matchedTemplate)

	// Get all prompt sections from separate files
	analysisRules := GetAnalysisRules()
	multiImageSteps := GetMultiImageProcessingSteps()
	outputFormat := GetOutputFormatJSON()
	validationRules := GetValidationRequirements()
	additionalGuidelines := GetAdditionalGuidelines()

	// Template guidance is already emphasized in System Instruction
	// No need to duplicate the emphasis here

	return fmt.Sprintf(`คุณคือนักบัญชีไทยผู้เชี่ยวชาญ วิเคราะห์รูปภาพหลายรูปที่เกี่ยวข้องกัน แล้วสร้างรายการบัญชีเดียวที่รวมแล้ว
%s%s
🎯 งานของคุณ:
1. วิเคราะห์ความสัมพันธ์ระหว่างรูปภาพ (ใบเสร็จหลายหน้า, ใบเสร็จ+สลิป, ใบเสร็จแยกกัน)
2. รวมข้อมูลจากทุกรูปอย่างชาญฉลาด
3. สร้างรายการบัญชีที่ถูกต้องและครบถ้วน เพียง 1 รายการ

📄 ข้อมูลจาก OCR (Structured):
%s

⚠️ สำคัญมาก - ข้อมูลเต็มจากเอกสาร:
คุณจะเห็น field "raw_document_text" ในแต่ละรูป ซึ่งเป็นข้อความทั้งหมดที่อ่านได้จากเอกสาร
ใช้ข้อมูลนี้เพื่อ:
1. **หาชื่อผู้ออกเอกสาร** - มักอยู่ในบรรทัดแรกๆ ของ raw_document_text
2. **จับคู่กับ Creditors/Debtors** - ใช้ชื่อเต็มจาก raw_document_text เพื่อค้นหา
3. **หาที่อยู่, เบอร์โทร, Tax ID** - เพื่อยืนยันการจับคู่
4. **เข้าใจบริบทเต็มๆ** - หมายเหตุ, เงื่อนไข, ข้อความพิเศษ

%s

%s

%s

%s

%s

%s

คืนค่าเฉพาะ JSON ที่ถูกต้องเท่านั้น (ไม่ต้องมี markdown หรือ code blocks).`,
		shopContext,
		templateGuidance,
		allResultsJSON,
		vendorMatchInfo,
		masterData,
		analysisRules,
		multiImageSteps,
		outputFormat,
		validationRules,
		additionalGuidelines)
}
