// prompt_output_format.go - JSON Output Format Schema
//
// ไฟล์นี้กำหนดรูปแบบ JSON ที่ AI ต้องส่งกลับมา
// รวมถึง Validation Rules และตัวอย่างการใช้งาน

package ai

// GetOutputFormatJSON returns the JSON schema for AI response
// This defines the complete structure that AI must follow
func GetOutputFormatJSON() string {
	return `🎨 OUTPUT FORMAT (JSON):

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
      "date": "[วันที่ในรูปแบบ YYYY-MM-DD - แปลง พ.ศ. เป็น ค.ศ. ด้วยการ -543]",
      "confidence": "[คะแนน]"
    }
  ],
  "receipt": {
    "number": "[เลขที่ใบเสร็จ]",
    "date": "[วันที่ในรูปแบบ YYYY-MM-DD - แปลง พ.ศ. เป็น ค.ศ. ด้วยการ -543]",
    "vendor_name": "[ชื่อผู้ขาย]",
    "vendor_tax_id": "[เลขผู้เสียภาษี]",
    "total": "[ยอดรวม]",
    "vat": "[ยอด VAT ที่ระบุชัดเจนในเอกสาร - ถ้าไม่มีระบุให้ใส่ null - ห้ามคำนวณ]",
    "payment_method": "[วิธีชำระเงิน]",
    "payment_proof_available": "[true/false]"
  },
  "creditor": {
    "creditor_code": "[รหัส - ถ้าเราเป็นผู้ซื้อ / null ถ้าไม่เจอ]",
    "creditor_name": "[ชื่อที่ตรงกัน]"
  },
  "debtor": {
    "debtor_code": "[รหัส - ถ้าเราเป็นผู้ขาย / null ถ้าไม่เจอ]",
    "debtor_name": "[ชื่อลูกค้า]"
  },
  "accounting_entry": {
    "document_date": "[วันที่เอกสารในรูปแบบ YYYY-MM-DD (ISO 8601) - ถ้าเป็น พ.ศ. ต้องแปลงเป็น ค.ศ. ด้วยการ -543 เช่น 2568-543=2025]",
    "reference_number": "[เลขที่อ้างอิง]",
    "journal_book_code": "[รหัสสมุด]",
    "journal_book_name": "[ชื่อสมุด]",
    "creditor_code": "[รหัส / null]",
    "creditor_name": "[ชื่อ / '']",
    "debtor_code": "[รหัส / null]",
    "debtor_name": "[ชื่อ / '']",
    "entries": [
      {
        "account_code": "[รหัสบัญชี]",
        "account_name": "[ชื่อบัญชี]",
        "debit": "[จำนวนเงิน Debit]",
        "credit": "[จำนวนเงิน Credit]",
        "description": "[คำอธิบาย]",
        "selection_reason": "[อธิบายละเอียดว่าทำไมถึงเลือกบัญชีนี้ อ้างอิงหลักฐานจากเอกสาร (เช่น เลขที่ใบเสร็จ ชื่อผู้ขาย ประเภทสินค้า/บริการ) และหลักการทางบัญชี หรือ template ที่ใช้ ความยาว 2-3 ประโยค ภาษาไทย]",
        "side_reason": "[อธิบายหลักการว่าทำไมถึงบันทึกฝั่งนี้ (DR/CR) โดยอธิบายผลกระทบต่องบการเงิน เช่น สินทรัพย์เพิ่ม/ลด หนี้สินเพิ่ม/ลด ค่าใช้จ่ายเพิ่ม/ลด รายได้เพิ่ม/ลด พร้อมอ้างอิงหลักการ Double Entry ความยาว 2-3 ประโยค ภาษาไทย]"
      }
    ],
    "balance_check": {
      "balanced": "[true/false]",
      "total_debit": "[Sum of all debit]",
      "total_credit": "[Sum of all credit]"
    }
  },
  "validation": {
    "confidence": {
      "level": "[high/medium/low]",
      "score": "[0-100]"
    },
    "requires_review": "[true/false]",
    "fields_requiring_review": "[array]",
    "processing_notes": "[หมายเหตุ]",
    "ai_explanation": {
      "reasoning": "[อธิบายเหตุผล 2-3 ประโยคสั้นๆ ภาษาไทย]",
      "vendor_matching": {
        "found_in_document": "[ชื่อที่พบในเอกสาร]",
        "matched_with": "[code และชื่อที่จับคู่ได้ / null]",
        "matching_method": "[exact_match/fuzzy_match/tax_id_match/not_found]",
        "confidence": "[0-100]",
        "reason": "[เหตุผลภาษาไทย]"
      },
      "transaction_analysis": {
        "type": "[purchase_for_use/sale_of_service/expense/revenue]",
        "buyer_seller_determination": "[อธิบายภาษาไทย]",
        "payment_method": "[วิธีชำระเงิน]",
        "has_vat": "[true/false]",
        "payment_proof": "[true/false]"
      },
      "account_selection_logic": {
        "template_used": "[true/false]",
        "template_details": "[ชื่อ template]"
      },
      "risk_assessment": {
        "overall_risk": "[low/medium/high]",
        "factors": "[ปัจจัยภาษาไทย]",
        "recommendations": "[คำแนะนำภาษาไทย]"
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
❌ "reason_for_selection": "Transaction is a purchase of goods/services..."
❌ "reasoning": "เอกสารที่ได้รับเป็นใบกำกับภาษีและใบส่งสินค้าจาก..." (ยาวเกินไป)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`
}

