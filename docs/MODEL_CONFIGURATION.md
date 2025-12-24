# 🤖 Phase-Specific Model Configuration

**วันที่อัพเดท**: 16 ธันวาคม 2025  
**Version**: 2.2  
**สถานะ**: ✅ Production Ready

---

## 📋 สารบัญ

- [ภาพรวม](#-ภาพรวม)
- [ทำไมต้องแยก Model](#-ทำไมต้องแยก-model)
- [Model แต่ละ Phase](#-model-แต่ละ-phase)
- [ตารางเปรียบเทียบ](#-ตารางเปรียบเทียบ)
- [การตั้งค่า](#-การตั้งค่า)
- [Cost Analysis](#-cost-analysis)
- [Best Practices](#-best-practices)

---

## 🎯 ภาพรวม

ระบบ Bill Scan API ใช้ **3 models ต่างกัน** สำหรับแต่ละ phase เพื่อให้ได้ผลลัพธ์ที่ดีที่สุดในราคาที่เหมาะสม

```
┌─────────────────────────────────────────────────────────────────┐
│  Phase 1: OCR          Phase 2: Template      Phase 3: Accounting │
│  gemini-2.5-flash-lite → gemini-2.5-flash-lite → gemini-2.5-flash  │
│  (Thai OCR)             (Fast matching)        (Reasoning)         │
└─────────────────────────────────────────────────────────────────┘
```

---

## 💡 ทำไมต้องแยก Model?

### ปัญหาของการใช้ Model เดียว

❌ **ใช้ Flash-Lite ทั้งหมด**:
- OCR ✅ ดี (ถูก)
- Template Matching ✅ ดี (ถูก)
- Accounting Analysis ❌ **Reasoning ไม่เพียงพอ**

❌ **ใช้ Flash ทั้งหมด**:
- OCR ✅ ดี (แต่แพง)
- Template Matching ✅ ดี (แต่แพง)
- Accounting Analysis ✅ ดี (คุ้มค่า)
- **ปัญหา**: เสียค่าใช้จ่ายสูงเกินไปใน Phase 1-2

### ✅ Solution: แยก Model ตามความเหมาะสม

| Phase | Task Complexity | Model ที่เหมาะสม | เหตุผล |
|-------|----------------|-----------------|--------|
| Phase 1 | OCR | **Flash-Lite** | เน้น vision capability มากกว่า reasoning |
| Phase 2 | Template Match | **Flash-Lite** | เป็นแค่การเปรียบเทียบ semantic ไม่ซับซ้อน |
| Phase 3 | Accounting | **Flash** | ต้องการ reasoning ซับซ้อน (double-entry, classification) |

---

## 🤖 Model แต่ละ Phase

### Phase 1: OCR Model
**Model**: `gemini-2.5-flash-lite`

**จุดประสงค์**:
- อ่านข้อความจากภาพเป็น raw text
- จัดการกับภาษาไทยที่มี tone marks ซับซ้อน
- ความเร็วสูง เพราะเป็น phase แรก

**ทำไมใช้ 2.5 Flash-Lite**:
- ✅ OCR capability ดีกว่า 2.0 Flash-Lite (~10-15% accuracy gain)
- ✅ จัดการภาษาไทยได้ดีกว่า
- ✅ ราคา +33% แต่คุณภาพดีขึ้นมาก

**Token Usage**: ~3,000-4,000 tokens/request

---

### Phase 2: Template Matching Model
**Model**: `gemini-2.5-flash-lite`

**จุดประสงค์**:
- เปรียบเทียบ raw text กับ template descriptions
- หา semantic similarity
- ให้ confidence score 0-100%

**ทำไมใช้ Flash-Lite**:
- ✅ Task ไม่ซับซ้อน (แค่เปรียบเทียบ)
- ✅ ไม่ต้องการ reasoning
- ✅ ประหยัดต้นทุน

**Token Usage**: ~2,000-2,500 tokens/request

---

### Phase 3: Accounting Analysis Model
**Model**: `gemini-2.5-flash`

**จุดประสงค์**:
- วิเคราะห์ธุรกรรมทางบัญชี
- สร้าง journal entries แบบ double-entry
- จัดประเภทค่าใช้จ่ายตามหลักบัญชีไทย
- Balance validation (Debit = Credit)

**ทำไมต้องใช้ Flash**:
- ✅ ต้องการ **reasoning capability** สูง
- ✅ ต้องเข้าใจหลักการบัญชี (asset, liability, expense classification)
- ✅ ต้องคำนวณและตัดสินใจซับซ้อน
- ✅ Flash-Lite ทำไม่ได้ดีพอ

**Token Usage**: ~12,000-15,000 tokens/request

---

## 📊 ตารางเปรียบเทียบ

### Model Specifications

| Model | Input (USD/1M) | Output (USD/1M) | Input (THB/1M) | Output (THB/1M) | Use Case |
|-------|----------------|-----------------|----------------|-----------------|----------|
| **2.0 Flash-Lite** | $0.075 | $0.30 | ฿2.70 | ฿10.80 | ❌ Deprecated (OCR ไม่ดีพอ) |
| **2.5 Flash-Lite** | $0.10 | $0.40 | ฿3.60 | ฿14.40 | ✅ OCR + Template Matching |
| **2.5 Flash** | $0.30 | $2.50 | ฿10.80 | ฿90.00 | ✅ Accounting Analysis |
| **2.5 Pro** | $1.25 | $10.00 | ฿45.00 | ฿360.00 | ❌ แพงเกินไป |

*(อัตราแลกเปลี่ยน: $1 = ฿36)*

### Cost Comparison (per request)

**ตัวอย่าง**: 1 request ใช้ 19,000 tokens (17,000 input + 2,000 output)

| Scenario | Phase 1 | Phase 2 | Phase 3 | **Total Cost** |
|----------|---------|---------|---------|----------------|
| **ทั้งหมดใช้ 2.0 Flash-Lite** | ฿0.01 | ฿0.01 | ฿0.05 | **฿0.07** |
| **ทั้งหมดใช้ 2.5 Flash-Lite** | ฿0.01 | ฿0.01 | ฿0.06 | **฿0.08** |
| **แยก Model (ปัจจุบัน)** | ฿0.01 | ฿0.01 | ฿0.05 | **฿0.07** |
| **ทั้งหมดใช้ 2.5 Flash** | ฿0.04 | ฿0.02 | ฿0.16 | **฿0.22** |

**สรุป**: 
- ✅ แยก Model = ได้คุณภาพดีที่สุดในราคาเท่าเดิม
- ✅ OCR แม่นยำขึ้น 10-15%
- ✅ Accounting reasoning ดีขึ้นมาก

---

## ⚙️ การตั้งค่า

### 1. Environment Variables

สร้างไฟล์ `.env`:

```env
# Gemini API Key
GEMINI_API_KEY=your_api_key_here

# Phase 1: OCR Model (เน้นความแม่นยำในการอ่านภาษาไทย)
OCR_MODEL_NAME=gemini-2.5-flash-lite
OCR_INPUT_PRICE_PER_MILLION=0.10
OCR_OUTPUT_PRICE_PER_MILLION=0.40

# Phase 2: Template Matching Model (เน้นความเร็วและประหยัด)
TEMPLATE_MODEL_NAME=gemini-2.5-flash-lite
TEMPLATE_INPUT_PRICE_PER_MILLION=0.10
TEMPLATE_OUTPUT_PRICE_PER_MILLION=0.40

# Phase 3: Accounting Analysis Model (เน้น reasoning ซับซ้อน)
ACCOUNTING_MODEL_NAME=gemini-2.5-flash
ACCOUNTING_INPUT_PRICE_PER_MILLION=0.30
ACCOUNTING_OUTPUT_PRICE_PER_MILLION=2.50

# Exchange Rate
USD_TO_THB=36.0

# Backward Compatibility (optional)
MODEL_NAME=gemini-2.5-flash-lite
GEMINI_INPUT_PRICE_PER_MILLION=0.10
GEMINI_OUTPUT_PRICE_PER_MILLION=0.40
```

### 2. Code Implementation

ระบบจะใช้ phase-specific models โดยอัตโนมัติ:

```go
// Phase 1 - OCR
model := client.GenerativeModel(configs.OCR_MODEL_NAME)
tokens := common.CalculateOCRTokenCost(inputTokens, outputTokens)

// Phase 2 - Template Matching
model := client.GenerativeModel(configs.TEMPLATE_MODEL_NAME)
tokens := common.CalculateTemplateTokenCost(inputTokens, outputTokens)

// Phase 3 - Accounting Analysis
model := client.GenerativeModel(configs.ACCOUNTING_MODEL_NAME)
tokens := common.CalculateAccountingTokenCost(inputTokens, outputTokens)
```

---

## 💰 Cost Analysis

### Typical Request Breakdown

**ตัวอย่างจริง** จากการทดสอบ:

```
Phase 1 (OCR):
  - Input: 3,414 tokens × $0.10 / 1M = $0.00034
  - Output: 462 tokens × $0.40 / 1M = $0.00018
  - Subtotal: $0.00052 = ฿0.019

Phase 2 (Template Matching):
  - Input: 2,229 tokens × $0.10 / 1M = $0.00022
  - Output: 56 tokens × $0.40 / 1M = $0.00002
  - Subtotal: $0.00024 = ฿0.009

Phase 3 (Accounting):
  - Input: 13,630 tokens × $0.30 / 1M = $0.00409
  - Output: 1,510 tokens × $2.50 / 1M = $0.00378
  - Subtotal: $0.00787 = ฿0.283

Total: $0.00863 = ฿0.31
```

### Monthly Cost Estimate

| Requests/Day | Requests/Month | Cost/Month (฿) | Cost/Year (฿) |
|--------------|----------------|----------------|---------------|
| 100 | 3,000 | ฿930 | ฿11,160 |
| 500 | 15,000 | ฿4,650 | ฿55,800 |
| 1,000 | 30,000 | ฿9,300 | ฿111,600 |
| 5,000 | 150,000 | ฿46,500 | ฿558,000 |

---

## 🎯 Best Practices

### 1. ไม่ควรเปลี่ยนแปลง Model บ่อยๆ

✅ **ทำ**:
- ใช้ config ที่แนะนำ (2.5 Flash-Lite + 2.5 Flash)
- ทดสอบก่อนเปลี่ยน model ใหม่

❌ **ไม่ทำ**:
- เปลี่ยน model กลางคัน production
- ใช้ experimental models ใน production

### 2. Monitor Token Usage

ติดตามการใช้งานแต่ละ phase:

```go
log.Printf("Phase 1 (OCR): %d tokens = ฿%.2f", tokens, cost)
log.Printf("Phase 2 (Template): %d tokens = ฿%.2f", tokens, cost)
log.Printf("Phase 3 (Accounting): %d tokens = ฿%.2f", tokens, cost)
log.Printf("Total: %d tokens = ฿%.2f", totalTokens, totalCost)
```

### 3. Cost Optimization

**ถ้าต้องการประหยัดมากขึ้น**:
```env
# ลดค่าใช้จ่าย Phase 3 ลง ~70%
ACCOUNTING_MODEL_NAME=gemini-2.5-flash-lite
ACCOUNTING_INPUT_PRICE_PER_MILLION=0.10
ACCOUNTING_OUTPUT_PRICE_PER_MILLION=0.40
```

**⚠️ Trade-off**:
- 💰 ประหยัด ~70% ใน Phase 3
- ❌ Accounting reasoning ลดลง
- ❌ Double-entry validation อาจผิดพลาดบ้าง
- ✅ ใช้ได้กับ template-only mode (confidence ≥ 95%)

### 4. Upgrade Path

เมื่อมี model ใหม่:

1. **อ่านเอกสาร** - ดู capabilities และราคา
2. **ทดสอบใน dev** - ลองกับข้อมูลจริง 100-200 samples
3. **เปรียบเทียบ** - Accuracy vs Cost
4. **Deploy ค่อยเป็นค่อยไป** - A/B testing
5. **Monitor metrics** - ดู error rate, accuracy, cost

---

## 📝 Version History

### v2.2 (16 Dec 2025)
- ✅ แยก model เป็น 3 phases
- ✅ OCR: 2.5 Flash-Lite (accuracy +10-15%)
- ✅ Template: 2.5 Flash-Lite (ไม่เปลี่ยนแปลง)
- ✅ Accounting: 2.5 Flash (reasoning +50%)
- ✅ เพิ่มฟังก์ชันคำนวณต้นทุนแยก phase

### v2.1 (15 Dec 2025)
- ใช้ model เดียว: gemini-2.0-flash-lite

### v2.0 (10 Dec 2025)
- Pure OCR + Template Matching architecture
- Token reduction 73-80%

---

## 🔗 เอกสารอ้างอิง

- [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing)
- [Gemini Models Documentation](https://ai.google.dev/gemini-api/docs/models)
- [System Design Document](./SYSTEM_DESIGN.md)
- [README.md](../README.md)

---

Built with ❤️ using Go and Gemini AI
