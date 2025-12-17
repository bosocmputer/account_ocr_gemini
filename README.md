# 🧾 Bill Scan API - AI Accounting System

> ระบบวิเคราะห์บิลและสร้างรายการบัญชีอัตโนมัติด้วย AI  
> AI-powered Receipt Analysis & Accounting Entry Generation System

[![Go Version](https://img.shields.io/badge/Go-1.24.5-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Gemini API](https://img.shields.io/badge/Gemini-2.5--flash-4285F4?style=flat&logo=google)](https://ai.google.dev/)
[![MongoDB](https://img.shields.io/badge/MongoDB-6.0-47A248?style=flat&logo=mongodb)](https://www.mongodb.com/)

---

## 📋 สารบัญ

- [ภาพรวมระบบ](#-ภาพรวมระบบ)
- [คุณสมบัติหลัก](#-คุณสมบัติหลัก)
- [สถาปัตยกรรมระบบ](#-สถาปัตยกรรมระบบ)
- [เทคโนโลยีที่ใช้](#-เทคโนโลยีที่ใช้)
- [การติดตั้ง](#-การติดตั้ง)
- [API Documentation](#-api-documentation)
- [โครงสร้างโปรเจกต์](#-โครงสร้างโปรเจกต์)
- [📚 เอกสารเพิ่มเติม](#-เอกสารเพิ่มเติม)

---

## 🎯 ภาพรวมระบบ

**Bill Scan API** คือระบบ Backend ที่พัฒนาด้วย Go เพื่อแปลงรูปภาพใบเสร็จ/ใบกำกับภาษี **(รองรับทั้ง Image และ PDF)** ให้เป็นรายการบัญชีอัตโนมัติ โดยใช้ **Gemini AI** วิเคราะห์เอกสาร และจับคู่กับ **Template ทางบัญชี** จาก MongoDB เพื่อสร้างรายการบัญชีที่ถูกต้องตามหลักการบัญชีไทย

### ปัญหาที่แก้ไข
- ❌ นักบัญชีต้องป้อนรายการบัญชีด้วยตนเอง → เสียเวลา เสี่ยงผิดพลาด
- ❌ ใช้ token มาก (60,000 tokens/request) → ค่าใช้จ่ายสูง
- ❌ AI เลือกบัญชีผิด → ไม่เข้าใจบริบทบัญชีไทย

### วิธีแก้
- ✅ **Pure OCR + Template Matching** → ลด token 83% (60K → 10-17K)
- ✅ **AI-driven Template Matching** → จับคู่ template อัจฉริยะ (95-100% accuracy)
- ✅ **Template-Only Mode** (≥85% confidence) → ใช้ template พร้อม forced balance
- ✅ **Full Mode** (< 85% confidence) → วิเคราะห์เต็มรูปแบบพร้อม Thai accounting rules
- ✅ **Thai Accounting Classification** → แยกประเภทค่าใช้จ่ายตามมาตรฐานไทย
- ✅ **Master Data Integration** → ใช้ผังบัญชี, สมุดรายวัน, เจ้าหนี้/ลูกหนี้ จาก MongoDB

---

## ✨ คุณสมบัติหลัก

### 🚀 Performance Optimization
- **Token Savings**: ลดจาก 60,000 → 12,000-17,000 tokens (73-80% reduction)
- **Cost Reduction**: ลดค่าใช้จ่าย AI API 73-80%
- **Fast Processing**: 15-20 วินาที/request
- **File Format Support**: รองรับทั้ง Image (JPG, PNG) และ PDF โดยอัตโนมัติ

### 🎯 Intelligent Processing
- **3-Phase Architecture**:
  1. **Pure OCR** (Phase 2) - สกัดข้อความดิบ (~2,100 tokens)
  2. **AI Template Matching** (Phase 2.5) - จับคู่ template อัจฉริยะ (~1,200 tokens)
  3. **Accounting Analysis** (Phase 3) - วิเคราะห์บัญชี (10,000-17,000 tokens)

- **Dual Mode Operation**:
  - **Template-Only Mode** (template confidence ≥ 85%):
    - ใช้บัญชีจาก template เท่านั้น
    - Force balance (Total Debit = Total Credit)
    - ไม่ต้องใช้ full master data → ประหยัด ~25,000 tokens
  
  - **Full Mode** (template confidence < 85%):
    - ใช้ผังบัญชีเต็ม (240 accounts)
    - Thai accounting classification rules
    - Smart account selection based on transaction type

### 🇹🇭 Thai Accounting Support
- **หลักการจัดประเภทบัญชีไทย**:
  - แยกแยะ: บริการวิชาชีพ vs วัสดุอุปกรณ์
  - ค้นหาบัญชีจาก Chart of Accounts (ไม่ใช้รหัสเฉพาะ hardcode)
  - รองรับผังบัญชีที่แตกต่างกันของแต่ละธุรกิจ

- **Master Data Integration**:
  - Chart of Accounts (ผังบัญชี)
  - Journal Books (สมุดรายวัน)
  - Creditors/Debtors (เจ้าหนี้/ลูกหนี้)
  - Document Templates (รูปแบบบัญชีที่บันทึกไว้)

### 🔒 Quality Assurance
- **Confidence Scoring**: ระดับความมั่นใจแต่ละฟิลด์
- **Balance Validation**: ตรวจสอบ Debit = Credit
- **Review Flags**: บอกว่าข้อมูลไหนต้องตรวจสอบ
- **Thai Language**: คำอธิบายเป็นภาษาไทยทั้งหมด

### ⚡ Rate Limiting & Reliability
- **Sequential Processing**: ประมวลผล 1 request ต่อครั้งเพื่อหลีกเลี่ยง API burst traffic
- **Token Bucket Rate Limiter**: 12 tokens, 5-second refill (20% safety margin)
- **Smart Retry Logic**: Exponential backoff พร้อม 30-90 second delay สำหรับ 429 errors
- **Phase-Level Rate Limiting**: ทุก API call ผ่าน rate limiter (Pure OCR, Template Matching, Accounting Analysis)
- **Error Handling**: จัดการ Gemini API errors (429, 500, timeout) อัตโนมัติ

---

## 🏗️ สถาปัตยกรรมระบบ

### Processing Pipeline

```
1. Request Validation
   └─> Validate shopid, check master data exists

2. Pure OCR Extraction (~2,100 tokens)
   └─> Gemini AI อ่านข้อความทั้งหมดจากเอกสาร
   └─> Output: raw_document_text

3. AI Template Matching (~1,200 tokens)
   └─> Gemini AI วิเคราะห์ vs template descriptions
   └─> Confidence: 0-100%, Threshold: 85%

4. Conditional Processing:
   
   A. Template-Only Mode (confidence ≥ 85%)
      └─> ใช้บัญชีจาก template (~7,000 tokens)
      └─> Force balance: Debit = Credit
   
   B. Full Mode (confidence < 85%)
      └─> วิเคราะห์เต็มรูปแบบ (~15,000 tokens)
      └─> ใช้ Chart of Accounts (240 accounts)
      └─> Thai accounting classification

5. Response Generation
   └─> Receipt data + Accounting entry + Validation
```

### Model Pricing (per 1M tokens)

| Model | Input (USD) | Output (USD) | Input (THB) | Output (THB) | Use Case |
|-------|-------------|--------------|-------------|--------------|----------|
| **2.0 Flash-Lite** | $0.075 | $0.30 | ฿2.70 | ฿10.80 | ถูกสุด แต่ OCR ไม่ดีเท่า 2.5 |
| **2.5 Flash-Lite** | $0.10 | $0.40 | ฿3.60 | ฿14.40 | ⭐ OCR & Template (ปัจจุบัน) |
| **2.5 Flash** | $0.30 | $2.50 | ฿10.80 | ฿90.00 | ⭐ Accounting Analysis (ปัจจุบัน) |
| **2.5 Pro** | $1.25 | $10.00 | ฿45.00 | ฿360.00 | ❌ แพงเกินไป |

### Token Usage Comparison

| Mode | Phase 2 (OCR) | Phase 2.5 (Matching) | Phase 3 (Analysis) | **Total** | Savings |
|------|---------------|---------------------|-------------------|-----------|---------|
| **Old (Full OCR)** | 30,000 | N/A | 30,000 | **60,000** | - |
| **Template-Only** | 2,100 | 1,200 | 7,000 | **10,300** | **83%** ⬇️ |
| **Full Mode** | 2,100 | 1,200 | 14,000 | **17,300** | **71%** ⬇️ |

---

## 🛠️ เทคโนโลยีที่ใช้

| Component | Technology | Purpose |
|-----------|-----------|---------|
| **Backend** | Go 1.24.5 | High-performance, concurrent processing |
| **Framework** | Gin | HTTP web framework |
| **AI (OCR)** | Gemini 2.5 Flash-Lite | Thai OCR with better accuracy |
| **AI (Template)** | Gemini 2.5 Flash-Lite | Fast template matching |
| **AI (Accounting)** | Conditional Selection | Smart model switching based on confidence |
| ↳ Template-only (≥85%) | Gemini 2.5 Flash-Lite | Fast & cheap for high-confidence cases |
| ↳ Full analysis (<85%) | Gemini 2.5 Flash | Better reasoning for complex cases |
| **Database** | MongoDB 6.0 | Master data storage |
| **Caching** | In-memory | 5-minute TTL for master data |
| **Image** | Disintegration/Imaging | Image preprocessing |

### Key Dependencies
```go
github.com/gin-gonic/gin v1.11.0
github.com/google/generative-ai-go v0.20.1
go.mongodb.org/mongo-driver v1.17.1
```

---

## 🚀 การติดตั้ง

### Prerequisites
- Go 1.24.5+
- MongoDB 6.0+
- Gemini API Key ([Get here](https://ai.google.dev/))

### 1. Clone & Install
```bash
git clone <repository>
cd bill_scan_project
go mod download
```

### 2. Configuration
สร้างไฟล์ `.env` (หรือแก้ไข `configs/config.go`):
```env
# Gemini AI API Key
GEMINI_API_KEY=YOUR_GEMINI_API_KEY

# Phase 1: OCR Model (เน้นความแม่นยำในการอ่านภาษาไทย)
OCR_MODEL_NAME=gemini-2.5-flash-lite
OCR_INPUT_PRICE_PER_MILLION=0.10
OCR_OUTPUT_PRICE_PER_MILLION=0.40

# Phase 2: Template Matching Model (เน้นความเร็วและประหยัด)
TEMPLATE_MODEL_NAME=gemini-2.5-flash-lite
TEMPLATE_INPUT_PRICE_PER_MILLION=0.10
TEMPLATE_OUTPUT_PRICE_PER_MILLION=0.40

# Phase 3: Accounting Analysis Model (Conditional Selection)
# สำหรับเทมเพลต: ใช้ Flash-Lite (เร็ว ประหยัด) เมื่อความมั่นใจ ≥85%
TEMPLATE_ACCOUNTING_MODEL_NAME=gemini-2.5-flash-lite
TEMPLATE_ACCOUNTING_INPUT_PRICE_PER_MILLION=0.10
TEMPLATE_ACCOUNTING_OUTPUT_PRICE_PER_MILLION=0.40

# สำหรับวิเคราะห์เต็มรูปแบบ: ใช้ Flash (เน้น reasoning ซับซ้อน) เมื่อความมั่นใจ <85%
ACCOUNTING_MODEL_NAME=gemini-2.5-flash
ACCOUNTING_INPUT_PRICE_PER_MILLION=0.30
ACCOUNTING_OUTPUT_PRICE_PER_MILLION=2.50

# Exchange Rate
USD_TO_THB=36.0

# MongoDB
MONGO_URI=mongodb://localhost:27017
MONGO_DB_NAME=your_database
```

**💡 Why Different Models?**
- **OCR Phase**: 2.5 Flash-Lite มี OCR capability ดีกว่า 2.0 Flash-Lite (+33% cost แต่แม่นยำกว่า)
- **Template Matching**: ใช้ Flash-Lite ก็เพียงพอ ไม่ต้องใช้ model แพง
- **Accounting Analysis**: ต้องการ reasoning → ใช้ 2.5 Flash (คุณภาพสูงกว่า)

### 3. Setup MongoDB
ต้องมี collections:
- `chartOfAccounts` - ผังบัญชี
- `journalBooks` - สมุดรายวัน
- `creditors` - เจ้าหนี้
- `debtors` - ลูกหนี้
- `documentFormate` - Templates ทางบัญชี
- `shopProfile` - ข้อมูลร้านค้า

#### 📋 Template Format (documentFormate collection)
```json
{
  "_id": "template_id",
  "shopid": "36gw9v2oP2Rmg98lIovlQ6Dbcfh",
  "description": "ค่าน้ำมัน",
  "promptdescription": "ซื้อน้ำมันเชื้อเพลิงจากปั๊ม เช่น บางจาก, ปตท, เชลล์, เอสโซ่, คาลเท็กซ์",
  "bookcode": "02",
  "details": [
    {"accountcode": "531220", "detail": "ค่าน้ำมัน-ค่าแก๊สรถยนต์"},
    {"accountcode": "111110", "detail": "เงินสดในมือ"}
  ]
}
```

**คำอธิบาย fields:**
- `description`: ชื่อ template หลัก (แสดงให้ user เห็น)
- `promptdescription`: **(ใหม่!)** บริบทเพิ่มเติมสำหรับ AI เพื่อเลือก template ให้ถูกต้อง
  - ระบุคีย์เวิร์ด, ชื่อผู้ขาย, ประเภทสินค้า/บริการ
  - ยิ่งระบุชัดเจน AI จะเลือกได้แม่นยำมากขึ้น
  - ตัวอย่าง:
    - ค่าทำบัญชี: "บริการทำบัญชีจากบริษัท ซีแอนฮิล, AAA Accounting"
    - ค่าไฟฟ้า: "ค่าไฟฟ้าจาก การไฟฟ้านครหลวง (MEA), การไฟฟ้าส่วนภูมิภาค (PEA)"
    - ค่าอินเทอร์เน็ต: "ค่าบริการอินเทอร์เน็ตจาก True, AIS, 3BB"
- `bookcode`: รหัสสมุดรายวันที่ใช้
- `details`: รายการบัญชีที่ต้องใช้ทุกครั้ง

**💡 Tips สำหรับ promptdescription ที่ดี:**
- ✅ ระบุชื่อบริษัท/ผู้ขายที่ชัดเจน
- ✅ ใช้คำที่มักปรากฏในเอกสารจริง
- ✅ ระบุทั้งชื่อเต็มและชื่อย่อ (เช่น "MEA, การไฟฟ้านครหลวง")
- ❌ อย่าใช้คำที่กว้างเกินไป (เช่น "ค่าใช้จ่ายทั่วไป")

### 4. Run Server
```bash
# Development
go run ./cmd/api

# Production
make build
./bin/go-receipt-parser
```

Server จะรันที่ `http://localhost:8080`

---

## 📡 API Documentation

### POST /api/v1/analyze-receipt

วิเคราะห์เอกสารใบเสร็จ/ใบกำกับภาษี (รองรับ Image และ PDF) และสร้างรายการบัญชีอัตโนมัติ

#### Request
**Headers:**
- `Content-Type: application/json`

**Body:** `application/json`
```json
{
  "shopid": "36gw9v2oP2Rmg98lIovlQ6Dbcfh",
  "imagereferences": [
    {
      "documentimageguid": "36gwYCpY7QlbF6tfT9B8ekE1N9Q",
      "imageuri": "https://storage.blob.core.windows.net/container/receipt.jpg"
    }
  ]
}
```

**รองรับ File Types:**
- ✅ **Images**: JPG, PNG
- ✅ **PDF**: รองรับ PDF โดยตรง (ไม่ต้องแปลงเป็นรูปภาพ)
- 🔍 ระบบตรวจจับประเภทไฟล์อัตโนมัติจาก Content-Type header

**ตัวอย่าง PDF:**
```json
{
  "shopid": "36gw9v2oP2Rmg98lIovlQ6Dbcfh",
  "imagereferences": [
    {
      "documentimageguid": "36gwYCpY7QlbF6tfT9B8ekE1N9Q",
      "imageuri": "https://storage.blob.core.windows.net/container/receipt.pdf"
    }
  ]
}
```

#### Example Request
```bash
curl -X POST http://localhost:8080/api/v1/analyze-receipt \
  -H "Content-Type: application/json" \
  -d '{
    "shopid": "36gw9v2oP2Rmg98lIovlQ6Dbcfh",
    "imagereferences": [{
      "documentimageguid": "36gwYCpY7QlbF6tfT9B8ekE1N9Q",
      "imageuri": "https://storage.blob.core.windows.net/container/image.jpg"
    }]
  }'
```

#### Success Response
```json
{
  "status": "success",
  "receipt": {
    "number": "W25101502018171",
    "date": "06/11/2025",
    "vendor_name": "บริษัท บางจากกรีนเนท จำกัด",
    "total": 2320,
    "vat": 151.78
  },
  "accounting_entry": {
    "journal_book_code": "02",
    "journal_book_name": "สมุดรายวันซื้อ",
    "entries": [
      {
        "account_code": "531220",
        "account_name": "ค่าน้ำมัน-ค่าแก๊สรถยนต์",
        "debit": 2320,
        "credit": 0
      },
      {
        "account_code": "111110",
        "account_name": "เงินสดในมือ",
        "debit": 0,
        "credit": 2320
      }
    ],
    "balance_check": {
      "balanced": true,
      "total_debit": 2320,
      "total_credit": 2320
    }
  },
  "template_info": {
    "template_used": true,
    "template_name": "ค่าน้ำมัน",
    "confidence": 100
  },
  "validation": {
    "confidence": { "level": "high", "score": 99 },
    "requires_review": false,
    "ai_explanation": {
      "reasoning": "ใบกำกับภาษี ซื้อน้ำมันเชื้อเพลิง ยอด 2,320 บาท ใช้บัญชีตาม template"
    }
  },
  "metadata": {
    "duration_sec": 15.02,
    "cost_thb": "฿0.07"
  }
}
```

---

## 📁 โครงสร้างโปรเจกต์

```
bill_scan_project/
├── cmd/api/main.go              # Entry point
├── internal/
│   ├── api/                     # HTTP handlers
│   ├── ai/                      # Gemini AI integration
│   ├── processor/               # Image & template processing
│   ├── storage/                 # MongoDB & caching
│   └── common/                  # Shared types
├── configs/config.go            # Configuration
├── docs/                        # Documentation
├── go.mod
└── README.md
```

---

## 📚 เอกสารเพิ่มเติม

- 📖 [Model Configuration Guide](docs/MODEL_CONFIGURATION.md) - อธิบาย phase-specific models และ pricing
- 🏗️ [System Design](docs/SYSTEM_DESIGN.md) - สถาปัตยกรรมและ flow การทำงาน
- 📄 **[PDF Support Documentation](PDF_SUPPORT.md)** - คู่มือรองรับไฟล์ PDF โดยละเอียด
- 🐳 [Docker Deployment](docs/DOCKER_DEPLOY.md) - การ deploy ด้วย Docker
- ⚡ [Rate Limiting Solutions](docs/RATE_LIMITING_SOLUTIONS.md) - แก้ปัญหา API rate limit

---

## 🎓 Key Concepts

### Pure OCR vs Full OCR
- **Old**: สกัด structure ทั้งหมดในครั้งเดียว (60K tokens)
- **New**: อ่านข้อความ → จับคู่ template → วิเคราะห์ (10-17K tokens)

### Template Matching
- **AI-Driven Matching**: AI วิเคราะห์ความเหมือนระหว่างเอกสารกับ template
- **Dual Description System**: 
  - `description`: ชื่อ template หลัก (เช่น "ค่าน้ำมัน")
  - `promptdescription`: บริบทเพิ่มเติมสำหรับ AI (เช่น "ซื้อน้ำมันจากปั๊ม บางจาก, ปตท, เชลล์")
- **Threshold 85%**: confidence ≥ 85% → template-only mode
- **Consistent Entries**: Template มีบัญชีที่กำหนดไว้ → ใช้เหมือนเดิมทุกครั้ง
- **Fuzzy Matching**: ยอมรับความต่างเล็กน้อย (typo, ตัวสะกด) ด้วย similarity > 75%

### Thai Accounting Rules
- แยกแยะ: **บริการ** (ค่าที่ปรึกษา) vs **วัสดุ** (ค่าเบ็ดเตล็ด)
- ค้นหาบัญชีจาก Chart of Accounts แต่ละธุรกิจ
- คำอธิบายเป็นภาษาไทยทั้งหมด

---

## � เอกสารเพิ่มเติม

- 📖 [Model Configuration Guide](docs/MODEL_CONFIGURATION.md) - อธิบาย phase-specific models และ pricing
- 🏗️ [System Design](docs/SYSTEM_DESIGN.md) - สถาปัตยกรรมและ flow การทำงาน
- 🐳 [Docker Deployment](docs/DOCKER_DEPLOY.md) - การ deploy ด้วย Docker
- ⚡ [Rate Limiting Solutions](docs/RATE_LIMITING_SOLUTIONS.md) - แก้ปัญหา API rate limit

---

## �📝 Recent Updates


### v2.4 - PDF Support (Dec 17, 2025)
- ✅ **Native PDF processing** - รองรับ PDF โดยตรงผ่าน Gemini API
- ✅ **Auto file type detection** - ตรวจจับ PDF/Image อัตโนมัติจาก Content-Type
- ✅ **No conversion needed** - ส่ง PDF raw bytes โดยไม่ต้องแปลงเป็นรูปภาพ
- ✅ **Multi-page support** - Gemini ประมวลผล PDF หลายหน้าได้
- ✅ **Unified API** - ใช้ endpoint เดียวกันสำหรับทั้ง Image และ PDF

**Supported Formats:**
- PDF files (application/pdf) - รองรับทั้ง text-layer และ scanned PDF
- Image files (image/jpeg, image/png) - รองรับเหมือนเดิม
- Mixed requests - ส่งทั้ง PDF และ Image ในคำขอเดียวกันได้

📚 **เอกสารเพิ่มเติม**: [PDF Support Documentation](PDF_SUPPORT.md)
### v2.3 - Conditional Model Selection (Dec 16, 2025)
- ✅ **Smart model switching** - Phase 3 เลือก model ตาม template confidence
- ✅ **Cost optimization** - Template-only mode (≥85%) ใช้ Flash-Lite แทน Flash
- ✅ **Performance boost** - ลดเวลาและต้นทุนใน template-only mode ~70%
- ✅ **Better accuracy maintained** - Full mode (<85%) ยังใช้ Flash reasoning เต็มรูปแบบ
- ✅ **Thai comments** - ไฟล์ .env เปลี่ยนเป็นภาษาไทยทั้งหมด

**Cost Improvement:**
- Template-only mode (≥85% confidence): ฿0.08-0.10/request (ลด ~70% จาก v2.2)
- Full mode (<85% confidence): ฿0.30-0.35/request (ใช้ Flash reasoning เต็มรูปแบบ)
- **Smart tradeoff**: ประหยัดเมื่อเป็นไปได้ แต่รักษาคุณภาพเมื่อจำเป็น

### v2.2 - Phase-Specific Models (Dec 16, 2025)
- ✅ **Separated AI models by phase** - OCR, Template Matching, Accounting Analysis
- ✅ **OCR Model upgraded** - 2.5 Flash-Lite (better Thai OCR than 2.0)
- ✅ **Accounting Model upgraded** - 2.5 Flash (better reasoning capability)
- ✅ **Flexible configuration** - Phase-specific pricing in .env
- ✅ **Backward compatible** - Old MODEL_NAME still works as fallback

### v2.1 - Rate Limiting & Reliability (Dec 2025)
- ✅ **Fixed HTTP 429 errors** - Implemented sequential processing (1 worker)
- ✅ **Rate limiter optimization** - 12 tokens with 5s refill (20% safety margin)
- ✅ **Smart retry logic** - 30-90s delay for rate limit errors
- ✅ **Phase-level rate limiting** - All API calls protected
- ✅ **Journal Book selection** - Priority-based rules with 100% accuracy
- ✅ **Improved prompts** - Added concrete examples for AI decision-making

### v2.0 - Token Optimization (Dec 2025)
- ✅ Reduced token usage by 73-80%
- ✅ Added AI template matching
- ✅ Implemented dual-mode processing
- ✅ Enhanced Thai accounting classification
- ✅ Removed prompt_system.go (legacy)

---

Built with ❤️ using Go and Gemini AI
