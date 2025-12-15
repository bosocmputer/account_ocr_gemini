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

---

## 🎯 ภาพรวมระบบ

**Bill Scan API** คือระบบ Backend ที่พัฒนาด้วย Go เพื่อแปลงรูปภาพใบเสร็จ/ใบกำกับภาษีให้เป็นรายการบัญชีอัตโนมัติ โดยใช้ **Gemini AI** วิเคราะห์เอกสาร และจับคู่กับ **Template ทางบัญชี** จาก MongoDB เพื่อสร้างรายการบัญชีที่ถูกต้องตามหลักการบัญชีไทย

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
| **AI** | Gemini 2.5 Flash | Vision AI for OCR & analysis |
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
แก้ไข `configs/config.go`:
```go
const (
    GEMINI_API_KEY = "YOUR_GEMINI_API_KEY"
    MODEL_NAME     = "gemini-2.5-flash"
    MONGODB_URI    = "mongodb://localhost:27017"
    MONGODB_DB     = "your_database"
)
```

### 3. Setup MongoDB
ต้องมี collections:
- `chartOfAccounts` - ผังบัญชี
- `journalBooks` - สมุดรายวัน
- `creditors` - เจ้าหนี้
- `debtors` - ลูกหนี้
- `documentFormate` - Templates ทางบัญชี
- `shopProfile` - ข้อมูลร้านค้า

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

วิเคราะห์รูปภาพใบเสร็จและสร้างรายการบัญชีอัตโนมัติ

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
      "imageuri": "https://storage.blob.core.windows.net/container/image.jpg"
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

## 🎓 Key Concepts

### Pure OCR vs Full OCR
- **Old**: สกัด structure ทั้งหมดในครั้งเดียว (60K tokens)
- **New**: อ่านข้อความ → จับคู่ template → วิเคราะห์ (10-17K tokens)

### Template Matching
- AI วิเคราะห์ความเหมือนระหว่างเอกสารกับ template
- Threshold 85%: confidence ≥ 85% → template-only mode
- Template มีบัญชีที่กำหนดไว้ → ใช้เหมือนเดิมทุกครั้ง

### Thai Accounting Rules
- แยกแยะ: **บริการ** (ค่าที่ปรึกษา) vs **วัสดุ** (ค่าเบ็ดเตล็ด)
- ค้นหาบัญชีจาก Chart of Accounts แต่ละธุรกิจ
- คำอธิบายเป็นภาษาไทยทั้งหมด

---

## 📝 Recent Updates

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
