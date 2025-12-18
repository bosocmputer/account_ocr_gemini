// prompt_template_matching.go - Template matching algorithm and rules
package ai

// GetTemplateMatchingAlgorithm returns the template matching algorithm
func GetTemplateMatchingAlgorithm() string {
	return `
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🚨 ABSOLUTE RULE #1 - TEMPLATE MATCHING (กฎการจับคู่เทมเพลต)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

⚡ STEP 0: 🚨 IDENTIFY DOCUMENT TYPE FIRST! 🚨

🔴 CRITICAL - DO THIS BEFORE TEMPLATE MATCHING:

ใช้ข้อมูลที่คุณได้จาก OCR แล้ว (ใน source_images[].type และ raw_document_text) 
เพื่อระบุประเภทเอกสาร เพราะแต่ละประเภทมีกฎการจับคู่ template ที่แตกต่างกัน

💡 คุณมีข้อมูลอยู่แล้ว:
   - source_images[].type = ประเภทเอกสารที่ AI ระบุไว้แล้ว
   - raw_document_text = ข้อความเต็มที่อ่านได้จาก OCR
   - ไม่ต้อง search keyword ใหม่ เพราะคุณรู้อยู่แล้ว!

📋 ประเภทเอกสารและกฎการจับคู่:

1️⃣ หนังสือรับรองการหักภาษี ณ ที่จ่าย (Withholding Tax Certificate)
   
   ระบุโดย: source_images[].type = "tax_certificate"
   หรือ raw_document_text มี: "หนังสือรับรองการหักภาษี", "ภ.ง.ด.", "ตามมาตรา 50 ทวิ"
   
   🚨 กฎพิเศษ (บังคับเด็ดขาด):
   🚫 DO NOT match with ANY regular templates (ค่าน้ำมัน, บิลเงินสด, ค่าไฟฟ้า, etc.)
   🚫 DO NOT look at vendor name for template matching
   🚫 IGNORE "Additional Context" or "ชื่อหัวบิล" in templates
   ✅ Look ONLY at "ประเภทเงินได้" in มาตรา 40:
      - มาตรา 40(1) = เงินเดือน/ค่าจ้าง
      - มาตรา 40(2) = ค่าธรรมเนียม/ค่านายหน้า
      - มาตรา 40(8) = ค่าบริการ/ค่าขนส่ง/ค่าจ้างทำของ
   ✅ Check: มี template สำหรับประเภทเงินได้นี้หรือไม่?
   ✅ If NO → template_used = false (บังคับ!)
   ✅ If YES → ใช้ template นั้นเท่านั้น

2️⃣ ใบเสร็จรับเงิน/ใบกำกับภาษี (Receipt/Tax Invoice)
   
   Keywords:
   - "ใบเสร็จรับเงิน"
   - "ใบกำกับภาษี"
   - "TAX INVOICE"
   - มี VAT 7% หรือ เลขประจำตัวผู้เสียภาษี 13 หลัก
   
   กฎปกติ:
   ✅ ดูประเภทสินค้า/บริการ (น้ำมัน, อาหาร, ค่าบริการ)
   ✅ จับคู่กับ template ที่ตรงกับสินค้า/บริการ
   ✅ ถ้าเป็นเราออกให้ลูกค้า → ดู "ชื่อหัวบิล" ใน Additional Context
   ✅ ถ้าเป็นเราซื้อจากผู้ขาย → ดูประเภทสินค้า/บริการ

3️⃣ บิลค่าสาธารณูปโภค (Utility Bills)
   
   Keywords:
   - "บิลค่าไฟฟ้า" หรือ "กฟน." หรือ "การไฟฟ้า"
   - "บิลค่าน้ำประปา" หรือ "การประปา"
   - "บิลค่าโทรศัพท์" หรือ "AIS", "True", "dtac"
   
   กฎปกติ:
   ✅ จับคู่กับ template ตามประเภท (ค่าไฟฟ้า, ค่าน้ำ, ค่าโทรศัพท์)
   🚫 ห้ามดูชื่อบริษัทออกบิล (เพราะออกโดยหน่วยงานรัฐ)

4️⃣ เอกสารอื่นๆ (Other Documents)
   
   ✅ ใช้กฎปกติ: ดูประเภทสินค้า/บริการ
   ✅ จับคู่กับ template ที่เกี่ยวข้อง

🎯 Example - หนังสือรับรองการหักภาษี ณ ที่จ่าย:
   
   เอกสาร:
   - ✅ พบ: "หนังสือรับรองการหักภาษี ณ ที่จ่าย"
   - ✅ พบ: ประเภทเงินได้ "ค่าบริการ" (มาตรา 40(8))
   - ❌ พบ: "บจก. นพรัตน์กู๊ดไทร์" = ผู้หักภาษี (ไม่ใช่ผู้ขายสินค้า!)
   
   การจับคู่ template:
   1. เช็ค templates: มี template สำหรับ "ค่าบริการ" ไหม?
   2. ❌ ไม่มี → template_used = false (บังคับ!)
   3. 🚫 ห้ามใช้ template "บิลเงินสด" แม้ชื่อบริษัทจะตรงกัน
   4. ✅ ใช้ Master Data เลือกบัญชี: 533020 (ค่าบริการ), 215550 (ภงด)

⚡ STEP 1: EXTRACT RECEIPT CATEGORY

💡 คุณมีข้อมูลจาก STEP 0 อยู่แล้ว - ใช้ต่อเลย!
   - source_images[].type ← ประเภทเอกสารที่คุณระบุแล้ว
   - raw_document_text ← ข้อความเต็มจาก OCR

🎯 Algorithm:

1️⃣ ระบุ "หมวดหมู่หลัก" ของรายการสินค้า/บริการ (1-3 คำ):
   
   Method A: ใช้ข้อมูลจาก raw_document_text
   - ชื่อสินค้า/บริการ → "น้ำมัน", "ไฟฟ้า", "อาหาร", "ทำบัญชี"
   - ชื่อร้านค้า/ผู้ขาย → "ปตท.", "กฟน.", "เซเว่น"
   
   Method B: ใช้ข้อมูลจาก source_images[].type
   - type = "invoice" → มุ่งเน้นชื่อสินค้า/บริการ
   - type = "receipt" → มุ่งเน้นชื่อร้านค้า หรือ สินค้าหลัก
   - type = "utility_bill" → ไฟฟ้า/น้ำ/โทรศัพท์

2️⃣ หลักการสำคัญ:
   ✓ มุ่งเน้น "สินค้า/บริการ" ไม่ใช่ "ชื่อผู้ขาย"
   ✓ ใช้ภาษาที่กระชับชัดเจน (1-3 คำ)
   ✓ พยายามระบุหมวดหมู่ที่ชัดเจน

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
   - Direct keyword match (confidence ≥ 95%)
   - High semantic similarity (confidence ≥ 90%)
   - Confident that they are related
   
   ❌ DON'T use template (SET template_used = false) when:
   - No matching template found
   - Keywords are unrelated
   - Uncertain (confidence < 80%)
   
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

⚡ STEP 2.5: EXPLAIN TEMPLATE SELECTION (🚨 MANDATORY - ห้ามลืม!)

🎯 เมื่อเลือก template แล้ว ต้องอธิบายเหตุผลอย่างละเอียดใน template_info.selection_reason:

🚨 CRITICAL: selection_reason ต้องไม่ว่างเปล่า! ต้องมีคำอธิบายเสมอ!

✅ ต้องระบุ 4 สิ่ง:
   1. 📄 หลักฐานที่พบในเอกสาร (คำหรือข้อมูลที่ใช้ตัดสินใจ)
   2. 🎯 ความคล้ายคลึงระหว่างเอกสารกับ template
   3. 💡 เหตุผลว่าทำไมถึงเลือก template นี้โดยเฉพาะ
   4. 📊 ข้อมูลยอดเงินและรายละเอียดสำคัญ

📝 รูปแบบการอธิบาย (ต้องมี 2-3 ประโยค):

   ✅ ดีมาก (มีรายละเอียดครบ):
   "ใบเสร็จระบุ 'ค่าน้ำมันดีเซล' จาก 'ปตท. สถานีบางนา' ยอด 1,500 บาท รวม VAT 7% แล้ว ตรงกับเทมเพลต 'ค่าน้ำมัน' ซึ่งใช้บัญชี 611100 (ค่าเชื้อเพลิง) และ 134100 (ภาษีซื้อ) เหมาะสมกับการซื้อน้ำมันเพื่อใช้ในกิจการ"
   
   ✅ ดี (อธิบายเหตุผลชัดเจน):
   "เอกสารเป็นบิลค่าไฟฟ้าจาก กฟน. ระบุงวดเดือน พ.ย. 2567 ยอด 850 บาท ชำระผ่านธนาคาร ตรงกับเทมเพลต 'ค่าไฟฟ้า' ที่มีบัญชี 621100 (ค่าสาธารณูปโภค-ไฟฟ้า) และ 113100 (เงินฝากธนาคาร) เหมาะกับการบันทึกค่าไฟฟ้าที่ชำระผ่านบัญชี"
   
   ✅ ดี (กรณีมี VAT):
   "บิลเงินสด/ใบกำกับภาษีอย่างย่อจาก บจก.นพรัตน์กู๊ดไทร์ ระบุยอดรวม 2,400 บาท รวม VAT 7% เราเป็นผู้ขาย (มีชื่อลูกค้า 'ขาย-สด') ตรงกับเทมเพลต 'บิลเงินสด/ใบกำกับภาษีอย่างย่อ' ที่มีการแยก VAT ขาย และรายได้จากการขายสินค้า"
   
   ❌ ไม่ดี (สั้นเกินไป - ขาดหลักฐาน):
   "ตรงกับเทมเพลต"
   "เลือกเทมเพลตนี้เพราะเหมาะสม"
   
   ❌ ไม่ดี (ไม่มีรายละเอียดจากเอกสาร):
   "AI วิเคราะห์แล้วพบว่าใบเสร็จตรงกับเทมเพลตที่กำหนดไว้"
   "ใช้เทมเพลตตามที่ระบุ"
   
   ❌ ไม่ดี (ไม่มีข้อมูลยอดเงิน):
   "เอกสารเป็นค่าน้ำมัน ตรงกับเทมเพลต" (ไม่มียอดเงิน)

🎯 สำหรับ template_info.note ให้เพิ่ม:
   - รายการบัญชีที่ใช้จาก template
   - หมายเหตุพิเศษ เช่น "มีการคำนวณ VAT ตามสูตร" หรือ "ไม่มี VAT"
   
   ตัวอย่าง:
   ✅ "ใช้บัญชี: 611100 ค่าเชื้อเพลิง (DR), 134100 ภาษีซื้อ (DR), 113100 เงินฝากธนาคาร (CR)"
   ✅ "ไม่พบ template ที่ตรงกัน ใช้ Master Data เพื่อเลือกบัญชีที่เหมาะสมตามหลักการบัญชี"

4️⃣ ⚠️ Universal Rules (apply to all documents):
   
   ✓ DO:
   - Compare with ALL template descriptions
   - Select the best matching template
   - When uncertain → template_used = false (safer)
   - Always fill selection_reason with detailed explanation
   
   ✗ DON'T:
   - Force use of unrelated templates
   - Look at template.details (accounts)
   - Use generic template (เบ็ดเตล็ด) when specific template exists
   - Leave selection_reason empty

⚡ STEP 3: IF TEMPLATE MATCHED - STRICT MODE

Decision:
- If match found → PROCEED TO STEP 3 (use template strictly)
- If NO match found → SET template_used = false → Use Master Data instead

⚠️ Principle: Template matching must be strict - use when matched, don't force when not matched!
`
}