// GetValidationRequirements returns the validation rules for AI output
// These ensure data quality and accounting compliance
func GetValidationRequirements() string {
	return `⚠️ VALIDATION REQUIREMENTS (ข้อกำหนดการตรวจสอบ)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

0. **Receipt Section - ข้อมูลพื้นฐานของใบเสร็จ**:
   🚨 **CRITICAL RULE: ห้ามคำนวณค่าใน receipt section**
   
   ✅ ใช้เฉพาะตัวเลขที่ปรากฏในเอกสาร:
   - "total": ยอดรวมที่ระบุชัดเจนในเอกสาร
   - "vat": ยอด VAT ที่ระบุชัดเจนในเอกสาร
     → ถ้าเอกสารไม่มีระบุ VAT แยก → ใส่ null
     → ห้ามคำนวณ VAT จาก total × 7/107
   
   ❌ ห้ามทำ:
   - คำนวณ VAT จาก total (เช่น 1040 × 7/107 = 72.9)
   - อนุมาน VAT จากสูตรใดๆ
   
   📌 ตัวอย่าง:
   เอกสารแสดง: "จำนวนเงินทั้งสิ้น 1,040 บาท" (ไม่มีระบุ VAT แยก)
   ✅ ถูก: {"total": 1040, "vat": null}
   ❌ ผิด: {"total": 1040, "vat": 72.9} ← คำนวณเอง ห้าม!
   
   💡 หมายเหตุ: การคำนวณ VAT ใน accounting_entry.entries[] เป็นคนละเรื่อง
      → ถ้ามี Template + สูตรคำนวณ → คำนวณได้ (แต่ receipt.vat ยังคงห้าม)

1. **Balance Check (ตรวจสอบยอดคงเหลือ)**:
   Sum Total Debit and Total Credit from all entry amounts
   Balance is NOT required - document errors should be visible to users
   DO NOT calculate or adjust amounts to force balance

🚨 **CRITICAL - การคำนวณทศนิยม (ห้ามผิดพลาด!)**:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

⚠️ **ห้ามปัดเศษ - ใช้ทศนิยม 2 ตำแหน่งเท่านั้น**

**กฎการคำนวณ:**
- ใช้ทศนิยม **2 ตำแหน่งเท่านั้น** (ไม่มากกว่า ไม่น้อยกว่า)
- **ห้ามปัดเศษ** ทุกกรณี แม้แต่ 0.01 บาท
- คำนวณโดยใช้ตัวเลขที่เห็นจากเอกสารเท่านั้น
- ตรวจสอบผลการคำนวณด้วยเครื่องคิดเลขก่อนส่งคำตอบ

**ตัวอย่างการคำนวณที่ถูกต้อง:**

✅ **ถูก:**
- 22,582.43 - 677.47 = **21,904.96** ✓
- 10,000.00 - 300.00 = **9,700.00** ✓
- 5,350.25 + 374.75 = **5,725.00** ✓
- 1,869.16 + 130.84 = **2,000.00** ✓

❌ **ผิด (ห้ามทำ):**
- 22,582.43 - 677.47 = 21,905.96 ❌ (ผิด! ควรเป็น 21,904.96)
- 22,582.43 - 677.47 = 21,904.96 ≈ 21,905 ❌ (ห้ามปัดเศษ!)
- 10,000 - 300 = 9,700 → แสดงเป็น 9700 ❌ (ต้องมีทศนิยม: 9,700.00)

**ข้อมูลทั่วไปเกี่ยวกับ VAT (สำหรับเข้าใจบริบท - ไม่ใช่คำสั่งให้คำนวณ):**
- มูลค่ารวม = ยอดก่อน VAT + VAT (โครงสร้างทั่วไป)
- มูลค่าก่อน VAT = มูลค่ารวม × (100 ÷ 107) (สำหรับอ้างอิง เท่านั้น)
- VAT = มูลค่ารวม × (7 ÷ 107) (สำหรับอ้างอิง เท่านั้น)

🚨 **CRITICAL: ห้ามใช้สูตรข้างบนคำนวณตัวเลข!**
   → ใช้เฉพาะตัวเลขที่อ่านได้จาก OCR เท่านั้น
   → แม้ตัวเลขใน OCR จะไม่ตรงกับสูตรก็ตาม ให้ใช้ตัวเลข OCR

**การตรวจสอบ:**
- ✓ เช็คว่าตัวเลขทุกตัวมีปรากฏในเอกสารหรือไม่
- ✓ เช็คผลรวม Debit และ Credit (ทศนิยม 2 ตำแหน่ง)
- ✓ ห้ามคำนวณตัวเลขใดๆ - ต้องมีในเอกสารทั้งหมด

2. **Template Compliance (การปฏิบัติตาม Template)**:
   If template_used = true:
   ✓ All accounts in entries[] MUST come from template.details[]
   ✓ Account count MUST match template
   ✓ NO tax accounts unless they exist in template.details[]

3. **Account Codes (รหัสบัญชี)**:
   EVERY account code MUST exist in the provided Master Data
   NEVER use codes from AI's internal knowledge
   Each shop has different chart of accounts

4. **Journal Book (สมุดรายวัน)** - ⚠️ สำคัญมาก:
   🔴 **กฎสูงสุด: ถ้ามี VAT → ห้ามใช้สมุดทั่วไป!**
   
   Priority:
   1. มี VAT + เป็นผู้ซื้อ → ค้นหาสมุดที่มีคำว่า "ซื้อ" หรือ "จ่าย"
   2. มี VAT + เป็นผู้ขาย → ค้นหาสมุดที่มีคำว่า "ขาย" หรือ "รับ"
   3. เกี่ยวกับธนาคาร → ค้นหาสมุดที่มีคำว่า "ธนาคาร"
   4. ไม่มี VAT + ไม่ใช่ซื้อ-ขาย → ใช้สมุด "ทั่วไป"

5. **Creditor/Debtor (เจ้าหนี้/ลูกหนี้)**:
   🎯 **ถ้าไม่เจอใน Master Data → ใส่ null**
   - Fuzzy matching ≥70%
   - ถ้าไม่เจอ → code = null, requires_review = true

6. **Confidence Score (คะแนนความมั่นใจ)**:
   ⚠️ **ระบบจะคำนวณ confidence อัตโนมัติ (weighted calculation)**
   - Template matching (30%)
   - Vendor matching (25%)
   - Data completeness (20%)
   - Field validation (15%)
   - Balance validation (10%)
   
   💡 AI ไม่ต้องกังวลเรื่อง confidence - ระบบคำนวณให้อัตโนมัติ
   ให้ AI focus ที่การวิเคราะห์ข้อมูลให้ถูกต้องเท่านั้น

7. **Language - ภาษาไทยเท่านั้น (บังคับ)**:
   ⚠️ **ใช้ภาษาไทยทั้งหมดใน ai_explanation**
   - reasoning → ภาษาไทย
   - reason_for_selection → ภาษาไทย
   - ทุกฟิลด์ใน ai_explanation ต้องเป็นภาษาไทย

8. **Validation Summary (ภาพรวมการตรวจสอบ)** - 🆕 CRITICAL:
   Backend จะคำนวณ confidence score อัตโนมัติตามหลักเกณฑ์:
   - Template Match: 90% × 30% = 27.0
   - Party Match: 80% × 25% = 20.0 ← **ใช้ debtor_match สำหรับเอกสารขาย**
   - Balance: 100% × 10% = 10.0
   - Field: 100% × 15% = 15.0
   - Data: 60% × 20% = 12.0
   → Total = 84%
   
   💡 **IMPORTANT**: 
   - ถ้าเอกสาร**ขาย** (เราเป็นผู้ขาย) → มี debtor_code → Party Match จะใช้คะแนนจาก debtor_match (80%)
   - ถ้าเอกสาร**ซื้อ** (เราเป็นผู้ซื้อ) → มี creditor_code → Party Match จะใช้คะแนนจาก creditor_match
   
   ⚠️ **ข้อสังเกต**:
   - Backend จะ**คำนวณใหม่**หมด - AI ไม่ต้องคำนวณ
   - AI แค่ใส่ข้อมูล debtor_code/creditor_code ให้ถูกต้อง
   - Backend จะเลือกใช้ score ที่เหมาะสมตามบริบทเอง`
}
