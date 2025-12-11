# 📋 System Design: AI-Powered Receipt Analysis System

## 🎯 System Overview

**ระบบวิเคราะห์ใบเสร็จอัตโนมัติด้วย AI**

A production-ready Go backend service that automatically analyzes receipt images using Gemini AI, integrates with MongoDB master data, and generates accounting entry recommendations. The system processes receipts in **20-35 seconds** with 90-99% confidence.

**Key Features:**
- ✅ Multi-image support (ใบเสร็จ + สลิป, หรือใบเสร็จหลายหน้า)
- ✅ Thai document type detection (ใบเสร็จรับเงิน vs ใบกำกับภาษี)
- ✅ Confidence scoring (ทุกฟิลด์มี confidence score)
- ✅ N/A policy (AI ซื่อสัตย์เมื่อไม่แน่ใจ)
- ✅ Master data caching (ลด MongoDB queries)
- ✅ Document template matching (ใช้ template ถ้ามี)
- ✅ No draft saving (แค่ return JSON response)

---

## 🎬 User Journey (Phase 1 Only)

```
┌─────────────────────────────────────────────────────────────┐
│                    1. เปิดรูปบิล 📸                          │
│              User เลือกรูปจาก Gallery/Camera                 │
│              อัปโหลดไปยัง Azure Blob Storage                 │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│            2. กดปุ่ม "ส่งให้ AI วิเคราะห์" 🤖              │
│   Frontend: POST /api/v1/analyze-receipt                    │
│   Body: { shopid, imagereferences[] }                       │
│   รองรับ: 1 รูป หรือ หลายรูป (ใบเสร็จ+สลิป)                │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│              3. Backend ประมวลผล (20-35 วินาที)             │
│                                                              │
│   Step 0: Master Data Validation (< 1s):                    │
│   • ตรวจสอบ shopid มี master data หรือไม่                   │
│   • ถ้าไม่มี → reject ทันที (ประหยัด token!)               │
│   • ดึงจาก cache ถ้ามี (TTL = 5 นาที)                       │
│                                                              │
│   Step 1: Download Images (2-3s):                           │
│   • ดาวน์โหลดรูปจาก Azure Blob Storage                      │
│   • รองรับหลายรูป (ใบเสร็จ + สลิป, หรือหลายหน้า)           │
│                                                              │
│   Step 2: Full OCR Processing (10-15s):                     │
│   • Gemini 2.5 Flash Full OCR                               │
│   • Extract: items, amounts, dates, receipt details         │
│   • Confidence scoring ทุกฟิลด์                             │
│   • N/A policy (ไม่เดาถ้าไม่แน่ใจ)                          │
│   • Image quality validation                                │
│                                                              │
│   Step 3: Multi-Image Accounting Analysis (15-20s):         │
│   • วิเคราะห์ความสัมพันธ์ของรูป (ใบเสร็จ+สลิป?)            │
│   • เลือก document template (ถ้ามี)                         │
│   • AI เลือกรายการบัญชีที่เหมาะสม                           │
│   • Validate double-entry balance                           │
│   • Calculate confidence scores                             │
│   • **ไม่บันทึก draft** → แค่ return JSON                  │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│           4. ได้ข้อมูลกลับมาทันที (JSON Response) ✅         │
│   Response JSON (NO draft_id - direct response):            │
│                                                              │
│   • Status: "success"                                        │
│   • Receipt Data:                                            │
│     - เลขที่: 06131560570                                    │
│     - วันที่: 06/10/2020                                     │
│     - Vendor: Makro Store                                    │
│     - Tax ID: 0105536034923                                  │
│     - Items: 2 รายการ                                       │
│     - Total: 1,205.61 ฿                                     │
│     - VAT: 84.39 ฿                                          │
│     - Grand Total: 1,290.00 ฿                               │
│                                                              │
│   • AI Analysis:                                             │
│     - Document Type: tax_invoice (99% confidence)           │
│     - Transaction Type: asset_purchase (95%)                │
│     - Payment Method: cash (90%)                            │
│     - Has VAT: true                                          │
│                                                              │
│   • Accounting Entry:                                        │
│     - สมุดรายวัน: "สมุดรายวันซื้อ" (95% confidence)        │
│     - Entries (3 รายการ):                                   │
│   • Metadata:                                                │
│     - Model: gemini-2.5-flash                                │
│     - Processing Time: 25,400 ms                             │
│     - Total Tokens: 12,500                                   │
│                                                              │
│   • Multi-Image Analysis (ถ้ามีหลายรูป):                    │
│     - Document Relationship: "receipt_with_payment_slip"    │
│     - Merged Data: รวมข้อมูลจากทุกรูป                       │
│     - Confidence: 95%                                        │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│         5. Frontend แสดงผลให้ User ตรวจสอบ 🔍               │
│   • แสดงรายละเอียดใบเสร็จ (จากรูปที่อัปโหลด)                │
│   • แสดงรายการบัญชีที่ AI แนะนำ พร้อม confidence score      │
│   • แสดง warning/suggestion (ถ้ามี)                         │
│   • User ตรวจสอบและอนุมัติ (หรือแก้ไข) ใน Frontend          │
│   • Frontend บันทึกเข้า accounting system ของตัวเอง         │
│                                                              │
│   ⚠️ หมายเหตุ:                                              │
│   • Backend ไม่เก็บ draft (stateless)                       │
│   • Frontend รับผิดชอบการจัดการ draft                       │
│   • User สามารถ re-analyze รูปเดิมได้ตลอด                   │
└─────────────────────────────────────────────────────────────┘│
│   • แสดงรายการบัญชีที่ AI แนะนำ พร้อม confidence score      │
│   • User สามารถเก็บ draft_id ไว้แก้ไขภายหลัง                │
│                                                              │
│   *** Phase 2 Draft Management APIs ***                     │
│   (Coming Soon - not implemented in Phase 1):               │
│   • GET /api/v1/draft-entries/:id - ดึง draft              │
│   • PUT /api/v1/draft-entries/:id - แก้ไข draft            │
│   • POST /api/v1/approve-entry/:id - อนุมัติบันทึกบัญชี    │
└─────────────────────────────────────────────────────────────┘
```