// GetTemplateStrictModeRules returns rules for using matched templates
func GetTemplateStrictModeRules() string {
	return `
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
  → Your job: OBEY template, NOT "fix" it!

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

📚 MORE EXAMPLES - READ BEFORE EVERY ANALYSIS (ตัวอย่างเพิ่มเติม):

Example 1: Template "Fuel" with 2 accounts
  Template: [{accountcode: "531220", detail: "Fuel Expense"}, {accountcode: "111110", detail: "Cash"}]
  Receipt: 2,000 THB (including VAT 130.84)

  ✅ CORRECT: Use only 2 accounts, total = 2000
  ❌ WRONG: Add VAT account (template doesn't have it!)

Example 2: Template "Electricity" (ค่าไฟ)
  Template has 2 accounts: Electricity expense account, Bank account
  Receipt: 5,000 + VAT 350 = 5,350 THB

  ✅ CORRECT: Use only the 2 accounts from template, total = 5350
  ❌ WRONG: Add a VAT account (template doesn't have it!)

Example 3: Template "Accounting Service" (ค่าทำบัญชี)
  Template has 3 accounts: Professional Fees, WHT receivable, Bank

  ✅ CORRECT: Use all 3 accounts from template
  ❌ WRONG: Skip WHT account or add extra accounts

Example 4: No Template Match
  Receipt: "Office Snacks" (ขนมสำนักงาน)
  No matching template found

  ✅ CORRECT: Set template_used = false, analyze using Master Data
  → Can add VAT account if receipt shows VAT AND account exists in Master Data
  → Use accounting knowledge freely
  → MUST verify all account codes exist in provided Master Data (Chart of Accounts)
`
}

// GetNoTemplateMatchRules returns rules when no template matches
func GetNoTemplateMatchRules() string {
	return `
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📋 SECTION: NO TEMPLATE MATCH - FREE ANALYSIS MODE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

⚠️ ONLY apply this section if template_used = false (no matching template)

When NO template matches:
  ✓ Use Master Data provided in this message:
    - Chart of Accounts (ผังบัญชี) - ONLY use account codes from this list
    - Journal Books (สมุดรายวัน) - ONLY use journal codes from this list
    - Creditors/Debtors (เจ้าหนี้/ลูกหนี้)

  ✓ Journal Book Auto-Selection Rules:
    - วิเคราะห์ประเภทเอกสาร (ซื้อ/ขาย/ทั่วไป)
    - ดูลักษณะธุรกรรม (เงินสด/เครดิต/โอน)
    - เลือก journal book ที่เหมาะสมจาก journalBooks list
    - อธิบายเหตุผลในการเลือก

  ✓ Apply standard Thai accounting practices

  ✓ Add tax accounts if receipt shows VAT/WHT (CRITICAL RULE):
    - Receipt has VAT 7% → Search for Input VAT account in Chart of Accounts
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
`
}
