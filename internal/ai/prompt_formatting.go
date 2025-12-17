// prompt_formatting.go - Helper functions for formatting master data
package ai

import (
	"encoding/json"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
)

// FormatBusinessContext formats shop profile into business context section
func FormatBusinessContext(shopProfile interface{}) string {
	if shopProfile == nil {
		return ""
	}

	shopProfileJSON, _ := json.MarshalIndent(shopProfile, "  ", "  ")
	return fmt.Sprintf(`
📌 บริบทธุรกิจของเรา:
%s

⚠️ สำคัญมาก - การระบุบทบาทในธุรกรรม:

🚨 STEP 0: ตรวจสอบก่อนทุกอย่าง - ชื่อผู้ขายเป็นชื่อบริษัทเราหรือไม่?
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
**ก่อนทำอะไร ต้องเช็คนี้ก่อน:**
1. ดึง**ชื่อบริษัท/แบรนด์หลัก** จากชื่อผู้ขาย/ผู้รับเงิน (vendor_name) ในเอกสาร
2. เทียบกับชื่อบริษัทเราทุกชื่อใน names[].name (จาก shopProfile)
3. ถ้า**ชื่อบริษัทหลักตรงกัน ≥ 70%%** → นี่คือ**เอกสารภายใน**หรือ**เราเป็นผู้ขาย**:
   ✅ ตัวอย่างที่ถูกต้อง:
      - vendor_name = "หจก.นิธิบุญ" + names[0].name = "หจก.นิธิบุญ" → "นิธิบุญ" ตรงกัน 100%% ✓
      - vendor_name = "บริษัท นิธิบุญ" + names[0].name = "หจก.นิธิบุญ" → "นิธิบุญ" ตรงกัน 100%% ✓
      - vendor_name = "DEMOAccount Co." + names[1].name = "DEMOAccount" → "DEMOAccount" ตรงกัน 100%% ✓
   
   ❌ ตัวอย่างที่ผิด (ต้อง match กับ Creditors แทน):
      - vendor_name = "บริษัท ซีแอนด์ฮิล" + names[0].name = "หจก.นิธิบุญ" → "ฮิล" ≠ "นิธิบุญ" ✗
      - vendor_name = "บริษัท ABC" + names[0].name = "บริษัท XYZ" → "ABC" ≠ "XYZ" ✗
   
   **→ ถ้าชื่อบริษัทหลักตรงกัน → ข้าม Creditors ทั้งหมด!**
   **→ ถ้าชื่อบริษัทหลักไม่ตรง → ไปจับคู่กับ Creditors**

4. ถ้า**ไม่ตรงกัน** → ปกติ → ไปขั้นตอนที่ 1-3 ตามด้านล่าง (จับคู่กับ Creditors/Debtors)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

1. ดูชื่อผู้ขาย/ผู้รับเงิน (vendor_name) ในใบเสร็จ/ใบกำกับภาษี
2. เปรียบเทียบกับชื่อบริษัทเรา (ดูใน names[].name ในบริบทธุรกิจข้างบน):
   - ชื่อบริษัทเราอยู่ใน names array โดยดูที่ field "name" (เช่น names[0].name = "หจก.นิธิบุญ")
   - มักจะเป็น code="th" สำหรับชื่อภาษาไทย
   - ถ้าชื่อผู้ขาย**ตรงกับชื่อบริษัทเรา** (หรือใกล้เคียง) → **เราเป็นผู้ขาย** → ใช้ลูกหนี้ (Debtors)
   - ถ้าชื่อผู้ขาย**ไม่ตรงกับชื่อบริษัทเรา** → **เราเป็นผู้ซื้อ** → ใช้เจ้าหนี้ (Creditors)
3. เทคนิคการจับคู่ (ใช้ Fuzzy Matching):
   - 🎯 **ดึงคำสำคัญ** จากชื่อในเอกสาร (ตัด "บริษัท", "จำกัด", "หจก.", "บมจ." ออก)
   - 🔍 **เปรียบเทียบคำสำคัญ** กับทุกชื่อใน Creditors/Debtors
   - ✅ **ถ้าคำสำคัญตรงกัน ≥ 70%%** → Match! (ไม่สนใจตัวสะกดเล็กน้อย)
   - 🆔 **ดู Tax ID** ถ้าตรงกัน → Match 100%% ทันที (settings.taxid)
   - ⚠️ **ตัวอย่าง:** "ซีแอนด์ฮิลล์" ควร match กับ "ซีแอนด์ฮิล" (ต่างแค่ 'ล์')
   - ถ้าไม่แน่ใจ → ส่วนใหญ่จะเป็นการซื้อ → ใช้ Creditors
`, string(shopProfileJSON))
}