---

## 🏗️ System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Frontend (Flutter/Web)                       │
│  • เลือกรูป → Upload to Azure Blob → ส่ง imageuri ไปยัง Backend│
└─────────────────────────────────────────────────────────────────┘
                                ↓ HTTP/JSON
┌─────────────────────────────────────────────────────────────────┐
│                   Go Backend (Port 8080)                         │
│                    GIN_MODE=release                              │
│                                                                  │
│  📦 Core Files (12 files):                                      │
│  • main.go              - Entry point, Gin router               │
│  • handlers.go          - HTTP handlers (793 lines)             │
│  • gemini.go            - Gemini API integration (959 lines)    │
│  • prompt_system.go     - ⭐ OCR prompts (ภาษาไทย)              │
│  • prompts.go           - Accounting prompts                    │
│  • mongodb.go           - Database operations                   │
│  • cache.go             - Master data caching (TTL=5min)        │
│  • config.go            - Environment config                    │
│  • imageprocessor.go    - Image preprocessing                   │
│  • gemini_retry.go      - Retry logic, error handling           │
│  • request_context.go   - Logging, tracking                     │
│  • template_extractor.go- Template matching                     │
│                                                                  │
│  🌐 API Endpoints:                                              │
│  • GET  /health                    - Health check               │
│  • POST /api/v1/analyze-receipt    - Full analysis (20-35s)     │
│                                                                  │
│  ⚡ Performance Features:                                       │
│  • No Quick OCR (removed for speed)                             │
│  • Master data caching (5min TTL)                               │
│  • Image preprocessing (sharpen, contrast)                      │
│  • Graceful shutdown (SIGTERM/SIGINT)                           │
│  • Request timeout (5 minutes max)                              │
│  • CORS with configurable origins                               │
│  • Minimal logging (production-ready)                           │
└─────────────────────────────────────────────────────────────────┘
          ↓                    ↓                    ↓
┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
│   Azure Blob     │  │  Gemini 2.5 AI   │  │    MongoDB       │
│    Storage       │  │  (Flash)         │  │  (smldevdb)      │
│                  │  │                  │  │                  │
│ • รูปใบเสร็จ      │  │ • Full OCR       │  │ Collections:     │
│ • Download       │  │ • Multi-Image    │  │ • chartofaccounts│
│   via HTTP       │  │ • Accounting AI  │  │ • journalBooks   │
│ • Multi-image    │  │ • Vision API     │  │ • creditors      │
│   support        │  │ • Thai language  │  │ • debtors        │
└──────────────────┘  │ • Confidence     │  │ • documentFormate│
                      │   scoring        │  │   (templates)    │
                      └──────────────────┘  └──────────────────┘
```

---

## 📡 API Specification (Phase 1)

### 1. Health Check

```http
GET /health
```

**Response:**
```json
{
  "status": "ok",
  "service": "go-receipt-parser",
  "version": "1.0.0"
}
```

---

### 2. Analyze Receipt (Main API)

```http
POST /api/v1/analyze-receipt
Content-Type: application/json
```

**Request Body:**
```json
{
  "shopid": "SHOP001",
  "imagereferences": [
    {
      "documentimageguid": "550e8400-e29b-41d4-a716-446655440000",
      "imageuri": "https://dedeposblosstorage.blob.core.windows.net/dedeposdevcontainer/receipts/image.jpg"
    }
  ]
}
```

**Response (200 OK):**
```json
{
  "shopid": "SHOP001",
  "status": "success",
  "request_id": "req_abc123xyz",
  
  "receipt_data": {
    "receipt_number": "06131560570",
    "invoice_date": "06/10/2020",
    "vendor_name": "Makro Store",
    "vendor_taxid": "0105536034923",
    "total_amount": 1205.61,
    "vat_amount": 84.39,
    "grand_total": 1290.00,
    "items": [
      {
        "product_id": "8851788000015",
        "description": "เตาแก๊ส",
        "quantity": 1,
        "unit_price": 1205.61,
        "total_price": 1205.61
      }
    ]
  },
  
  "ai_analysis": {
    "document_type": "tax_invoice",
    "transaction_type": "asset_purchase",
    "has_vat": true,
    "payment_method": {
      "detected": "cash",
      "confidence": 90
    },
    "business_context": {
      "category": "equipment",
      "confidence": 95
    },
    "reasoning": "ซื้ออุปกรณ์ (เตาแก๊ส) เป็นสินทรัพย์..."
  },
  
  "accounting_entry": {
    "journal_book": {
      "id": "JB_PURCHASE",
      "name": "สมุดรายวันซื้อ",
      "confidence": 95
    },
    "entries": [
      {
        "type": "Dr",
        "account_id": "1450",
        "account_name": "อุปกรณ์",
        "amount": 1205.61,
        "confidence": 92,
        "reasoning": "ซื้อสินทรัพย์ถาวร (เตาแก๊ส)"
      },
      {
        "type": "Dr",
        "account_id": "1171",
        "account_name": "ภาษีซื้อรอเรียกคืน",
        "amount": 84.39,
        "confidence": 98,
        "reasoning": "VAT 7% ของการซื้อ"
      },
      {
        "type": "Cr",
        "account_id": "1111",
        "account_name": "เงินสด",
        "amount": 1290.00,
        "confidence": 95,
        "reasoning": "ชำระด้วยเงินสด"
      }
    ],
    "balance_check": {
      "passed": true,
      "message": "Balanced: Dr=1,290.00, Cr=1,290.00"
    },
    "creditor": null,
    "description": "ซื้ออุปกรณ์เตาแก๊สจาก Makro"
  },
  
  "validation": {
    "overall_confidence": {
      "level": "high",
      "score": 99
    },
    "requires_review": false,
    "warnings": [],
    "suggestions": []
  },
  
  
  "metadata": {
    "model_name": "gemini-2.5-flash",
    "prompt_tokens": 6500,
    "candidates_tokens": 6000,
    "total_tokens": 12500,
    "processing_time_ms": 25400,
    "api_version": "v1",
    "processed_at": "2024-12-11T13:00:00Z"
  },
  
  "multi_image_analysis": {
    "total_images": 2,
    "relationship": "receipt_with_payment_slip",
    "confidence": 95,
    "merged_data": true,
    "note": "รูปที่ 1: ใบเสร็จ, รูปที่ 2: สลิปโอนเงิน"
  },
  
  "image_references": [
    {
      "documentimageguid": "550e8400-e29b-41d4-a716-446655440000",
      "imageuri": "https://...",
      "image_index": 0
    },
    {
      "documentimageguid": "550e8400-e29b-41d4-a716-446655440001",
      "imageuri": "https://...",
      "image_index": 1
    }
  ]
```

**Error Responses:**

```json
// 400 Bad Request - Missing master data
{
  "status": "error",
  "error": "master_data_not_found",
  "message": "ไม่พบข้อมูล Master Data สำหรับ Shop นี้",
  "details": {
    "shopid": "SHOP001",
    "accounts_found": 0,
    "journal_books_found": 0
  },
  "required": {
    "chart_of_accounts": "ต้องมีอย่างน้อย 1 รายการ",
    "journal_books": "ต้องมีอย่างน้อย 1 รายการ"
  }
}

// 400 Bad Request - Low image quality
{
  "status": "rejected",
  "reason": "image_quality_insufficient",
  "message": "คุณภาพภาพไม่เพียงพอ กรุณาถ่ายใหม่ให้ชัดเจน",
  "failed_images": [
    {
      "documentimageguid": "...",
      "image_index": 0,
      "imageuri": "...",
      "issues": [
        {
          "field": "overall_confidence",
          "issue": "ความมั่นใจโดยรวมต่ำเกินไป",
          "current_value": "65",
          "min_required": "70"
        }
      ]
    }
  ],
  "suggestions": [
    "ถ่ายภาพในที่แสงสว่างเพียงพอ",
    "ให้ใบเสร็จอยู่ในกรอบภาพทั้งหมด",
    "หลีกเลี่ยงเงาและแสงสะท้อน"
  ]
}

// 408 Request Timeout
{
  "error": "Processing timeout",
  "message": "Receipt is too complex and processing exceeded 5 minutes",
  "request_id": "req_abc123"
}

// 500 Internal Server Error
{
  "error": "OCR processing failed",
  "details": "Gemini API error: rate limit exceeded",
  "request_id": "req_abc123"
}
```

---

## 🗄️ Database Schema (MongoDB)

### Collections Used (All Read Only)

#### 1. `chartofaccounts` (Read Only, Cached)
```javascript
{
  "_id": ObjectId,
  "shopid": "SHOP001",
  "accountcode": "111110",
  "accountname": "เงินสดในมือ",
  "accounttype": "สินทรัพย์",
  "normalbalance": "Dr"
}
```

#### 2. `journalBooks` (Read Only, Cached)
```javascript
{
  "_id": ObjectId,
  "shopid": "SHOP001",
  "journalbookcode": "PJ01",
  "journalbookname": "สมุดรายวันซื้อ",
  "description": "บันทึกการซื้อสินค้า/บริการ"
}
```

#### 3. `creditors` (Read Only, Cached)
```javascript
{
  "_id": ObjectId,
  "shopid": "SHOP001",
  "creditor_code": "CR001",
  "creditor_name": "Makro Store",
  "taxid": "0105536034923",
  "creditterm": 30
}
```

#### 4. `documentFormate` (Read Only, Optional)
```javascript
{
  "_id": ObjectId,
  "shopid": "SHOP001",
  "description": "ค่าน้ำมัน",
  "details": [
    {
      "accountcode": "535010",
      "detail": "ค่าน้ำมันเชื้อเพลิง"
    },
    {
      "accountcode": "111110",
      "detail": "เงินสดในมือ"
    }
  ]
}
```

**⚠️ หมายเหตุ:**
- **ไม่มี collection สำหรับเก็บ draft** (Backend เป็น stateless)
- ทุก collection มี **cache** (TTL = 5 นาที)
- Query ด้วย `shopid` filter เสมอ
- Frontend รับผิดชอบการจัดการ draft
```

---

## 🧠 AI Processing Pipeline (Optimized - No Quick OCR)

### Step 0: Master Data Validation (< 1 second)

**Purpose:** Validate master data exists before processing (saves tokens!)

**Logic:**
- Check cache first (TTL = 5 minutes)
- If not in cache, query MongoDB with `shopid` filter
- Validate: accounts > 0 AND journal_books > 0
- **If validation fails → reject immediately** (don't waste tokens)

**Cache Structure:**
```go
type MasterDataCache struct {
    Accounts     []bson.M
    JournalBooks []bson.M
    Creditors    []bson.M
    LoadedAt     time.Time
    ShopID       string
}
```

### Step 1: Download Images (2-3 seconds)

**Purpose:** Download all images from Azure Blob Storage

**Logic:**
- Support multi-image (1-5 images)
- Download via HTTP GET
- Save to `/uploads/` temporarily
- Track each image with GUID and index

### Step 2: Full OCR Processing (10-15 seconds)

**Purpose:** Extract complete receipt details with high accuracy

**Prompt Source:** `prompt_system.go` - `GetOCRPrompt()` (ภาษาไทย!)

**Key Features:**
- ✅ Thai language support (อ่านภาษาไทยได้แม่นยำ)
- ✅ Confidence scoring (ทุกฟิลด์มี level + score)
- ✅ N/A policy (อย่าเดาถ้าไม่แน่ใจ < 85%)
- ✅ Document type detection (ใบเสร็จรับเงิน vs ใบกำกับภาษี)
- ✅ Barcode reading (EAN-13, 13 digits)
- ✅ Price extraction (unit_price vs total_price)

**Image Preprocessing:**
- Sharpen (เพิ่มความคมชัด)
- Contrast adjustment (ปรับความเข้มตัด)
- Brightness optimization (ปรับความสว่าง)
- Grayscale conversion (แปลงขาวดำ)

**Quality Validation:**
- Overall confidence ≥ 70% (reject if lower)
- Check for N/A values
- Validate required fields

**Output:**
```json
{
  "status": "success",
  "document_type_header": "ใบเสร็จรับเงิน/ใบกำกับภาษี",
  "receipt_number": "06131560570",
  "invoice_date": "06/10/2020",
  "total_amount": 1205.61,
  "vat_amount": 84.39,
  "items": [...],
  "validation": {
    "overall_confidence": { "level": "high", "score": 99 },
    "requires_review": false,
    "field_confidence": {...}
  }
}
```

### Step 3: Multi-Image Accounting Analysis (15-20 seconds)

**Purpose:** Analyze multiple images and create merged accounting entries

**Prompt Source:** `prompts.go` - `BuildMultiImageAccountingPrompt()`

**Multi-Image Logic:**
1. **Document Relationship Detection:**
   - Receipt + Payment Slip (ใบเสร็จ + สลิป)
   - Multi-page Receipt (ใบเสร็จหลายหน้า)
   - Separate Receipts (ใบเสร็จแยกกัน)

2. **Template Matching (Optional):**
   - Check `documentFormate` collection
   - Match by description (e.g., "ค่าน้ำมัน", "ค่าไฟ")
   - Use template accounts if match found (99% confidence!)

3. **Master Data Integration:**
   - All accounts from cache (filtered by shopid)
   - All journal books from cache
   - All creditors from cache
   - Business context from `business_context.md`

4. **AI Analysis:**
   - Document type (paid/unpaid)
   - Select appropriate accounts
   - Select journal book
   - Match creditor (if applicable)
   - Generate descriptions
   - Calculate confidence scores

**Output:**
```json
{
  "document_analysis": {
    "relationship": "receipt_with_payment_slip",
    "confidence": 95
  },
  "journal_book_code": "PJ01",
  "journal_book_name": "สมุดรายวันซื้อ",
  "journal_entries": [
    {
      "account_code": "535093",
      "account_name": "ค่าเบ็ดเตล็ด",
      "debit": 1205.61,
      "credit": 0,
      "description": "ซื้ออุปกรณ์",
      "confidence": 92
    },
    ...
  ],
  "creditor": null,
  "balance_check": {
    "passed": true,
    "total_debit": 1290.00,
    "total_credit": 1290.00
  }
}
```

---

## ⚙️ Production Configuration

### Environment Variables

```bash
# Server
GIN_MODE=release
PORT=8080

# MongoDB
MONGO_URI=mongodb://103.13.30.32:27017
MONGO_DB_NAME=smldevdb

# Gemini AI
GEMINI_API_KEY=your-api-key-here
MODEL_NAME=gemini-2.5-flash

# CORS
ALLOWED_ORIGINS=https://your-frontend-domain.com

# Image Processing
ENABLE_IMAGE_PREPROCESSING=true
MAX_IMAGE_DIMENSION=2000

# Performance Optimization
ENABLE_QUICK_OCR=false              # Default: skip Quick OCR (save time)
FULL_OCR_TIMEOUT=45                 # 45 seconds
ACCOUNTING_TIMEOUT=60               # 60 seconds
PARALLEL_PROCESSING=true            # Enable parallel image processing

# Timeouts
REQUEST_TIMEOUT=300s                # 5 minutes max
GRACEFUL_SHUTDOWN_TIMEOUT=30s
```

### Server Specifications

- **Request Timeout:** 5 minutes per request (complex receipts)
- **Read Timeout:** 10 seconds
- **Write Timeout:** 3 minutes
- **Max Header Bytes:** 1MB
- **Graceful Shutdown:** 30 seconds
- **Cache TTL:** 5 minutes (master data)

### Performance Metrics (After Optimization)

**Processing Time:**
- **Total:** 20-35 seconds (ลดลงจาก 36-47 วินาที)
- **Step 0: Master Data Validation:** < 1 second (cached)
- **Step 1: Download Images:** 2-3 seconds
- **Step 2: Full OCR:** 10-15 seconds
- **Step 3: Accounting Analysis:** 15-20 seconds
- **No Quick OCR:** Saved 3-5 seconds! ⚡

**Accuracy:**
- **Confidence Scores:** 90-99% typical
- **Success Rate:** 95%+ for Thai receipts
- **N/A Rate:** ~5% (AI honest when uncertain)

**Resource Usage:**
- **Token Usage:** 10,000-15,000 tokens per receipt (ลดลง!)
- **Cache Hit Rate:** ~80% (master data)
- **Memory:** ~50MB per request
- **CPU:** Moderate (image preprocessing)

---

## 🚀 Deployment Guide

### Docker Deployment (Recommended)

```dockerfile
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o receipt-parser .

FROM alpine:latest
RUN apk --no-cache add ca-certificates

WORKDIR /root/
COPY --from=builder /app/receipt-parser .

EXPOSE 8080
CMD ["./receipt-parser"]
```

```bash
# Build
docker build -t receipt-parser:latest .

# Run
docker run -d \
  --name receipt-parser \
  -p 8080:8080 \
  -e GEMINI_API_KEY=your-key \
  -e MONGO_URI=mongodb://host:27017 \
  -e ALLOWED_ORIGINS=https://yourdomain.com \
  receipt-parser:latest
```

### Manual Deployment

```bash
# Install Go 1.24+
# Clone repository
git clone https://github.com/your-org/receipt-parser.git
cd receipt-parser

# Install dependencies
go mod download

# Build
go build -o receipt-parser .

# Set environment variables
export GIN_MODE=release
export GEMINI_API_KEY=your-key
export MONGO_URI=mongodb://103.13.30.32:27017
export ALLOWED_ORIGINS=https://yourdomain.com

# Run
./receipt-parser
```

---

## 📊 Monitoring & Logging

### Log Levels (Production)

**Enabled:**
- Server start/stop
- Fatal errors
- API request failures
- MongoDB connection errors
- Gemini API errors

**Disabled:**
- Debug messages
- Verbose OCR logs
- Phase completion logs
- Token usage details
- File operation logs

### Health Check

```bash
curl http://localhost:8080/health
```

### Process Monitoring

```bash
# Check if server is running
ps aux | grep receipt-parser

# View logs
tail -f /var/log/receipt-parser.log

# Monitor requests
# (Implement custom middleware for request tracking)
```

---

## 🔐 Security Considerations

1. **CORS:** Configure `ALLOWED_ORIGINS` for production frontend domain
2. **API Keys:** Store `GEMINI_API_KEY` in secure secret management (e.g., AWS Secrets Manager)
3. **MongoDB:** Use authentication and TLS in production
4. **Rate Limiting:** Implement rate limiting middleware (not included in Phase 1)
5. **Input Validation:** All inputs validated before processing
6. **Timeout Protection:** 2-minute request timeout prevents resource exhaustion

---

## 🛠️ Troubleshooting

### Common Issues

**1. Request Timeout (408)**
- Receipt image too large (>5MB)
- Gemini API slow response
- Solution: Resize images before upload, check API quota

**2. Low Confidence Scores (<80%)**
- Poor image quality (blurry, dark)
- Non-standard receipt format
- Solution: Improve image preprocessing, add more examples

**3. Incorrect Account Selection**
- Limited master data
- Ambiguous transaction type
- Solution: Expand account descriptions, improve Smart Filter

**4. MongoDB Connection Failed**
- Network issues
- Wrong credentials
- Solution: Check firewall, verify MONGO_URI

---
## 📈 Future Enhancements

### 1. Performance Optimization
- [ ] Implement Redis for distributed caching
- [ ] Add rate limiting per shopid
- [ ] Optimize image compression algorithms
- [ ] Parallel OCR for multi-image (currently sequential)

### 2. AI Improvements
- [ ] Fine-tune Gemini model with Thai receipts
- [ ] Add feedback loop (user corrections → improve AI)
- [ ] Support more document types (credit notes, purchase orders)
- [ ] Improve confidence scoring algorithm

### 3. Feature Additions
- [ ] Batch processing API (multiple receipts at once)
- [ ] Webhook support (notify when analysis complete)
- [ ] Export to PDF with annotations
- [ ] Mobile SDK (iOS/Android)

### 4. Enterprise Features
- [ ] Multi-tenant isolation
- [ ] Audit logs
- [ ] Role-based access control
- [ ] SLA monitoring and alerting
- Export to accounting software

---

## 📞 Support
---

## 🎉 Recent Changes

### v1.1.0 (December 11, 2024)
- ✅ **Removed Quick OCR Phase** - Saved 3-5 seconds per request
- ✅ **Added prompt_system.go** - Thai language prompts (easy to read/edit)
- ✅ **Master data caching** - 5-minute TTL, ~80% cache hit rate
- ✅ **Improved error handling** - Better user-friendly messages
- ✅ **No draft saving** - Backend is now stateless
- ✅ **Updated to Gemini 2.5 Flash** - Better performance
- ✅ **Processing time:** 20-35 seconds (down from 36-47)

### v1.0.0 (December 9, 2024)
- Initial production release
- Full OCR + Accounting analysis
- Multi-image support
- Thai document type detection

---

**Version:** 1.1.0 (Production Ready - Optimized)  
**Last Updated:** December 11, 2024  
**Tech Stack:** Go 1.24, Gin, Gemini 2.5 Flash, MongoDB  
**Maintained By:** Development Teamyourdomain.com/docs  
**Issue Tracker:** https://github.com/your-org/receipt-parser/issues

---

**Version:** 1.0.0 (Phase 1 - Production Ready)  
**Last Updated:** December 9, 2024  
**Maintained By:** Development Team
