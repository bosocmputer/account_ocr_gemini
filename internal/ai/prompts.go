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

	// Format Journal Books (essential for correct journal_book_code)
	journalBooksJSON, _ := json.MarshalIndent(journalBooks, "  ", "  ")
	journalBooksSection := fmt.Sprintf(`

📚 JOURNAL BOOKS (สมุดรายวัน):
%s

⚠️ กฎสำคัญ - Journal Book Code:
- ต้องใช้รหัสจาก Journal Books ข้างบนเท่านั้น
- ห้ามใช้ "GL", "JV" หรือรหัสอื่นที่ไม่มีในรายการ
- ให้เลือกสมุดที่เหมาะสมกับประเภทธุรกรรม
`, string(journalBooksJSON))

	// Format Creditors (for vendor matching when we are buyer)
	creditorsJSON, _ := json.MarshalIndent(creditors, "  ", "  ")
	creditorsSection := fmt.Sprintf(`

👥 CREDITORS (รายชื่อเจ้าหนี้/ผู้ขาย):
%s
`, string(creditorsJSON))

	// Format Debtors (for customer matching when we are seller)
	debtorsJSON, _ := json.MarshalIndent(debtors, "  ", "  ")
	debtorsSection := fmt.Sprintf(`

👤 DEBTORS (รายชื่อลูกหนี้/ลูกค้า):
%s

🔍 สำคัญมาก - การจับคู่ชื่อภาษาไทย (Thai Name Matching):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

**ปัญหาที่พบบ่อย:**
- OCR อาจอ่านผิด: "บริษัท ซีแอนด์ฮิลล์" อาจกลายเป็น "ซีแอนฮิล", "ซีแอนด์ฮิล"
- ชื่อย่อ: "บจก." vs "บริษัท จำกัด"
- พิมพ์ผิด: "สำนักงานใหญ่" vs "สำนักงานใหญ่่"
- ตัวสะกด: "เชียงใหม่" vs "เชียงใหม่่"

**วิธีการจับคู่ที่ถูกต้อง:**

1️⃣ **ดึงคำสำคัญ (Keywords) จากชื่อในเอกสาร:**
   - "บริษัท ซีแอนด์ฮิลล์ จำกัด (สำนักงานใหญ่)" → คำสำคัญ: "ซีแอนด์ฮิลล์", "ซีแอนฮิล"
   - "หจก. นิธิบุญ" → คำสำคัญ: "นิธิบุญ"
   - "บมจ. เอ็ม วิชั่น" → คำสำคัญ: "เอ็ม", "วิชั่น"

2️⃣ **ค้นหาในรายการ Creditors/Debtors:**
   - เปรียบเทียบคำสำคัญกับ name field
   - ถ้าพบคำสำคัญที่ตรงกัน → Match!
   - ไม่จำเป็นต้องตรงทุกตัวอักษร

3️⃣ **ตัวอย่างการจับคู่:**
   
   ✅ GOOD MATCHES:
   - เอกสาร: "บริษัท ซีแอนด์ฮิลล์ จำกัด"
     Master: "บริษัท ซีแอนฮิล จำกัด" → ใกล้เคียง ✓
   
   - เอกสาร: "หจก. นิธิบุญ"
     Master: "บริษัท นิธิบุญ จำกัด" → มีคำว่า "นิธิบุญ" ✓
   
   - เอกสาร: "บมจ. เอ็ม วิชั่น"
     Master: "บริษัท เอ็ม วิชั่น จำกัด (มหาชน)" → ตรงกัน ✓

   ❌ BAD MATCHES:
   - เอกสาร: "บริษัท ซีแอนด์ฮิลล์"
     Master: "บริษัท ABC" → ไม่เกี่ยวข้อง ✗

4️⃣ **ความมั่นใจ (Confidence) - ใช้ Fuzzy Matching:**
   - 90-100%: คำสำคัญตรงกันเกือบทั้งหมด (ต่างแค่ตัวสะกดเล็กน้อย เช่น "ล์")
   - 70-89%: คำสำคัญตรงกัน แต่อาจมีคำเพิ่ม/ลดออก
   - 50-69%: พอมีความเกี่ยวข้อง แต่ไม่แน่ใจ → ใช้ได้ถ้าเป็นตัวเลือกเดียว
   - < 50%: ไม่เกี่ยวข้อง → ใช้ Unknown Vendor/Customer

🎯 **ตัวอย่าง Fuzzy Matching ที่ถูกต้อง:**
   - "ซีแอนด์ฮิลล์" (เอกสาร) ⟷ "ซีแอนด์ฮิล" (Master) → Match 95% ✅ (ต่างแค่ "ล์")
   - "บจก.สยามแม็คโคร" (เอกสาร) ⟷ "บริษัท สยามแม็คโคร จำกัด" (Master) → Match 90% ✅
   - "makro" (เอกสาร) ⟷ "ซีพี แอ็กซ์ตร้า" (Master) → Match 0% ❌ (ต้องมี "makro" ใน description)

🚫 **CRITICAL: ห้าม Match แบบผิด ๆ:**
   ❌ "ศรีทองโชตนา" (เอกสาร) ⟷ "บางจากกรีนแนท" (Master) → ทั้งคู่ขายน้ำมัน แต่เป็นคนละบริษัท!
   ❌ "โลตัส" (เอกสาร) ⟷ "แม็คโคร" (Master) → ทั้งคู่เป็นร้านค้าปลีก แต่เป็นคนละบริษัท!
   ❌ "7-11" (เอกสาร) ⟷ "เซเว่น อีเลฟเว่น" (Master) → คล้ายกัน แต่ต้องตรวจสอบ Tax ID!
   
   ⚠️ **หลักการ: Match ด้วยชื่อ/คำสำคัญ ไม่ใช่ประเภทธุรกิจ!**
      - ใช้คำสำคัญในชื่อบริษัทเป็นหลัก (เช่น "ศรีทอง", "บางจาก")
      - ห้ามอนุมานจาก "ขายสินค้าเหมือนกัน" → ไม่ได้แปลว่าเป็นบริษัทเดียวกัน
      - ถ้าชื่อไม่ตรง + Tax ID ไม่ตรง → ต้องเป็น Unknown Vendor

5️⃣ **กรณีพิเศษ:**
   - ถ้าชื่อในเอกสารเป็นชื่อย่อมาก → ลอง Tax ID matching
   - ถ้า Tax ID ตรงกัน → Match 100% แม้ชื่อจะต่างกัน
   - ถ้าไม่มี Tax ID และชื่อไม่ตรง → Unknown Vendor/Customer
   - ⚠️ **ยอมรับความต่างเล็กน้อย** (typo, ตัวสะกด, วงเล็บ) → ยังถือว่า Match

6️⃣ **คำแนะนำสำคัญ:**
   - **ใช้ชื่อจาก Master Data เป็นหลัก** - ห้ามใช้ชื่อจาก OCR โดยตรง
   - ถ้าจับคู่ได้ → ใช้ name และ code จาก Master Data
   - ถ้าจับคู่ไม่ได้ → ใช้ "Unknown Vendor" หรือ "Unknown Customer"
   - ระบุ matching_method: exact_match, fuzzy_match, tax_id_match, หรือ not_found
   - **ยอมรับความต่างเล็กน้อย (≥70%)** - "ซีแอนด์ฮิลล์" = "ซีแอนด์ฮิล" (fuzzy_match)

`, string(debtorsJSON))

	return fmt.Sprintf(`%s

🎯 TEMPLATE MATCHED - ใช้รูปแบบบัญชีที่กำหนดไว้แล้ว:
%s

⚡ โหมดประหยัด TOKEN - คุณกำลังอยู่ใน Template-Only Mode:
- AI จับคู่เจอ template ที่ตรงกับเอกสารนี้แล้ว (ความมั่นใจ ≥85 percent)
- Template = ทางลัดที่ผู้ใช้กำหนดไว้ → ทำตาม template อย่างเคร่งครัด

🚨 กฎสำคัญที่สุด - ใช้ Template แบบเคร่งครัด:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1️⃣ **ต้องใช้ทุก account ที่อยู่ใน template.details[] - ห้ามข้าม!**
   - ถ้า template มี 4 accounts → ต้องใช้ครบ 4 accounts
   - ถ้า template มี 2 accounts → ต้องใช้ครบ 2 accounts
   - **ห้าม** เลือกใช้บางบัญชีแล้วข้ามบางบัญชี

2️⃣ **ความหมายของ fields ใน template.details[]:**
   - template.details[].accountcode = **รหัสบัญชี** (ใช้เป็น account_code)
   - template.details[].detail = **ชื่อบัญชี** (ใช้เป็น account_name)
   - ตัวอย่าง: {"accountcode": "533020", "detail": "ค่าธรรมเนียม-ค่าที่ปรึกษาบัญชี"}
     → account_code = "533020"
     → account_name = "ค่าธรรมเนียม-ค่าที่ปรึกษาบัญชี"

3️⃣ **Journal Book Code:**
   - ใช้จาก template.bookcode (ถ้ามี)
   - หรือใช้จาก template.module (ถ้า bookcode ว่าง)
   - **ห้าม** ใช้ "GL" หรือ default อื่นๆ

4️⃣ **ห้ามเพิ่ม/ลดบัญชี:**
   - ห้ามเพิ่มบัญชีอื่นๆ แม้ว่าจะเป็นมาตรฐานทางบัญชี
   - ห้ามเพิ่มบัญชีภาษี (VAT, Withholding Tax) ถ้า template ไม่มี
   - ห้ามข้ามบัญชีที่ template กำหนดไว้

📋 ข้อมูลที่ต้องการจากคุณ:
1. **ใช้ทุก account ใน template.details[]** - ห้ามข้าม!
2. ตั้งค่า template_used = true
3. ระบุ template_id ที่ใช้
4. ระบุ journal_book_code จาก template.bookcode หรือ template.module
5. **กรอกจำนวนเงิน debit/credit ตามที่เห็นในเอกสารจริงๆ**

🚨 กฎสำคัญ - บันทึกตามเอกสารจริง:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
**บันทึกตัวเลขตามที่เห็นในเอกสารจริงๆ**
**ไม่ต้องบังคับให้ Balance** (Total Debit อาจจะ ≠ Total Credit ก็ได้)
**ถ้าเอกสารผิด → ให้ user ตรวจสอบและแก้เองในภายหลัง**
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

💡 หลักการบันทึก:
- **ใช้เฉพาะตัวเลขที่ปรากฏในเอกสาร** - ห้ามคำนวณหรือหาค่าเอง
- ถ้าเอกสารไม่ระบุยอดเงินสดที่จ่ายชัดเจน → ห้ามคำนวณ (เช่น ยอดรวม - หัก ณ ที่จ่าย)
- บันทึก Debit/Credit ตามความเป็นจริงของแต่ละรายการ
- **อย่าปรับตัวเลขให้ Balance โดยอัตโนมัติ**
- ถ้า Total Debit ≠ Total Credit → ปล่อยให้เป็นไปตามเอกสาร

📌 ตัวอย่าง - บันทึกเฉพาะตัวเลขที่เห็น:

เอกสารระบุ:
- มูลค่าสินค้า: 1,869.16
- VAT: 130.84
- ยอดรวม: 2,000
- ชำระโดย: เงินสด

Template มี: 531220 (ค่าน้ำมัน) + 115810 (ภาษีซื้อ) + 111110 (เงินสด)

✅ ถูกต้อง - ใช้เฉพาะตัวเลขที่เห็น:
{
  "531220": {"debit": 1869.16, "credit": 0},  // เห็นในเอกสาร
  "115810": {"debit": 130.84, "credit": 0},   // เห็นในเอกสาร
  "111110": {"debit": 0, "credit": 2000}      // เห็นในเอกสาร (ยอดรวม)
}
→ Total Debit (2000) = Total Credit (2000) ✓

📌 ตัวอย่าง - เอกสารมี "หัก ณ ที่จ่าย":

เอกสารระบุ:
- มูลค่าสินค้า: 2,000
- VAT: 140
- หัก ณ ที่จ่าย: 60
- ยอดรวม: 2,140
- ชำระโดย: เงินสด

❌ ผิด - คำนวณเอง:
{
  "533020": {"debit": 2000},
  "111110": {"credit": 2080}  // ← คำนวณ 2140-60=2080 (ผิด! เลข 2080 ไม่มีในเอกสาร)
}

✅ ถูก - ใช้เฉพาะตัวเลขที่เห็น:
{
  "533020": {"debit": 2000},    // เห็นในเอกสาร
  "115810": {"debit": 140},     // เห็นในเอกสาร
  "215550": {"credit": 60},     // เห็นในเอกสาร
  "111110": {"credit": 2140}    // เห็นในเอกสาร (ยอดรวม)
}
→ ไม่ Balance แต่ถูกต้อง - บันทึกตามที่เห็น

🎯 สรุป:
- บันทึกตัวเลขตามที่วิเคราะห์ได้จากเอกสาร
- ใช้หลักบัญชีที่ถูกต้อง (Debit/Credit ตามประเภทบัญชี)
- **ไม่ต้องกังวลเรื่อง Balance** - ถ้าผิด user จะแก้เอง
- ความถูกต้องของข้อมูล > การบังคับให้ Balance

⚠️ ข้อจำกัดสำคัญ:
- ไม่มี Chart of Accounts แบบเต็ม (เพื่อประหยัด tokens)
- ✅ มี Creditors/Debtors list - ให้จับคู่ชื่อผู้ขาย/ลูกค้า
- ✅ มี Journal Books list - ให้เลือกสมุดที่เหมาะสม
- ถ้าต้องการ Chart of Accounts เต็ม → ระบุ template_used = false (AI จะ retry พร้อม full master data)

สมมติ template มีข้อมูล:
{
  "_id": "693a84c83c54ede15017fcbc",
  "description": "บันทึกค่าทำบัญชี บริษัทซีแอนฮิล",
  "bookcode": "02",  // ← ใช้เป็น journal_book_code
  "details": [
    {"accountcode": "533020", "detail": "ค่าธรรมเนียม-ค่าที่ปรึกษาบัญชี"},
    {"accountcode": "115810", "detail": "ค่าภาษีซื้อ"},
    {"accountcode": "115840", "detail": "ค่าภาษีเงินได้นิติบุคคลถูกหัก ณ ที่จ่าย"},
    {"accountcode": "111110", "detail": "เงินสดในมือ"}
  ]
}

✅ วิธีที่ถูก - ใช้ครบทุกบัญชี:
"entries": [
  {"account_code": "533020", "account_name": "ค่าธรรมเนียม-ค่าที่ปรึกษาบัญชี", "debit": 2000, "credit": 0},
  {"account_code": "115810", "account_name": "ค่าภาษีซื้อ", "debit": 140, "credit": 0},
  {"account_code": "115840", "account_name": "ค่าภาษีเงินได้นิติบุคคลถูกหัก ณ ที่จ่าย", "debit": 0, "credit": 0},
  {"account_code": "111110", "account_name": "เงินสดในมือ", "debit": 0, "credit": 2140}
],
"journal_book_code": "02"  // ← จาก template.bookcode

❌ วิธีที่ผิด - ข้ามบัญชีบางตัว:
"entries": [
  {"account_code": "533020", "debit": 2140},
  {"account_code": "111110", "credit": 2140}
]  // ← ผิด! ข้าม 115810 และ 115840

⚠️ หมายเหตุ:
- ถ้าบัญชีไหนไม่มียอดเงิน → ใส่ debit: 0, credit: 0
- แต่**ต้องมีในรายการ entries[] ครบทุกบัญชี**
- เพื่อให้ consistent และตรวจสอบได้ว่าใช้ template นี้
`, businessContext, string(templateJSON), journalBooksSection, creditorsSection, debtorsSection)
}

