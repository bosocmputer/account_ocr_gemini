// prompt_amount_rules.go - Amount recording rules
package ai

// GetAmountRecordingRules returns strict rules for recording amounts
func GetAmountRecordingRules() string {
	return `
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
`
}

// GetTemplateAmountDistributionRules returns rules for distributing amounts when using template
func GetTemplateAmountDistributionRules() string {
	return `
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
`
}
