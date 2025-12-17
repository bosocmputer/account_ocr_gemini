// prompt_template_matching.go - Template matching algorithm and rules
package ai

// GetTemplateMatchingAlgorithm returns the template matching algorithm
func GetTemplateMatchingAlgorithm() string {
	return `
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
   
   C. Document type determines extraction method:
      ✓ Withholding Tax Certificate → Income Type ONLY (ignore item descriptions)
      ✓ Regular receipt → Focus on goods/services received
      ✓ Use concise, clear language (1-3 words)

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

4️⃣ ⚠️ Universal Rules (apply to all documents):
   
   ✓ DO:
   - Compare with ALL template descriptions
   - Select the best matching template
   - When uncertain → template_used = false (safer)
   
   ✗ DON'T:
   - Force use of unrelated templates
   - Look at template.details (accounts)
   - Use generic template (เบ็ดเตล็ด) when specific template exists

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