// journalBooksSection, creditorsSection, debtorsSection are defined above

// DEPRECATED: Use formatMasterDataWithMode() instead
// Kept for backward compatibility
func formatMasterData(accounts []bson.M, journalBooks []bson.M, creditors []bson.M, debtors []bson.M, shopProfile interface{}, documentTemplates []bson.M) string {
	accountsJSON, _ := json.MarshalIndent(accounts, "  ", "  ")
	journalBooksJSON, _ := json.MarshalIndent(journalBooks, "  ", "  ")
	creditorsJSON, _ := json.MarshalIndent(creditors, "  ", "  ")
	debtorsJSON, _ := json.MarshalIndent(debtors, "  ", "  ")

	// Format business context
	businessContext := ""
	if shopProfile != nil {
		shopProfileJSON, _ := json.MarshalIndent(shopProfile, "  ", "  ")
		businessContext = fmt.Sprintf(`
📌 บริบทธุรกิจของเรา:
%s

⚠️ สำคัญมาก - การระบุบทบาทในธุรกรรม:

🚨 STEP 0: ตรวจสอบก่อนทุกอย่าง - ชื่อผู้ขายเป็นชื่อบริษัทเราหรือไม่?
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
**ก่อนทำอะไร ต้องเช็คนี้ก่อน:**
1. ดึง**ชื่อบริษัท/แบรนด์หลัก** จากชื่อผู้ขาย/ผู้รับเงิน (vendor_name) ในเอกสาร
2. เทียบกับชื่อบริษัทเราทุกชื่อใน names[].name (จาก shopProfile)
3. ถ้า**ชื่อบริษัทหลักตรงกัน ≥ 70%** → นี่คือ**เอกสารภายใน**หรือ**เราเป็นผู้ขาย**:
   ✅ ตัวอย่างที่ถูกต้อง:
      - vendor_name = "หจก.นิธิบุญ" + names[0].name = "หจก.นิธิบุญ" → "นิธิบุญ" ตรงกัน 100% ✓
      - vendor_name = "บริษัท นิธิบุญ" + names[0].name = "หจก.นิธิบุญ" → "นิธิบุญ" ตรงกัน 100% ✓
      - vendor_name = "DEMOAccount Co." + names[1].name = "DEMOAccount" → "DEMOAccount" ตรงกัน 100% ✓
   
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
   - ✅ **ถ้าคำสำคัญตรงกัน ≥ 70%** → Match! (ไม่สนใจตัวสะกดเล็กน้อย)
   - 🆔 **ดู Tax ID** ถ้าตรงกัน → Match 100% ทันที (settings.taxid)
   - ⚠️ **ตัวอย่าง:** "ซีแอนด์ฮิลล์" ควร match กับ "ซีแอนด์ฮิล" (ต่างแค่ 'ล์')
   - ถ้าไม่แน่ใจ → ส่วนใหญ่จะเป็นการซื้อ → ใช้ Creditors

`, string(shopProfileJSON))
	}

	// Format document templates (only if exist)
	templatesSection := ""
	if len(documentTemplates) > 0 {
		// 🎯 Optimize: ลดข้อมูล template ให้เหลือเฉพาะที่จำเป็น
		compactTemplates := make([]map[string]interface{}, 0, len(documentTemplates))
		for _, template := range documentTemplates {
			// สร้าง compact version ของ details
			details := []map[string]interface{}{}

			// Try different type assertions for details field
			if detailsArray, ok := template["details"].(bson.A); ok {
				// bson.A type (MongoDB array)
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
				// []interface{} type
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
		templatesSection = fmt.Sprintf(`

📋 ACCOUNTING TEMPLATES (รูปแบบบัญชีที่กำหนด):
%s

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🚨 ABSOLUTE RULE #1 - TEMPLATE MATCHING (กฎการจับคู่เทมเพลต)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

⚡ STEP 1: EXTRACT RECEIPT CATEGORY

🎯 Algorithm for extracting the main category:

1️⃣ Read the document and identify the "main category" in 1-3 words:
   
   🚨 CRITICAL: Check document type FIRST before extracting category!
   
   Method (ordered by priority):
   
   A. IF document contains ANY of these keywords:
      - "หนังสือรับรองการหักภาษี ณ ที่จ่าย"
      - "ภ.ง.ด.53", "ภ.ง.ด.3", "ภ.ง.ด.1-ก"
      - "ตามมาตรา 50 ทวิ"
      - "ภาษีหัก ณ ที่จ่าย" (as document title, not just amount)
      
      → This is a "Withholding Tax Certificate"
      → Look ONLY at "Income Type" (ประเภทเงินได้) under Section 40/มาตรา 40
      → Extract as: "เงินเดือน" (salary), "ค่าจ้าง" (wage), "ค่าเช่า" (rent), "บริการวิชาชีพ" (professional service), etc.
      → 🚫 IGNORE item descriptions like "ค่าธรรมเนียม", "ค่าที่ปรึกษา" - they are NOT relevant for this document type!
   
   B. IF regular receipt/tax invoice:
      → Look at: vendor name, product/service type, main product name
      → Extract as: "น้ำมัน" (fuel), "ไฟฟ้า" (electricity), "อาหาร" (food), "ทำบัญชี" (accounting), etc.
   
   C.Document type determines extraction method
   ✓ Withholding Tax Certificate → Income Type ONLY (ignore item descriptions)
   ✓ Regular receipt → Focus on goods/services received
   ✓ Use concise, clear language (1-3 words)
   
3️⃣ Examples:
   ✓ "หนังสือรับรองฯ มาตรา 40(1) เงินเดือน ค่าจ้าง" → Extract: "เงินเดือน"
   ✗ "หนังสือรับรองฯ + รายการ: ค่าธรรมเนียม-ค่าที่ปรึกษาบัญชี" → DO NOT extract: "ทำบัญชี" ❌
   ✓ "ใบเสร็จ ปตท. น้ำมันดีเซล" → Extract: "น้ำมัน"ฟฟ้า" (electricity), "อินเตอร์เน็ท" (internet), etc.
   
   D. IF uncertain:
      → Extract as: "เบ็ดเตล็ด" (miscellaneous) or closest category

2️⃣ Key principles:
   ✓ Focus on "goods/services received", NOT "vendor name"
   ✓ Use concise, clear language (1-3 words)
   ✓ Try to identify clear main categories

⚡ STEP 2: FIND BEST MATCHING TEMPLATE

🎯 Semantic Matching Algorithm (generic - works for all document types):

1️⃣ Compare the "main category" from STEP 1 with ALL template.description:
   
   Method:
   A. Check if keyword appears in description:
      - "น้ำมัน" in "ค่าน้ำมัน" → MATCH ✓
      - "ไฟฟ้า" in "ค่าไฟฟ้า" → MATCH ✓
      - "เงินเดือน" in "บันทึกค่าทำบัญชี" → NO MATCH ✗
   
   B. Use semantic similarity:
      - "ทำบัญชี" ≈ "บันทึกค่าทำบัญชี" → MATCH ✓
      - "อินเตอร์เน็ท" ≈ "ค่าอินเตอร์เน็ท" → MATCH ✓
   
   C. Reject unrelated matches:
      - "เงินเดือน" ≠ "ค่าน้ำมัน" → NO MATCH ✗
      - "ค่าเช่า" ≠ "ค่าไฟฟ้า" → NO MATCH ✗

2️⃣ Decision Rules:
   
   ✅ USE template when:
   - Direct keyword match (confidence ≥ 95 percent)
   - High semantic similarity (confidence ≥ 90 percent)
   - Confident that they are related
   
   ❌ DON'T use template (SET template_used = false) when:
   - No matching template found
   - Keywords are unrelated
   - Uncertain (confidence < 80 percent)
   
   → Use Master Data instead

3️⃣ Matching Examples (for all document types):

   ✓ GOOD MATCHES:
   "น้ำมัน" + template "ค่าน้ำมัน" → ✓ USE
   "ไฟฟ้า" + template "ค่าไฟฟ้า" → ✓ USE
   "ทำบัญชี" + template "บันทึกค่าทำบัญชี" → ✓ USE
   "อินเตอร์เน็ท" + template "ค่าอินเตอร์เน็ท" → ✓ USE
   
   ✗ BAD MATCHES (forbidden):
   "เงินเดือน" + template "บันทึกค่าทำบัญชี" → ✗ template_used = false
   "ค่าเช่า" + template "ค่าน้ำมัน" → ✗ template_used = false
   "น้ำมัน" + template "ค่าใช้จ่ายเบ็ดเตล็ด" → ✗ template_used = false (more specific template exists)

4️⃣ ⚠️ Universal Rules (apply to all documents):
   
   ✓ DO:
   - Compare with ALL template descriptions
   - Select the best matching template

5️⃣ 🚨 SPECIAL RULE for Withholding Tax Certificate:
   
   IF document type = "Withholding Tax Certificate":
   
   ALLOWED templates ONLY:
   - Template description contains "เงินเดือน" (if income type = salary)
   - Template description contains "ค่าจ้าง" (if income type = wage)
   - Template description contains "ค่าเช่า" (if income type = rent)
   - Template description matches income type EXACTLY
   
   FORBIDDEN templates:
   - "บันทึกค่าทำบัญชี" ❌ (even if item description mentions accounting)
   - "ค่าใช้จ่ายเบ็ดเตล็ด" ❌
   - Any template that doesn't match income type ❌
   
   IF no matching template for income type:
   → MUST set template_used = false
   → Use Master Data to create entry
   
   Example:
   ✗ Withholding Tax Cert + "มาตรา 40(1)" + item "ค่าที่ปรึกษาบัญชี"
     → category = "เงินเดือน" (from income type)
     → Check templates: no "เงินเดือน" template found
     → Result: template_used = false ✓
     → DO NOT use "บันทึกค่าทำบัญชี" ❌
   - When uncertain → template_used = false (safer)
   
   ✗ DON'T:
   - Force use of unrelated templates
   - Look at template.details (accounts)
   - Use generic template (เบ็ดเตล็ด) when specific template exists

⚡ STEP 3: IF TEMPLATE MATCHED - STRICT MODE

Decision:
- If match found → PROCEED TO STEP 3 (use template strictly)
- If NO match found → SET template_used = false → Use Master Data instead

⚠️ Principle: Template matching must be strict - use when matched, don't force when not matched!

✅ MUST DO when using template:
  ✓ Use EXACTLY all accounts from template.details[] (accountcode → account_code, detail → account_name)
  ✓ Use ALL accounts - if template has 3 accounts, output must have 3 accounts
  ✓ Record amounts using ONLY numbers EXPLICITLY VISIBLE in document
  ✓ Use accounting principles ONLY for Debit/Credit side determination (NOT for calculating amounts)
  ✓ DO NOT force Balance - record actual amounts as seen in document
  ✓ NEVER calculate, subtract, add, or derive any amount
  ✓ Set template_used = true
  ✓ Set template_id = template._id
  ✓ Set template_name = template.description
  ✓ Set confidence = 99

❌ ABSOLUTELY FORBIDDEN (ห้ามเด็ดขาด - ไม่มีข้อยกเว้น):
  ✗ NEVER add accounts beyond template (even if receipt has VAT/WHT)
  ✗ NEVER add Input VAT accounts if template doesn't include them - EVEN IF RECEIPT SHOWS VAT!
  ✗ NEVER add Withholding Tax accounts if template doesn't include them
  ✗ NEVER add Output VAT accounts if template doesn't include them
  ✗ NEVER add ANY tax-related accounts if template doesn't include them
  ✗ NEVER remove accounts from template (must use all)
  ✗ NEVER substitute accounts (e.g., replace one expense account with another)
  ✗ NEVER use your internal accounting knowledge to "improve" the template
  ✗ NEVER think "this should have tax accounts" - Template = User's explicit choice!
  ✗ NEVER use account codes that don't exist in the provided Master Data

📌 WHY SO STRICT? (ทำไมถึงเข้มงวด?)
  → Template = User's predefined accounting preference
  → User CHOSE these specific accounts for a reason
  → If template omits tax accounts → User wants simplified entry (no tax split)
  → Your job: OBEY template, NOT

✅ MUST DO when no template matches:
  ✓ Set template_used = false
  ✓ Set template_id = null or ""
  ✓ Set template_name = null or ""
  ✓ Use Master Data (Chart of Accounts) to select appropriate accounts
  ✓ Apply standard accounting rules (VAT, WHT, etc.) as needed
  ✓ Set confidence based on actual extraction quality (not 99)

Example: Receipt for "เงินเดือน" (salary) but no matching template exists
  → template_used = false
  → Select accounts from Chart of Accounts (e.g., 511010 เงินเดือน, 111110 เงินสด, 221001 ภาษีหัก ณ ที่จ่าย)
  → Create journal entry using accounting knowledge

⚡ STEP 5: AMOUNT DISTRIBUTION STRATEGY

⚡ STEP 5: AMOUNT RECORDING RULES (กฎการบันทึกจำนวนเงิน)

🚨 ABSOLUTE RULE - USE ONLY VISIBLE NUMBERS (ใช้เฉพาะตัวเลขที่เห็น):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
❌ NEVER CALCULATE: ห้ามคำนวณหรือหาค่าใดๆ เอง
❌ NEVER SUBTRACT: ห้ามลบ (เช่น ยอดรวม - หัก ณ ที่จ่าย)
❌ NEVER ADD: ห้ามบวก (เช่น มูลค่าสินค้า + VAT)
❌ NEVER DERIVE: ห้ามอนุมาน (เช่น ยอดเงินสดที่จ่ายจริง)

✅ ONLY USE numbers that are EXPLICITLY WRITTEN in the document:
  → If document shows "2,140" → Use 2140
  → If document shows "60" → Use 60
  → If document shows "140" → Use 140
  → If document shows "2,000" → Use 2000
  
❌ DO NOT create numbers by calculation:
  → Do NOT calculate 2140 - 60 = 2080
  → Do NOT calculate 2000 + 140 = 2140
  → Even if it makes accounting sense - DON'T DO IT!

📌 Example - What you SEE vs What you MUST NOT DO:
Document shows:
  - มูลค่าสินค้า: 2,000
  - VAT: 140
  - หัก ณ ที่จ่าย: 60
  - จำนวนเงินรวมทั้งสิ้น: 2,140 ← This is the TOTAL, use this!
  - ชำระโดย: เงินสด (no separate amount shown)

✅ CORRECT - Use only visible numbers:
  533020: 2000 (visible)
  115810: 140 (visible)
  215550: 60 (visible)
  111110: 2140 (visible - use "จำนวนเงินรวมทั้งสิ้น" as payment amount)
  → Result: Debit 2140 ≠ Credit 2200 (not balanced, but CORRECT!)

❌ WRONG - Calculated number:
  111110: 2080 (calculated 2140-60 = WRONG! Number 2080 not in document)
  → Result: Debit 2140 = Credit 2140 (balanced, but WRONG data!)

⚠️ CRITICAL RULES FOR PAYMENT WITH WHT (Withholding Tax):

1️⃣ **"จำนวนเงินรวมทั้งสิ้น" = Payment Amount**
   → Use the TOTAL as payment for Cash/Bank account
   → Even if document shows "หัก ณ ที่จ่าย" (WHT)
   → DON'T subtract WHT from total!

2️⃣ **WHT Accounting Logic:**
   
   Document shows:
   - มูลค่า: 2,000
   - VAT: 140
   - หัก ณ ที่จ่าย: 60
   - ยอดรวม: 2,140
   - ชำระโดย: เงินสด (no separate amount shown)
   
   Correct interpretation:
   - Total invoice = 2,140 (This is what we pay!)
   - WHT = 60 (This is a LIABILITY/ภาระหนี้, NOT a deduction from payment!)
   - Cash payment = 2,140 (Use the TOTAL, not 2,140-60!)
   
   Why? Because:
   - We pay 2,140 to vendor (Cash account)
   - We OWE government 60 (WHT Payable account)
   - This creates unbalanced entry, which is CORRECT!

3️⃣ **Result:**
   → Debit: 2,140 (expense + VAT)
   → Credit: 2,140 + 60 = 2,200 (cash + WHT)
   → NOT balanced - but reflects actual document!

🔴 CRITICAL RULE - When template has multiple accounts:
  → Use ONLY amounts that are EXPLICITLY WRITTEN in the document
  → Map each visible amount to ONE account
  → Use accounting logic ONLY to determine Debit/Credit side (NOT to calculate amounts!)
  → NEVER calculate amounts (ห้ามคำนวณตัวเลข):
    ❌ Don't subtract (ยอดรวม - หัก ณ ที่จ่าย)
    ❌ Don't add (มูลค่า + VAT)
    ❌ Don't derive any number not visible
  → If document shows 4 numbers → Use all 4 numbers as-is
  → Each visible number should map to exactly 1 account

Example - Template "Fuel" with 2 accounts:
  Template: [{accountcode: "531220", detail: "Fuel Expense"}, {accountcode: "111110", detail: "Cash"}]
  Receipt: 2,000 THB (including VAT 130.84)

  ✅ CORRECT Output:
  {
    "entries": [
      {"account_code": "531220", "account_name": "Fuel Expense", "debit": 2000, "credit": 0},
      {"account_code": "111110", "account_name": "Cash", "debit": 0, "credit": 2000}
    ],
    "template_used": true,
    "template_id": "...",
    "template_name": "Fuel",
    "ai_explanation": {
      "account_selection_logic": {
        "template_used": true,
        "template_details": "Used template 'Fuel' with 2 accounts (531220, 111110). Did NOT add VAT account even though receipt shows VAT, because template doesn't include it. User chose simplified entry."
      }
    }
  }

  ❌ WRONG Output (DO NOT DO THIS):
  {
    "entries": [
      {"account_code": "531220", "debit": 1869.16},  ← Split VAT out
      {"account_code": "115XXX", "debit": 130.84},   ← ❌ ADDED TAX ACCOUNT!
      {"account_code": "111110", "credit": 2000}
    ]
  }
  → This violates Rule #1: You added a tax account which is NOT in template!

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📚 MORE EXAMPLES - READ BEFORE EVERY ANALYSIS (ตัวอย่างเพิ่มเติม)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Example 2: Template "Electricity" (ค่าไฟ)
  Template has 2 accounts: Electricity expense account, Bank account
  Receipt: 5,000 + VAT 350 = 5,350 THB

  ✅ CORRECT: Use only the 2 accounts from template, total = 5350
  ❌ WRONG: Add a VAT account (template doesn't have it!)

Example 3: Template "Accounting Service" (ค่าทำบัญชี)
  Template has 3 accounts: Professional Fees, WHT receivable, Bank
  Document shows 3 visible numbers:
    - Service amount: 10,000 (visible)
    - WHT 3%%: 300 (visible)
    - Payment amount: 9,700 (visible in payment section)

  ✅ CORRECT: Use all 3 visible numbers as-is
  - Professional Fees: Debit 10,000 (visible in document)
  - WHT receivable: Debit 300 (visible in document)
  - Bank: Credit 9,700 (visible in document)

  ❌ WRONG: Calculate 10,000 - 300 = 9,700 (even if makes sense!)
  → If document DOESN'T show 9,700 explicitly, DO NOT use template!
  
  Note: This template INCLUDES WHT account in template.details[], so we use it!

Example 4: No Template Match
  Receipt: "Office Snacks" (ขนมสำนักงาน)
  No matching template found

  ✅ CORRECT: Set template_used = false, analyze using Master Data
  → Can add VAT account if receipt shows VAT AND account exists in Master Data
  → Use accounting knowledge freely
  → MUST verify all account codes exist in provided Master Data (Chart of Accounts)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📋 SECTION 4: NO TEMPLATE MATCH - FREE ANALYSIS MODE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

⚠️ ONLY apply this section if template_used = false (no matching template)

When NO template matches:
  ✓ Use Master Data provided in this message:
    - Chart of Accounts (ผังบัญชี) - ONLY use account codes from this list
    - Journal Books (สมุดรายวัน) - ONLY use journal codes from this list
    - Creditors/Debtors (เจ้าหนี้/ลูกหนี้)

  ✓ Apply standard Thai accounting practices

  ✓ Add tax accounts if receipt shows VAT/WHT (CRITICAL RULE):
    - Receipt has VAT 7%% → Search for Input VAT account in Chart of Accounts
    - Receipt has WHT → Search for WHT account in Chart of Accounts
    - ONLY add if account exists in Master Data (search by account name/description)
    - DO NOT assume account code numbers - each shop has different chart of accounts

  ✓ Account Code Validation (MANDATORY):
    - EVERY account code you use MUST exist in the provided Chart of Accounts
    - Search Chart of Accounts by account name if code is unclear
    - If needed account doesn't exist in Chart of Accounts → use closest alternative
    - NEVER use account codes from your internal knowledge

  ✓ Set template_used = false
  ✓ Explain reasoning in ai_explanation

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
  □ Confidence = 99
  □ ai_explanation.account_selection_logic.template_details explains why template was used
  
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
`, string(templatesData))
	}

	// ถ้ามี template → ไม่ส่ง accounts (ประหยัด ~8,000 tokens)
	// ถ้าไม่มี template → ส่ง accounts แบบเดิม
	accountsSection := ""
	if len(documentTemplates) == 0 {
		accountsSection = fmt.Sprintf(`ข้อมูลหลัก - ผังบัญชี:
%s

`, string(accountsJSON))
	}

	return fmt.Sprintf(`%s%sข้อมูลหลัก - สมุดรายวัน:
%s

ข้อมูลหลัก - เจ้าหนี้ (Creditors - ใช้เมื่อเราเป็นผู้ซื้อ):
%s

ข้อมูลหลัก - ลูกหนี้ (Debtors - ใช้เมื่อเราเป็นผู้ขาย):
%s%s`, accountsSection, businessContext, string(journalBooksJSON), string(creditorsJSON), string(debtorsJSON), templatesSection)
}

// ============================================================================
// 📋 SECTION 3: ANALYSIS RULES
// ============================================================================

const analysisRules = `⚠️ หกรการวิเคราะห์ที่สำคัญ:

⚡ ความยืดหยุ่นในประเภทเอกสาร (สำคัญ):
- รับเอกสารทางบัญชีทุกประเภท: ใบเสร็จ, ใบกำกับภาษี, บิลค่าสาธารณูปโภค, ค่าธรรมเนียมราชการ, ใบแจ้งหนี้
- ไม่ใช่ทุกเอกสารจะมีรายการสินค้า - ปรับวิธีการวิเคราะห์ตามความเหมาะสม
- มุ่งเน้น: จำนวนเงิน, วันที่, ผู้จ่าย/ผู้รับ, วัตถุประสงค์ของรายการ
- ใช้บริบทของเอกสารเพื่อกำหนดรหัสบัญชีที่เหมาะสม

⚡ การใช้บริบทธุรกิจ (สำคัญมาก):
- **อ่านบริบทธุรกิจด้านบนอย่างละเอียด** เพื่อเข้าใจว่าธุรกิจนี้ทำอะไร
- ใช้ประเภทธุรกิจเพื่อเลือกบัญชีที่เหมาะสม:
  * อาหารและเครื่องดื่ม → วัตถุดิบ = ต้นทุนขาย (วัตถุดิบอาหาร/เครื่องดื่ม)
  * ค้าปลีก → บัญชีสินค้าคงคลัง
  * บริการ → ต้นทุนบริการ
- จับคู่คำอธิบายผู้ขาย/สินค้ากับค่าใช้จ่ายทั่วไปของธุรกิจ
- ตัวอย่าง: "ไก่ทอด" ในร้านอาหาร → ต้นทุนอาหาร ไม่ใช่ วัสดุสิ้นเปลือง

⚡ หลักการจัดประเภทบัญชีแบบนักบัญชีไทย (🇹🇭 สำคัญมาก!):
คุณคือนักบัญชีไทยมืออาชีพที่เข้าใจการจัดประเภทค่าใช้จ่ายตามมาตรฐานไทย ต้องแยกแยะอย่างชัดเจนระหว่าง:

📌 **1. ค่าบริการ/ค่าที่ปรึกษา/ค่าธรรมเนียมวิชาชีพ (Professional Service Fees)**
   ลักษณะ: การรับบริการจากผู้เชี่ยวชาญหรือผู้ให้บริการวิชาชีพ (ไม่มีตัวตน)
   
   ใช้เมื่อ:
   - รับบริการที่ปรึกษา (กฎหมาย, บัญชี, ภาษี, ธุรกิจ)
   - ค่าธรรมเนียมวิชาชีพ (ทนาย, สถาปนิก, วิศวกร)
   - ค่าจ้างบุคคลภายนอกทำงาน (outsourcing)
   - บริการดูแลระบบ, บริการซ่อม
   
   คำสำคัญในเอกสาร:
   ✅ "ค่าทำบัญชี", "ค่าทนายความ", "ค่าออกแบบ", "ค่าที่ปรึกษา"
   ✅ "บริการ...", "งานจ้าง...", "ค่าธรรมเนียม..."
   
   วิธีค้นหาบัญชี:
   → ค้นหาจาก Chart of Accounts ที่มีคำว่า "ที่ปรึกษา", "ธรรมเนียม", "บริการ", "จ้าง"
   → มักอยู่ในกลุ่ม 533XXX หรือ 534XXX (แต่ขึ้นกับผังบัญชีของแต่ละธุรกิจ)
   
   ⚠️ สิ่งที่ไม่ใช่บริการวิชาชีพ:
   ❌ การซื้อวัสดุ อุปกรณ์ อะไหล่ → นี่ไม่ใช่บริการ!

📌 **2. ค่าวัสดุ/สินค้า/วัตถุดิบ (Materials, Supplies & Goods)**
   ลักษณะ: การซื้อสิ่งของที่มีตัวตนจับต้องได้
   
   ใช้เมื่อ:
   - ซื้อวัสดุอุปกรณ์ต่างๆ (อะไหล่, ชิ้นส่วน, เครื่องมือ)
   - วัสดุก่อสร้าง, วัสดุซ่อมแซม, วัสดุบำรุงรักษา
   - วัสดุสำนักงาน (กระดาษ, ดินสอ, แฟ้ม)
   - สินค้าที่ไม่ใช่สินค้าหลักของธุรกิจ
   
   คำสำคัญในเอกสาร:
   ✅ ชื่อสินค้าที่เป็นของจับต้องได้ (อะไหล่, วัสดุ, ชิ้นส่วน)
   ✅ "ซื้อ...", "วัสดุ...", "อุปกรณ์..."
   
   วิธีค้นหาบัญชี:
   → ค้นหาจาก Chart of Accounts ที่มีคำว่า "วัสดุ", "อุปกรณ์", "ซ่อม", "บำรุง"
   → มักอยู่ในกลุ่ม 535XXX (แต่ขึ้นกับผังบัญชีของแต่ละธุรกิจ)

📌 **3. ค่าเบ็ดเตล็ด/ค่าใช้จ่ายเบ็ดเตล็ด (Miscellaneous Expenses)**
   ใช้เป็น "บัญชีรองรับทั่วไป" เมื่อ:
   - ค่าใช้จ่ายจำนวนเล็กน้อยที่ไม่มีหมวดเฉพาะ
   - รายการวัสดุหลายชนิดปะปนกัน
   - ไม่สามารถจัดประเภทได้ชัดเจน
   
   ⚠️ หลักการใช้งาน:
   - ใช้เป็น "ที่พักชั่วคราว" ถ้าไม่แน่ใจ
   - ปลอดภัยกว่าเลือกบัญชีที่ผิดพลาด
   
   วิธีค้นหาบัญชี:
   → ค้นหาจาก Chart of Accounts ที่มีคำว่า "เบ็ดเตล็ด", "อื่นๆ", "ทั่วไป"

📌 **4. ค่าสาธารณูปโภค (Utilities)**
   ใช้เฉพาะ:
   ✅ "ค่าไฟฟ้า", "ค่าน้ำประปา", "ค่าโทรศัพท์", "ค่าอินเทอร์เน็ต"
   
   วิธีค้นหาบัญชี:
   → ค้นหาจาก Chart of Accounts ที่มีคำว่า "ไฟฟ้า", "น้ำ", "โทรศัพท์", "สาธารณูปโภค"
   → มักอยู่ในกลุ่ม 531XXX (แต่ขึ้นกับผังบัญชีของแต่ละธุรกิจ)

📌 **5. ค่าน้ำมัน/เชื้อเพลิง (Fuel)**
   ใช้เฉพาะ:
   ✅ "น้ำมันเบนซิน", "ดีเซล", "แก๊ส NGV"
   ❌ "น้ำมันเครื่อง" → ไม่ใช่เชื้อเพลิง → ค้นหาบัญชีวัสดุบำรุงรักษาแทน
   
   วิธีค้นหาบัญชี:
   → ค้นหาจาก Chart of Accounts ที่มีคำว่า "น้ำมัน", "เชื้อเพลิง"

🎯 วิธีการตัดสินใจอย่างถูกต้อง:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

ขั้นตอนที่ 1: อ่านรายละเอียดสินค้า/บริการจากเอกสาร

ขั้นตอนที่ 2: ถามตัวเองว่า "นี่เป็นการซื้อสิ่งของ หรือการรับบริการ?"
  
  A. ถ้าซื้อสิ่งของที่จับต้องได้ (มีตัวตน):
     → ค้นหาบัญชีที่เกี่ยวกับ "วัสดุ", "อุปกรณ์", "ซ่อม", "บำรุง"
     → หรือบัญชี "เบ็ดเตล็ด" ถ้าไม่แน่ใจ
     → หรือบัญชี "สินค้าคงคลัง" ถ้าเป็นสินค้าหลักของธุรกิจ
  
  B. ถ้ารับบริการจากผู้ให้บริการ (ไม่มีตัวตน):
     → ค้นหาบัญชีที่เกี่ยวกับ "ที่ปรึกษา", "บริการ", "ธรรมเนียม", "จ้าง"

ขั้นตอนที่ 3: ตรวจสอบชื่อผู้ขายเพิ่มเติม
  - "ร้าน...", "บริษัทขาย...", "ห้าง..." → มักขายสินค้า → ค้นหาบัญชีวัสดุ
  - "สำนักงาน...", "สำนัก...", "บริษัทที่ปรึกษา..." → บริการ → ค้นหาบัญชีบริการ

ขั้นตอนที่ 4: ค้นหาจาก Chart of Accounts ที่ได้รับมา
  - **ห้าม** ใช้รหัสบัญชีจากความรู้ของคุณ
  - **ต้อง** ค้นหาจาก Chart of Accounts ที่ระบบส่งให้เท่านั้น
  - แต่ละธุรกิจใช้ผังบัญชีไม่เหมือนกัน

ขั้นตอนที่ 5: ถ้ายังไม่แน่ใจ
  → ค้นหาบัญชีที่มีคำว่า "เบ็ดเตล็ด" หรือ "อื่นๆ" หรือ "ทั่วไป"

🚨 กรณีตัวอย่างการคิดวิเคราะห์:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

เอกสาร: "ร้าน ABC - วัสดุอุปกรณ์หลายรายการ ราคา 2,000 บาท"
🔍 วิเคราะห์: 
   - "วัสดุอุปกรณ์" = สิ่งของที่จับต้องได้ (ไม่ใช่บริการ)
   - "ร้าน" = มักขายสินค้า
   → ค้นหาบัญชีที่มีคำว่า "วัสดุ" หรือ "เบ็ดเตล็ด" จาก Chart of Accounts
   ❌ ห้ามเลือกบัญชีที่เกี่ยวกับ "บริการ" หรือ "ที่ปรึกษา"

เอกสาร: "สำนักงานบัญชี XYZ - ค่าทำบัญชีประจำเดือน 3,000 บาท"
🔍 วิเคราะห์:
   - "ค่าทำบัญชี" = บริการวิชาชีพ (ไม่มีตัวตน)
   - "สำนักงานบัญชี" = ผู้ให้บริการ
   → ค้นหาบัญชีที่มีคำว่า "ที่ปรึกษา" หรือ "บัญชี" หรือ "ธรรมเนียม" จาก Chart of Accounts

เอกสาร: "ร้านฮาร์ดแวร์ - เครื่องมือช่าง 500 บาท"
🔍 วิเคราะห์:
   - "เครื่องมือช่าง" = สิ่งของที่จับต้องได้
   - "ร้านฮาร์ดแวร์" = ขายสินค้า
   → ค้นหาบัญชีที่มีคำว่า "วัสดุ" หรือ "อุปกรณ์" หรือ "เบ็ดเตล็ด" จาก Chart of Accounts

⚡ นโยบายความมั่นใจแบบเข้มงวด (บังคับ):
- ถ้ารายละเอียดสินค้า/รายการ ไม่ชัดเจน → ใช้ "Unknown Vendor" (อย่าเดา!) พร้อมตั้ง requires_review = true
- ถ้าการเลือกบัญชีมีความมั่นใจ < 85% → ค้นหาบัญชี "เบ็ดเตล็ด" จาก Chart of Accounts
- ข้อความลายมือ มีความเสี่ยง → ลดความมั่นใจ 20%, ตั้งค่า requires_review = true
- ใช้บัญชีปลอดภัยดีกว่าบัญชีที่ผิด
- อย่าเดารหัสบัญชีจากคำอธิบายที่ไม่ชัดเจน
- ความมั่นใจโดยรวม < 70% จะถูกปฏิเสธ
- **แต่**: ถ้าบริบทธุรกิจระบุชัดเจนถึงประเภทค่าใช้จ่าย ให้ใช้ความรู้นั้นด้วยความมั่นใจที่สูงขึ้น

⚡ การตอบกลับ - สั้น กระชับ ได้ใจความ (บังคับ):
- **ใช้ภาษาไทยทั้งหมด** - ห้ามใช้อังกฤษใน ai_explanation
- **reason_for_selection** - 1 ประโยคสั้นๆ (ไม่เกิน 20 คำ) เช่น "ซื้อวัสดุ ใช้บัญชีค่าเบ็ดเตล็ด"
- **reasoning** - สรุปสั้นๆ 2-3 ประโยค (ไม่เกิน 50 คำ) เช่น "ใบกำกับภาษี ซื้อวัสดุจาก ABC ยอด 5,000 บาท ชำระเงินสด"
- **ห้ามอธิบายซ้ำซ้อน** - ถ้าบอกแล้วใน reason_for_selection ไม่ต้องทำซ้ำใน reasoning
- **ตรงประเด็น** - บอกแค่ข้อมูลสำคัญ: ประเภทเอกสาร, ผู้ขาย, ยอดเงิน, วิธีชำระ`

// ============================================================================
// 📋 SECTION 4: MULTI-IMAGE PROCESSING STEPS
// ============================================================================

const multiImageSteps = `🔍 ขั้นตอนที่ 1: กำหนดความสัมพันธ์ของเอกสาร

วิเคราะห์รูปภาพและจำแนกความสัมพันธ์:

A. **เอกสาร + หลักฐานการชำระเงิน** (เอกสาร + สลิปโอนเงิน):
   - รูปที่ 1: เอกสารทางบัญชีใดก็ได้ (ใบเสร็จ, ใบกำกับภาษี, ใบแจ้งหนี้, บิลค่าสาธารณูปโภค)
   - รูปที่ 2: สลิปโอนเงินหรือหลักฐานการชำระเงิน
   - จำนวนเงินเท่ากันหรือไม่? ✓ → เป็นเอกสารเดียวกัน
   - การดำเนินการ: ใช้ข้อมูลจากเอกสาร + วิธีชำระเงินจากสลิป

B. **ใบเสร็จหลายหน้า**:
   - เลขที่ใบเสร็จเดียวกันในทุกรูป
   - ผู้ขายเดียวกัน, วันที่เดียวกัน
   - มีเลขหน้าหรือสัญลักษณ์ต่อเนื่อง
   - การดำเนินการ: รวมรายการทั้งหมด, รวมยอดรวม

C. **ใบเสร็จแยกกัน**:
   - เลขที่ใบเสร็จต่างกัน
   - ผู้ขายหรือวันที่ต่างกัน
   - การดำเนินการ: สร้างรายการแยกสำหรับแต่ละใบ

🧠 ขั้นตอนที่ 2: ตรรกะการรวมข้อมูลอย่างชาญฉลาด

สำหรับ ใบเสร็จ + สลิปการชำระเงิน:
1. ใช้รายละเอียดสินค้าจากใบเสร็จ
2. ใช้วิธีชำระเงินจากสลิป:
   - ถ้าสลิปแสดงการโอนเงิน → 111100 (เงินฝากธนาคาร)
   - ถ้าแสดงการชำระ QR → 111100 (เงินฝากธนาคาร)
   - ถ้าแสดงการฝากเงินสด → 111110 (เงินสดในมือ)
3. ตรวจสอบจำนวนเงินตรงกัน (ผิดเพี้ยนได้: 0.01 บาท)
4. ใช้ข้อมูลผู้ขายจากใบเสร็จ

สำหรับ ใบเสร็จหลายหน้า:
1. รวมรายการสินค้าทั้งหมด
2. รวมจำนวนและราคา
3. เก็บเลขที่ใบเสร็จเดียว
4. ใช้วิธีชำระเงินจากหน้าใดที่แสดง

สำหรับ ใบเสร็จแยกกัน:
1. นี่เกิดขึ้นไม่บ่อย - โดยปกติรูปภาพจะเกี่ยวข้องกัน
2. ถ้าแยกจริง → รายงานรายการที่มีความมั่นใจสูงสุด
3. เพิ่มหมายเหตุเกี่ยวกับใบเสร็จอื่นที่ตรวจพบ

📊 ขั้นตอนที่ 3: สร้างรายการบัญชีที่รวมแล้ว

ปฏิบัติตามหลักการบัญชีที่มีอยู่:
- ตรวจจับประเภทเอกสารไทย (ใบเสร็จรับเงิน = ชำระแล้ว)
- เลือกบัญชีอย่างชาญฉลาดตามประเภทรายการ
- นโยบาย Unknown Vendor สำหรับข้อมูลที่หายไป (พร้อมตั้ง requires_review = true)
- ตรวจสอบบัญชีคู่`

// ============================================================================
// 📋 SECTION 5: OUTPUT FORMAT (JSON SCHEMA)
// ============================================================================

const outputFormatJSON = `🎨 OUTPUT FORMAT (JSON):

{
  "document_analysis": {
    "total_images": "[จำนวนรูป]",
    "relationship": "[receipt_with_payment_proof/multi_page_receipt/separate_receipts/single_document]",
    "confidence": "[คะแนนความมั่นใจ]",
    "analysis_notes": "[บันทึกการวิเคราะห์]"
  },
  "source_images": [
    {
      "image_index": "[ลำดับรูป]",
      "type": "[receipt/invoice/payment_slip/tax_invoice/unknown]",
      "receipt_number": "[เลขที่]",
      "amount": "[จำนวนเงิน]",
      "date": "[วันที่]",
      "confidence": "[คะแนน]"
    }
    // ... รูปอื่นๆ ถ้ามี
  ],
  "receipt": {
    "number": "[เลขที่ใบเสร็จ]",
    "date": "[วันที่]",
    "vendor_name": "[ชื่อผู้ขาย - ค้นหาจาก OCR]",
    "vendor_tax_id": "[เลขผู้เสียภาษี หรือ Unknown Vendor ถ้าไม่มี]",
    "total": "[ยอดรวม]",
    "vat": "[ยอด VAT]",
    "payment_method": "[วิธีชำระเงิน]",
    "payment_proof_available": "[true/false]"
  },
  "creditor": {
    "creditor_code": "[ค้นหาจาก Creditors - ถ้าเราเป็นผู้ซื้อ]",
    "creditor_name": "[ชื่อที่ตรงกัน - ถ้าหาไม่เจอใช้ Unknown Vendor]"
  },
  "debtor": {
    "debtor_code": "[ค้นหาจาก Debtors - ถ้าเราเป็นผู้ขาย]",
    "debtor_name": "[ชื่อลูกค้า - ถ้าหาไม่เจอใช้ Unknown Customer]"
  },
  "accounting_entry": {
    "document_date": "[วันที่เอกสาร]",
    "reference_number": "[เลขที่อ้างอิง]",
    "journal_book_code": "[รหัสสมุด - ถ้าใช้ template ให้ใช้จาก template.bookcode หรือ template.module / ถ้าไม่ใช้ template ให้ค้นหาจาก Journal Books]",
    "journal_book_name": "[ชื่อสมุด - ค้นหาจาก Journal Books ด้วย journal_book_code]",
    "creditor_code": "[รหัส - ถ้าเราเป็นผู้ซื้อ / ว่างถ้าเป็นผู้ขาย]",
    "creditor_name": "[ชื่อ - ถ้าเราเป็นผู้ซื้อ / ว่างถ้าเป็นผู้ขาย]",
    "debtor_code": "[รหัส - ถ้าเราเป็นผู้ขาย / ว่างถ้าเป็นผู้ซื้อ]",
    "debtor_name": "[ชื่อ - ถ้าเราเป็นผู้ขาย / ว่างถ้าเป็นผู้ซื้อ]",
    "entries": [
      {
        "account_code": "[รหัสบัญชี - ถ้าใช้ template ให้ใช้จาก template.details[].accountcode / ถ้าไม่ใช้ template ให้ค้นหาจาก Chart of Accounts]",
        "account_name": "[ชื่อบัญชี - ถ้าใช้ template ให้ใช้จาก template.details[].detail / ถ้าไม่ใช้ template ให้ใช้จาก Chart of Accounts]",
        "debit": "[จำนวนเงิน Debit]",
        "credit": "[จำนวนเงิน Credit]",
        "description": "[คำอธิบาย]"
      }
      // ... ถ้าใช้ template ต้องมีครบทุก account ใน template.details[] / ถ้าไม่ใช้ template ให้สร้างตามความเหมาะสม
    ],
    "balance_check": {
      "balanced": "[true if total_debit == total_credit, else false]",
      "total_debit": "[Sum of all debit amounts from entries[]]",
      "total_credit": "[Sum of all credit amounts from entries[]]"
    }
  },
  "validation": {
    "confidence": {
      "level": "[high/medium/low]",
      "score": "[คะแนน 0-100]"
    },
    "requires_review": "[true/false]",
    "fields_requiring_review": "[array ของ field ที่ต้อง review]",
    "processing_notes": "[หมายเหตุ]",
    "ai_explanation": {
      "reasoning": "[อธิบายเหตุผลการตัดสินใจทุกขั้นตอนอย่างละเอียด]",
      "evidence_from_receipt": "[ระบุหลักฐานที่ใช้ตัดสินใจ: เลขที่ใบเสร็จ, วันที่, ชื่อผู้ออกเอกสาร, รายการ, ยอดเงิน]",
      "vendor_matching": {
        "found_in_document": "[ชื่อที่พบในเอกสาร - จาก vendor_name หรือ raw_document_text]",
        "matched_with": "[code และชื่อที่จับคู่ได้จาก Creditors/Debtors]",
        "matching_method": "[วิธีการจับคู่: exact_match / fuzzy_match / tax_id_match / not_found]",
        "confidence": "[ความมั่นใจในการจับคู่ 0-100 - ใช้ fuzzy matching ยอมรับ ≥70%]",
        "reason": "[เหตุผลที่เลือก creditor/debtor นี้ - ระบุว่าคำสำคัญตรงกันที่ส่วนไหน, ไม่สนใจตัวสะกดเล็กน้อย]"
      },
      "transaction_analysis": {
        "type": "[purchase_for_use / sale_of_service / expense / revenue]",
        "buyer_seller_determination": "[อธิบายว่าทำไมถึงรู้ว่าเราเป็นผู้ซื้อหรือผู้ขาย - เปรียบเทียบชื่อในเอกสารกับชื่อบริษัทเรา]",
        "payment_method": "[วิธีชำระเงิน]",
        "has_vat": "[true/false]",
        "payment_proof": "[มีหลักฐานหรือไม่]"
      },
      "account_selection_logic": {
        "template_used": "[true/false]",
        "template_details": "[ถ้า template_used=true: ระบุชื่อ template และรายชื่อบัญชีที่ใช้จาก template]",
        "debit_accounts": "[
          {
            \"account_code\": \"[รหัส]\",
            \"account_name\": \"[ชื่อบัญชี]\",
            \"amount\": [จำนวน],
            \"reason_for_selection\": \"[เหตุผลสั้นๆ ภาษาไทย 1 ประโยค]\"
          }
        ]",
        "credit_accounts": "[
          {
            \"account_code\": \"[รหัส]\",
            \"account_name\": \"[ชื่อบัญชี]\",
            \"amount\": [จำนวน],
            \"reason_for_selection\": \"[เหตุผลสั้นๆ ภาษาไทย 1 ประโยค]\"
          }
        ]",
        "verification": "[ยืนยันว่า Debit = Credit และใช้บัญชีจาก Master Data เท่านั้น]"
      },
      "risk_assessment": {
        "overall_risk": "[low/medium/high]",
        "factors": "[ปัจจัยความเสี่ยงสั้นๆ]",
        "recommendations": "[คำแนะนำสั้นๆ]"
      }
    }
  }
}

⚠️ สำคัญมาก - ภาษาและความกระชับ:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1. **ใช้ภาษาไทยทั้งหมดใน ai_explanation** - ห้ามใช้อังกฤษ
2. **reason_for_selection** - 1 ประโยคสั้นๆ ได้ใจความ (ไม่เกิน 20 คำ)
3. **reasoning** - 2-3 ประโยคสั้นๆ สรุปใจความสำคัญ (ไม่เกิน 50 คำ)
4. **ไม่ต้องอธิบายซ้ำซ้อน** - ถ้าใน reason_for_selection บอกแล้ว ไม่ต้องทำซ้ำใน reasoning

ตัวอย่างที่ดี:
✅ "reason_for_selection": "ซื้อวัสดุอุปกรณ์ ใช้บัญชีค่าเบ็ดเตล็ด"
✅ "reasoning": "เอกสารเป็นใบกำกับภาษี ซื้อวัสดุจาก Grey Matter ยอด 4,625 บาท ชำระเงินสด"

ตัวอย่างที่ไม่ดี:
❌ "reason_for_selection": "Transaction is a purchase of goods/services, and no specific template matched. 'ค่าเบ็ดเตล็ด' is a general expense account. Base amount from receipt."
❌ "reasoning": "เอกสารที่ได้รับเป็นใบกำกับภาษีและใบส่งสินค้าจาก 'บริษัท เกรซ แมทเทอร์ จำกัด' ถึง 'บจก.วี.วี.แมน' (ซึ่งคือบริษัทของเราตามบริบทธุรกิจ) โดยมีรายการสินค้า..." (ยาวเกินไป)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
⚠️ VALIDATION REQUIREMENTS (ข้อกำหนดการตรวจสอบ)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

1. **Balance Check (ตรวจสอบยอดคงเหลือ)**:
   Sum Total Debit and Total Credit from all entry amounts (ผลรวมจาก entries[] เท่านั้น)
   Balance is NOT required - document errors should be visible to users
   DO NOT calculate or adjust amounts to force balance

2. **Template Compliance (การปฏิบัติตาม Template)**:
   If template_used = true:
   ✓ All accounts in entries[] MUST come from template.details[]
   ✓ Account count MUST match template (template has 2 → output has 2)
   ✓ NO tax accounts unless they exist in template.details[]
   ✓ Verify in ai_explanation.account_selection_logic.verification

3. **Account Codes (รหัสบัญชี)** - CRITICAL RULE:
   ⚠️ EVERY account code MUST exist in the provided Master Data:

   If template_used = true:
   - Use ONLY codes from template.details[] (template already validated against Master Data)

   If template_used = false:
   - Search for account in Chart of Accounts by name/description
   - VERIFY code exists in provided Chart of Accounts list
   - NEVER use codes from your internal knowledge
   - Each shop has different chart of accounts with different codes
   - If needed account doesn't exist → use closest alternative from Chart of Accounts

4. **Journal Book (สมุดรายวัน)** - ⚠️ สำคัญมาก:

   🔴 **กฎสูงสุด: ถ้ามี VAT → ห้ามใช้สมุดทั่วไป!**

   **วิธีเลือกสมุดรายวัน (ตามลำดับความสำคัญ):**

   **Priority 1 - เอกสารซื้อ (เราเป็นผู้ซื้อ):**
   - เงื่อนไข: มี VAT หรือ ภาษีซื้อ
   - ประเภท: ค่าบริการ, ค่าทำบัญชี, ซื้อสินค้า, ค่าเช่า, ค่าใช้จ่ายทั่วไป
   - **ต้องใช้:** ค้นหาสมุดที่มีคำว่า "ซื้อ" หรือ "จ่าย"
   - ตัวอย่างชื่อ: "สมุดรายวันซื้อ", "สมุดรายวันจ่าย", "Purchase Journal"

   **Priority 2 - เอกสารขาย (เราเป็นผู้ขาย):**
   - เงื่อนไข: มี VAT หรือ ภาษีขาย
   - ประเภท: ขายสินค้า, ขายบริการ, รับเงิน
   - **ต้องใช้:** ค้นหาสมุดที่มีคำว่า "ขาย" หรือ "รับ"
   - ตัวอย่างชื่อ: "สมุดรายวันขาย", "สมุดรายวันรับ", "Sales Journal"

   **Priority 3 - เอกสารธนาคาร:**
   - เงื่อนไข: มีรายการฝาก/ถอน/โอนเงิน ผ่านธนาคาร
   - **ต้องใช้:** ค้นหาสมุดที่มีคำว่า "ธนาคาร" หรือ "เงินฝาก"
   - ตัวอย่างชื่อ: "สมุดเงินฝากธนาคาร", "Bank Journal"

   **Priority 4 - เอกสารทั่วไป (ใช้เมื่ออื่นไม่เข้าข่าย):**
   - เงื่อนไข: **ไม่มี VAT** + ไม่ใช่ซื้อ-ขาย + ไม่เกี่ยวกับธนาคาร
   - ประเภท: บันทึกปรับปรุง, โอนย้าย, ปิดบัญชี, เบิกถอน
   - **ต้องใช้:** ค้นหาสมุดที่มีคำว่า "ทั่วไป"
   - ตัวอย่างชื่อ: "สมุดรายวันทั่วไป", "General Journal"

   📋 **ตัวอย่างการเลือก:**

   ✅ **ถูกต้อง:**
   - ใบเสร็จค่าทำบัญชี + มี VAT 140 บาท
     → เราเป็นผู้ซื้อ → ใช้ "สมุดรายวันซื้อ" (02) ✅

   - ใบเสร็จขายสินค้า + มี VAT
     → เราเป็นผู้ขาย → ใช้ "สมุดรายวันขาย" (03) ✅

   - สลิปถอนเงินจากธนาคาร
     → เกี่ยวกับธนาคาร → ใช้ "สมุดเงินฝากธนาคาร" (05) ✅

   - บันทึกปรับปรุงยอดเปิด + ไม่มี VAT
     → ไม่ใช่ซื้อ-ขาย → ใช้ "สมุดรายวันทั่วไป" (01) ✅

   ❌ **ผิด (ห้ามทำ):**
   - ใบเสร็จค่าทำบัญชี + มี VAT
     → ใช้ "สมุดรายวันทั่วไป" ❌ **ผิด!** (มี VAT ต้องใช้สมุดซื้อ)

   - ใบเสร็จซื้อสินค้า + มี VAT
     → ใช้ "สมุดรายวันทั่วไป" ❌ **ผิด!** (มี VAT ต้องใช้สมุดซื้อ)

   ⚠️ **ข้อห้าม:**
   - **ห้าม** ใช้รหัสที่ไม่มีใน Master Data (เช่น "GL", "JV", "PJ")
   - **ห้าม** ใช้สมุดทั่วไปเมื่อมี VAT
   - **ห้าม** เดาหรือสุ่มเลือก

   📌 **ขั้นตอนการตัดสินใจ:**
   1. ✅ เช็คก่อน: เอกสารมี VAT หรือไม่?
   2. ✅ ถ้ามี VAT → เราเป็นผู้ซื้อหรือผู้ขาย?
   3. ✅ ผู้ซื้อ → ค้นหาสมุดที่มีคำว่า "ซื้อ" หรือ "จ่าย"
   4. ✅ ผู้ขาย → ค้นหาสมุดที่มีคำว่า "ขาย" หรือ "รับ"
   5. ✅ ถ้าไม่มี VAT และไม่ใช่ซื้อ-ขาย → ใช้ "ทั่วไป"

5. **Creditor/Debtor (เจ้าหนี้/ลูกหนี้)** - MUST fill both fields:
   - If we are buyer → Fill creditor_code/creditor_name (from Creditors or "Unknown Vendor") + Leave debtor fields empty
   - If we are seller → Fill debtor_code/debtor_name (from Debtors or "Unknown Customer") + Leave creditor fields empty
   - In accounting_entry: MUST have all 4 fields (creditor_code, creditor_name, debtor_code, debtor_name) with "" for unused
   - If not found and using "Unknown Vendor/Customer" → Set requires_review = true

6. **Confidence Score (คะแนนความมั่นใจ)**:
   Be honest - Low confidence → requires_review = true
   Template matched → confidence = 99

7. **Language - ภาษาไทยเท่านั้น (บังคับ)**:
   ⚠️ **ใช้ภาษาไทยทั้งหมดใน ai_explanation** - ห้ามใช้อังกฤษ
   - reasoning → ภาษาไทย
   - reason_for_selection → ภาษาไทย
   - factors → ภาษาไทย
   - recommendations → ภาษาไทย
   - buyer_seller_determination → ภาษาไทย
   - reason (ใน vendor_matching) → ภาษาไทย
   - verification → ภาษาไทย
   - **ทุกฟิลด์ใน ai_explanation ต้องเป็นภาษาไทยทั้งหมด**
   
   ตัวอย่างที่ถูกต้อง:
   ✅ "factors": "เอกสารชัดเจน template ตรงกัน บัญชีสมดุล"
   ✅ "reason": "ไม่พบผู้ขายในรายการ Creditors จึงใช้ Unknown Vendor"
   
   ตัวอย่างที่ผิด:
   ❌ "factors": "Document is clear, template matched"
   ❌ "reason": "Vendor not found in provided creditor list"`

// ============================================================================
// 📋 SECTION 6: ADDITIONAL GUIDELINES
// ============================================================================

const additionalGuidelines = `📌 แนวทางปฏิบัติทางบัญชีเพิ่มเติม:

💰 การตรวจจับวิธีการชำระเงิน:
- "โอนเงิน", "transfer", มี QR code → ค้นหาบัญชี "เงินฝากธนาคาร" จาก Chart of Accounts
- "เงินสด", "CASH", ไม่มีข้อมูลการโอน → ค้นหาบัญชี "เงินสดในมือ" จาก Chart of Accounts
- ⚠️ ถ้ามีสลิปการชำระเงิน → ใช้ข้อมูลจากสลิป (ความมั่นใจสูงสุด!)

🏢 การจับคู่ผู้ขาย/เจ้าหนี้/ลูกหนี้ (⚠️ สำคัญมาก!):

**ขั้นตอนที่ 0: ระบุชื่อผู้ออกเอกสาร (⚠️ ทำก่อนทุกอย่าง)**
📍 หาชื่อผู้ออกเอกสารจากตำแหน่งเหล่านี้ (เรียงตามความน่าเชื่อถือ):

1. **Header/ส่วนบน** (ความน่าเชื่อถือสูงสุด):
   - มักเป็นชื่อตัวใหญ่/ตัวหนาบนสุดของเอกสาร
   - หาคำว่า: "บริษัท", "ห้างหุ้นส่วน", "หจก.", "บจก.", "ร้าน"
   - ตัวอย่าง: "บริษัท ซีแอนด์ฮิล จำกัด", "หจก.นิธิบุญ"

2. **เลขประจำตัวผู้เสียภาษี**:
   - หาคำว่า: "เลขประจำตัวผู้เสียภาษี", "Tax ID", "เลขที่ผู้เสียภาษี"
   - ชื่อมักอยู่ใกล้เลขนี้

3. **ชื่อพร้อมที่อยู่ในส่วนบน**:
   - ถ้ามีที่อยู่ยาวๆ ในส่วนบน → ชื่อมักอยู่บรรทัดแรกของที่อยู่นั้น

4. **Footer/ตีนเอกสาร**:
   - บางใบเสร็จใส่ชื่อด้านล่าง (แต่น่าเชื่อถือน้อยกว่า)

⚠️ สิ่งที่ไม่ใช่ชื่อผู้ออก:
- ชื่อสินค้า/บริการ (เช่น "งานจัดทำบัญชี")
- ชื่อพนักงานขาย
- ชื่อที่อยู่ส่วนล่าง (มักเป็นสาขา/จุดบริการ)

**ขั้นตอนที่ 0 (ต้องทำก่อนทุกอย่าง): เช็คว่าผู้ออกเอกสารเป็นเราเองหรือไม่**
🚨 CRITICAL - เช็คนี้ก่อนทุกอย่าง:
- ดึงคำสำคัญจากชื่อผู้ออกเอกสาร (vendor_name)
- เทียบกับชื่อบริษัทเราทุกชื่อใน names[].name
- ถ้า**คำสำคัญตรงกัน** → **ผู้ออกคือเราเอง**:
  * ข้าม Creditors ทั้งหมด!
  * ไปใช้ Debtors (ลูกหนี้) แทน
  * ระบุ vendor_matching.matched_with = null
  * ระบุ vendor_matching.reason = "ผู้ขายคือบริษัทเราเอง - เอกสารภายในหรือเราเป็นผู้ขาย"
  * ตัวอย่าง: vendor_name="หจก.นิธิบุญ" + names[0].name="หจก.นิธิบุญ" → ตรงกัน!
- ถ้า**ไม่ตรงกัน** → ไปขั้นตอนที่ 1

**ขั้นตอนที่ 1: ระบุว่าเราเป็นผู้ซื้อหรือผู้ขาย (ถ้า Step 0 ไม่ตรง)**
- ใช้ชื่อผู้ออกเอกสารที่หาได้จากขั้นตอน 0
- เปรียบเทียบกับชื่อบริษัทของเรา (ดูใน names[].name ในบริบทธุรกิจข้างบน)
- ใช้การจับคู่คำสำคัญ (เช่น "นิธิบุญ" ตรงกับ "หจก.นิธิบุญ")
- ถ้าชื่อผู้ออก **ตรงกับชื่อบริษัทเรา** → เราเป็นผู้ขาย → ใช้ Debtors (ลูกหนี้)
- ถ้าชื่อผู้ออก **ไม่ตรงกับชื่อบริษัทเรา** → เราเป็นผู้ซื้อ → ใช้ Creditors (เจ้าหนี้)
- ถ้า**ไม่เห็นชื่อผู้ออกเลย** → ดู template หรือ account type (5xxxxx=รายจ่าย, 4xxxxx=รายรับ)

**ขั้นตอนที่ 2: จับคู่กับรายการที่ถูกต้อง**
- สำหรับเจ้าหนี้: ค้นหาในรายการ Creditors ที่ตรงกับชื่อผู้ขาย
- สำหรับลูกหนี้: ค้นหาในรายการ Debtors ที่ตรงกับชื่อผู้ซื้อ/ลูกค้า
- ใช้การจับคู่แบบคลุมเครือ:
  - ชื่อบางส่วน (เช่น "สยาม" ใน "บริษัท สยามพาณิชย์ จำกัด")
  - การสะกดคล้ายกัน
  - เทียบเลขผู้เสียภาษีถ้ามี
- ถ้าหาไม่เจอ → ใช้ "Unknown Vendor" และตั้ง requires_review = true
- ถ้าเป็นหน่วยราชการ/สาธารณูปโภค → อาจมีรหัสเจ้าหนี้เฉพาะ

**ตัวอย่าง:**
- ใบเสร็จแสดงผู้ขาย = "DEMOAccount" → ตรงกับชื่อบริษัทเรา → เราเป็นผู้ขาย → ค้นหาลูกค้าใน Debtors
- ใบเสร็จแสดงผู้ขาย = "ร้านค้าภายนอก" → ไม่ตรงกับชื่อบริษัทเรา → เราเป็นผู้ซื้อ → ค้นหาใน Creditors

📝 การเลือกสมุดรายวัน (ดูกฎละเอียดในข้อ 4 ด้านบน):
🔴 **กฎสำคัญ: ถ้ามี VAT → ห้ามใช้สมุดทั่วไป!**
- มี VAT + เราเป็นผู้ซื้อ → สมุดรายวันซื้อ/จ่าย
- มี VAT + เราเป็นผู้ขาย → สมุดรายวันขาย/รับ
- เกี่ยวกับธนาคาร → สมุดเงินฝากธนาคาร
- ไม่มี VAT + ไม่ใช่ซื้อ-ขาย → สมุดรายวันทั่วไป

🧾 การจัดการภาษีมูลค่าเพิ่ม (VAT):
- ถ้าพบคำว่า "ภาษีมูลค่าเพิ่ม" หรือ "VAT" → ดึงจำนวน VAT ออกมา
- อัตรา VAT ในไทย = 7%
- บัญชี: 221007 (ภาษีซื้อ) สำหรับการซื้อ
- ถ้าไม่มีข้อมูล VAT → vat = 0.00

📅 รูปแบบวันที่:
- รับได้: DD/MM/YYYY, DD/MM/YY (พ.ศ.), DD-MM-YYYY
- แปลงเป็น: DD/MM/YYYY (รูปแบบพุทธศักราช)
- ถ้าปี < 2500 → น่าจะเป็นค.ศ., เพิ่ม 543

🔢 การตรวจสอบจำนวนเงิน:
- ต้องเป็นตัวเลขบวก
- ปัดเศษเป็น 2 ทศนิยม
- ยอดรวม = ยอดย่อย + VAT (ถ้ามี)
- ตรวจสอบ ผลรวม(รายการ) ≈ ยอดรวม (ผิดเพี้ยนได้: 0.50 บาท)

⚠️ กรณีพิเศษ:
- หลายวิธีการชำระเงิน → แยกรายการตามความเหมาะสม
- การชำระบางส่วน → ระบุในคำอธิบาย
- การคืนเงิน/คืนสินค้า → ใช้จำนวนติดลบ, กลับบัญชี
- การวางมัดจำ → ใช้บัญชี "มัดจำ" (ช่วง 1150XX)
- เงินสดย่อย → ใช้ 111150 (เงินสดย่อย)

🎯 คู่มือการให้คะแนนความมั่นใจ:
- 95-100: ชัดเจนสมบูรณ์, ข้อมูลครบ, มีหลักฐานการชำระเงิน
- 85-94: ชัดเจนแต่มีความไม่แน่นอนเล็กน้อย (ลายมือ, มัวบางส่วน)
- 70-84: ความมั่นใจปานกลาง, มีการสันนิษฐานบ้าง, ต้องตรวจสอบ
- ต่ำกว่า 70: ความไม่แน่นอนมากเกินไป, ปฏิเสธหรือขอรูปภาพที่ดีกว่า

🚨 เงื่อนไขการปฏิเสธ (ตั้งค่า requires_review = true):
- จำนวนเงินรวมไม่ชัดเจนหรือหายไป
- ชื่อผู้ขายอ่านไม่ออกเลย
- วันที่หายไปหรือไม่ชัดเจน
- คุณภาพรูปแย่เกินไป
- ข้อมูลขัดแย้งกันระหว่างรูปภาพ
- ผลรวมไม่สมดุล (เกินค่าผิดเพี้ยนที่กำหนด)`

// ============================================================================
// 📋 MAIN PROMPT BUILDER
// ============================================================================

// BuildMultiImageAccountingPrompt creates the complete prompt for multi-image accounting analysis
// Supports conditional master data loading based on template matching
func BuildMultiImageAccountingPrompt(allResultsJSON string, mode MasterDataMode, matchedTemplate *bson.M, accounts []bson.M, journalBooks []bson.M, creditors []bson.M, debtors []bson.M, shopProfile interface{}, documentTemplates []bson.M) string {
	masterData := formatMasterDataWithMode(mode, matchedTemplate, accounts, journalBooks, creditors, debtors, shopProfile, documentTemplates)

	return fmt.Sprintf(`คุณคือนักบัญชีไทยผู้เชี่ยวชาญ วิเคราะห์รูปภาพหลายรูปที่เกี่ยวข้องกัน แล้วสร้างรายการบัญชีเดียวที่รวมแล้ว

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

คืนค่าเฉพาะ JSON ที่ถูกต้องเท่านั้น (ไม่ต้องมี markdown หรือ code blocks).`,
		allResultsJSON,
		masterData,
		analysisRules,
		multiImageSteps,
		outputFormatJSON,
		additionalGuidelines)
}