// FormatJournalBooksSection formats journal books with rules
func FormatJournalBooksSection(journalBooks []bson.M) string {
	journalBooksJSON, _ := json.MarshalIndent(journalBooks, "  ", "  ")
	return fmt.Sprintf(`

📚 JOURNAL BOOKS (สมุดรายวัน):
%s

⚠️ กฎสำคัญ - Journal Book Code:
- ต้องใช้รหัสจาก Journal Books ข้างบนเท่านั้น
- ห้ามใช้ "GL", "JV" หรือรหัสอื่นที่ไม่มีในรายการ
- ให้เลือกสมุดที่เหมาะสมกับประเภทธุรกรรม
`, string(journalBooksJSON))
}

// FormatCreditorsSection formats creditors list
func FormatCreditorsSection(creditors []bson.M) string {
	creditorsJSON, _ := json.MarshalIndent(creditors, "  ", "  ")
	return fmt.Sprintf(`

👥 CREDITORS (รายชื่อเจ้าหนี้/ผู้ขาย):
%s
`, string(creditorsJSON))
}

// FormatDebtorsSection formats debtors list with matching guidance
func FormatDebtorsSection(debtors []bson.M) string {
	debtorsJSON, _ := json.MarshalIndent(debtors, "  ", "  ")
	return fmt.Sprintf(`

👤 DEBTORS (รายชื่อลูกหนี้/ลูกค้า):
%s
`, string(debtorsJSON))
}

// FormatTemplatesSection formats document templates with matching rules
func FormatTemplatesSection(documentTemplates []bson.M) string {
	if len(documentTemplates) == 0 {
		return ""
	}

	// Optimize: reduce template data to only essential fields
	compactTemplates := make([]map[string]interface{}, 0, len(documentTemplates))
	for _, template := range documentTemplates {
		details := []map[string]interface{}{}

		// Try different type assertions for details field
		if detailsArray, ok := template["details"].(bson.A); ok {
			for _, d := range detailsArray {
				if detailMap, ok := d.(bson.M); ok {
					details = append(details, map[string]interface{}{
						"accountcode": detailMap["accountcode"],
						"detail":      detailMap["detail"],
					})
				} else if detailMap, ok := d.(map[string]interface{}); ok {
					details = append(details, map[string]interface{}{
						"accountcode": detailMap["accountcode"],
						"detail":      detailMap["detail"],
					})
				}
			}
		} else if detailsArray, ok := template["details"].([]interface{}); ok {
			for _, d := range detailsArray {
				if detailMap, ok := d.(bson.M); ok {
					details = append(details, map[string]interface{}{
						"accountcode": detailMap["accountcode"],
						"detail":      detailMap["detail"],
					})
				} else if detailMap, ok := d.(map[string]interface{}); ok {
					details = append(details, map[string]interface{}{
						"accountcode": detailMap["accountcode"],
						"detail":      detailMap["detail"],
					})
				}
			}
		}

		compactTemplates = append(compactTemplates, map[string]interface{}{
			"_id":         template["_id"],
			"description": template["description"],
			"details":     details,
		})
	}

	templatesData, _ := json.MarshalIndent(compactTemplates, "  ", "  ")
	return fmt.Sprintf(`

📋 ACCOUNTING TEMPLATES (รูปแบบบัญชีที่กำหนด):
%s
`, string(templatesData))
}

// FormatAccountsSection formats chart of accounts
func FormatAccountsSection(accounts []bson.M) string {
	if len(accounts) == 0 {
		return ""
	}

	accountsJSON, _ := json.MarshalIndent(accounts, "  ", "  ")
	return fmt.Sprintf(`
ข้อมูลหลัก - ผังบัญชี:
%s

`, string(accountsJSON))
}

// FormatFinalChecklist returns checklist before submitting response
func FormatFinalChecklist() string {
	return `
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
⚠️ FINAL CHECKLIST - REVIEW BEFORE SUBMITTING RESPONSE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Before submitting your JSON response, verify:

If template_used = true:
  🔥 CRITICAL RULE: MUST use ALL accounts from template.details[]
  □ All accounts in output are from template.details[] (no additions!)
  □ Account count MUST MATCH EXACTLY template (template has 4 → output MUST have 4)
  □ EVERY account in template.details[] MUST appear in journal_entries[]
  □ If template has VAT account → output MUST include VAT entry (even if 0)
  □ No tax accounts added unless they exist in template.details[]
  □ template_id and template_name are set
  □ ai_explanation.account_selection_logic.template_details explains why template was used
  
  💡 Note: Confidence score คำนวณโดยระบบอัตโนมัติ (weighted calculation)
  
  Example: Template has [ค่าน้ำมัน, ภาษีซื้อ, เงินสด]
  → Output MUST have 3 entries (not 2, not 4, exactly 3)

If template_used = false:
  □ Verified no matching template exists
  □ ALL account codes verified to exist in provided Chart of Accounts (Master Data)
  □ Did NOT use account codes from internal knowledge
  □ Applied accounting standards appropriately
  □ Explained reasoning in ai_explanation

Both cases:
  □ Debit and Credit recorded based on actual document analysis
  □ All amounts are positive numbers
  □ All account codes exist in provided Master Data
  □ creditor_code/debtor_code filled correctly
`
}
