# 🧾 Bill Scan API - AI Accounting System

> ระบบวิเคราะห์บิลและสร้างรายการบัญชีอัตโนมัติด้วย AI

[![Go Version](https://img.shields.io/badge/Go-1.24.5-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Gemini API](https://img.shields.io/badge/Gemini-2.5--flash-4285F4?style=flat&logo=google)](https://ai.google.dev/)
[![Mistral API](https://img.shields.io/badge/Mistral-OCR--3-FF7000?style=flat)](https://mistral.ai/)
[![MongoDB](https://img.shields.io/badge/MongoDB-6.0-47A248?style=flat&logo=mongodb)](https://www.mongodb.com/)

---

## 🎯 ภาพรวม

ระบบ Backend ที่แปลงรูปภาพใบเสร็จ/บิล **(รองรับ Image และ PDF)** เป็นรายการบัญชีอัตโนมัติด้วย AI

### ✨ จุดเด่น
- 🚀 **ลด Token 80%** - จาก 60K → 10-17K tokens ต่อ request
- 🎯 **Template Matching** - AI จับคู่ template อัจฉริยะ (95-100% accuracy)  
- 🇹🇭 **Thai Accounting** - รองรับหลักการบัญชีไทย + Chart of Accounts
- 💰 **Multi OCR Provider** - เลือกใช้ Mistral หรือ Gemini ตามต้องการ
- ⚡ **Fast & Reliable** - 15-20 วินาที/request พร้อม rate limiting

---

## 🔑 คุณสมบัติ

### 🤖 OCR Providers
- **Mistral OCR** - $2/1K pages, URL-based (แนะนำสำหรับ PDF URLs)
- **Gemini OCR** - Token-based, Image preprocessing
- **Request-based selection** - Frontend ระบุ provider ผ่าน `model` field ใน request body

### 📊 Processing Pipeline
1. **Pure OCR** - อ่านข้อความดิบ (~2K tokens)
2. **Template Matching** - AI จับคู่ template (~1K tokens)  
3. **Accounting Analysis** - สร้างรายการบัญชี (7-14K tokens)

### ✅ Quality & Reliability
- Confidence scoring + Balance validation
- Rate limiting + Smart retry logic
- Sequential processing (หลีกเลี่ยง 429 Error)
- Thai language explanations

---

## 🏗️ สถาปัตยกรรม

```
1. Validation → ตรวจสอบ master data
2. Pure OCR → อ่านข้อความ (Mistral/Gemini)  
3. Template Matching → AI จับคู่ template
4. Accounting Analysis → สร้างรายการบัญชี
   • Template Mode (≥95%) → ใช้ template (~7K tokens)
   • Full Mode (<95%) → วิเคราะห์เต็มรูปแบบ (~14K tokens)
5. Response → Receipt + Accounting + Validation
```

**Token Savings**: 60K → 10-17K tokens (ลด 71-83%)

📚 **รายละเอียดเพิ่มเติม**: [SYSTEM_DESIGN.md](docs/SYSTEM_DESIGN.md) | [MODEL_CONFIGURATION.md](docs/MODEL_CONFIGURATION.md)

---

## 🛠️ Tech Stack

- **Backend**: Go 1.24.5 + Gin Framework
- **OCR**: Mistral OCR 3 / Gemini 2.5 Flash-Lite  
- **AI**: Gemini 2.5 Flash (Template + Accounting)
- **Database**: MongoDB 6.0
- **Cache**: In-memory (5min TTL)

```go
github.com/gin-gonic/gin v1.11.0
github.com/google/generative-ai-go v0.20.1
go.mongodb.org/mongo-driver v1.17.1
```

---

## 🚀 Installation

### Requirements
- Go 1.24.5+, MongoDB 6.0+
- API Keys: Gemini (required), Mistral (optional)

### Quick Start
```bash
git clone <repository>
cd bill_scan_project
go mod download
```

### Configuration
สร้างไฟล์ `.env`:
```env
# API Keys (ต้องมีทั้ง 2 keys)
MISTRAL_API_KEY=your_mistral_key
MISTRAL_MODEL_NAME=mistral-ocr-latest

# Gemini (สำหรับ OCR + Template + Accounting)
GEMINI_API_KEY=your_gemini_key
OCR_MODEL_NAME=gemini-2.5-flash-lite
TEMPLATE_MODEL_NAME=gemini-2.5-flash-lite
ACCOUNTING_MODEL_NAME=gemini-2.5-flash

# MongoDB
MONGO_URI=mongodb://localhost:27017
MONGO_DB_NAME=your_database

# หมายเหตุ: OCR provider (gemini/mistral) ระบุโดย frontend
# ผ่าน field 'model' ใน request body ไม่ได้กำหนดใน .env
```

### 3. Setup MongoDB
MongoDB Collections ที่ต้องมี:
- `chartOfAccounts`, `journalBooks`, `creditors`, `debtors`
- `documentFormate` (Templates), `shopProfile`

📚 **Template Format**: ดู [SYSTEM_DESIGN.md](docs/SYSTEM_DESIGN.md#master-data)

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

## 📡 API

### POST /api/v1/analyze-receipt

**รองรับ**: Image (JPG, PNG) และ PDF

#### Request
```bash
curl -X POST http://localhost:8080/api/v1/analyze-receipt \
  -H "Content-Type: application/json" \
  -d '{
    "shopid": "36gw9v2oP2Rmg98lIovlQ6Dbcfh",
    "model": "mistral",
    "imagereferences": [{
      "documentimageguid": "36gwYCpY7QlbF6tfT9B8ekE1N9Q",
      "imageuri": "https://storage.blob.core.windows.net/container/image.jpg"
    }]
  }'
```

#### Response
```json
{
  "status": "success",
  "receipt": { "vendor_name": "...", "total": 2320 },
  "accounting_entry": {
    "entries": [/* Debit/Credit */],
    "balance_check": { "balanced": true }
  },
  "template_info": { "template_used": true, "confidence": 100 },
  "metadata": { "duration_sec": 15, "cost_thb": "฿0.07" }
}
```

📋 **ตัวอย่างเต็ม**: ดูใน logs ด้านบน หรือ [SYSTEM_DESIGN.md](docs/SYSTEM_DESIGN.md)

---

## 📝 เอกสาร

- 🏗️ [System Design](docs/SYSTEM_DESIGN.md) - สถาปัตยกรรมและ flow การทำงาน
- 📖 [Model Configuration](docs/MODEL_CONFIGURATION.md) - Phase-specific models และ pricing
- 📄 [PDF Support](PDF_SUPPORT.md) - คู่มือรองรับไฟล์ PDF
- 🐳 [Docker Deployment](docs/DOCKER_DEPLOY.md) - การ deploy ด้วย Docker
- ⚡ [Rate Limiting](docs/RATE_LIMITING_SOLUTIONS.md) - แก้ปัญหา API rate limit

---

## 🆕 Updates

**v2.6 - Request-based Model Selection** (Dec 19, 2025)
- ✅ Frontend controls OCR provider via `model` field in request
- ✅ Validation: model must be "gemini" or "mistral"
- ✅ Applies to both /analyze-receipt and /test-template endpoints

**v2.5 - Multi OCR Provider** (Dec 19, 2025)
- ✅ Mistral OCR 3 support ($2/1K pages, URL-based)
- ✅ Separate cost tracking (OCR + AI Processing)

**v2.4 - PDF Support** (Dec 17, 2025)  
- ✅ Native PDF processing + Auto file type detection

**v2.3 - Smart Model Selection** (Dec 16, 2025)
- ✅ Conditional model switching (cost optimization)

**v2.0-2.2** - Token optimization (73-80% reduction), Rate limiting, Phase-specific models

---

Built with ❤️ using Go and Gemini AI
