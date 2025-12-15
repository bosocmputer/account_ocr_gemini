# 🚨 แนวทางแก้ปัญหา Rate Limiting (Gemini API 15 RPM)

**วันที่**: 13 ธันวาคม 2025 (Updated: 15 ธันวาคม 2025)
**ปัญหา**: Gemini API มี limit 15 RPM แต่ 1 OCR request ใช้ 3 API calls = รองรับได้แค่ 5 requests/minute
**สถานะ**: ✅ **แก้ไขแล้ว** - Implemented Option 1 (Sequential Processing) + Rate Limiter Optimization

---

## 📊 สถานการณ์ปัจจุบัน

### ข้อจำกัดระบบ
- **Gemini API Limit**: 15 Requests Per Minute (RPM)
- **1 OCR Request** = 3 API calls:
  1. Pure OCR Extraction (~5-10 วิ)
  2. Template Matching (~1-2 วิ)
  3. Accounting Analysis (~10-15 วิ)
- **Throughput สูงสุด**: 5 requests/minute = 300 requests/hour
- **ราคา**: ฟรี (gemini-2.0-flash-lite)

### ปัญหาเมื่อมี User เยอะ
| Users พร้อมกัน | API Calls ต้องการ | เกิน Limit? |
|----------------|-------------------|-------------|
| 5 users        | 15 calls          | ✅ พอดี     |
| 6 users        | 18 calls          | ❌ Error 429 |
| 10 users       | 30 calls          | ❌ Error 429 |
| 20 users       | 60 calls          | ❌ Error 429 |

---

## 🎯 Solution Options (เรียงตามความเหมาะสม)

---

## ✅ Option 1: Queue System with In-Memory Queue

### 📝 ภาพรวม
เก็บ requests ใน Queue และ process ทีละ 5 requests/minute ตาม rate limit

### 🔧 Implementation Plan

#### Step 1: สร้าง Queue Manager
```go
// internal/queue/request_queue.go
type RequestQueue struct {
    queue       []QueueItem
    processing  map[string]*ProcessingItem
    mu          sync.RWMutex
    rateLimiter *ratelimit.RateLimiter
}

type QueueItem struct {
    RequestID    string
    ShopID       string
    Images       []string
    EnqueuedAt   time.Time
    Position     int
    StatusChan   chan ProcessingStatus
}

type ProcessingStatus struct {
    Status      string // "queued", "processing", "completed", "failed"
    Position    int
    TotalQueue  int
    EstimateWait time.Duration
    Result      interface{}
    Error       error
}
```

#### Step 2: API Endpoints
- `POST /api/v1/analyze-receipt/async` - Submit to queue, return tracking ID
- `GET /api/v1/analyze-receipt/status/:requestId` - Check status
- `GET /api/v1/analyze-receipt/result/:requestId` - Get result

#### Step 3: Worker Pool
```go
func (q *RequestQueue) StartWorkers(numWorkers int) {
    for i := 0; i < numWorkers; i++ {
        go q.worker()
    }
}

func (q *RequestQueue) worker() {
    for {
        item := q.Dequeue()
        if item == nil {
            time.Sleep(1 * time.Second)
            continue
        }
        
        // Process with rate limiting
        q.rateLimiter.WaitForRateLimit()
        result, err := processOCR(item)
        
        // Update status
        q.UpdateResult(item.RequestID, result, err)
    }
}
```

### 📈 ข้อดี
- ✅ **ควบคุม throughput ได้แม่นยำ** - ไม่เกิด Error 429
- ✅ **User experience ดี** - รู้สถานะและเวลาที่ต้องรอ
- ✅ **ไม่มีค่าใช้จ่ายเพิ่ม** - ใช้ free tier
- ✅ **Scalable** - เพิ่ม workers ได้ตาม API keys
- ✅ **Fair processing** - FIFO queue

### 📉 ข้อเสีย
- ❌ **User ต้องรอ** - ถ้าคิวยาว อาจรอ 1-2 นาที
- ❌ **ต้องพัฒนา** - Queue system + Status API
- ❌ **Frontend ต้องปรับ** - Polling หรือ WebSocket
- ❌ **Memory usage** - Queue เก็บใน RAM

### 💰 ต้นทุน
- **Development**: 3-5 วัน
- **Operational**: ฟรี (ใช้ free tier API)
- **Infrastructure**: ไม่ต้องเพิ่ม

### 🎯 เหมาะกับ
- ✅ Service ที่ user ยอมรอได้ (ไม่เร่งด่วน)
- ✅ ไม่มี budget สำหรับ paid API
- ✅ มี development time 3-5 วัน

---

## 💰 Option 2: Upgrade to Paid Tier

### 📝 ภาพรวม
เปลี่ยนจาก Gemini Flash Lite (ฟรี) → Gemini Flash/Pro (เสียเงิน) เพื่อเพิ่ม rate limit

### 📊 แผนราคา (Gemini 2.0)

| Plan | Rate Limit | ราคา/1M tokens | ค่าใช้จ่าย/เดือน (ประมาณ) |
|------|-----------|---------------|--------------------------|
| **Flash Lite** (current) | 15 RPM | ฟรี | ฿0 |
| **Flash** | 60 RPM | $0.075/$0.30 | ~฿500-1,500 |
| **Pro** | 360 RPM | $1.25/$5.00 | ~฿5,000-15,000 |

### 🔢 คำนวณต้นทุน
สมมติ 1,000 requests/เดือน:
- Input: 1,000 × 17,000 tokens = 17M tokens → ฿40
- Output: 1,000 × 2,000 tokens = 2M tokens → ฿20
- **รวม: ~฿60-100/เดือน** (Flash tier)

### 🔧 Implementation
1. เปลี่ยน API key เป็น paid tier
2. ปรับ rate limiter config
3. ไม่ต้องเปลี่ยน code อื่น

### 📈 ข้อดี
- ✅ **แก้ตรงจุด** - เพิ่ม capacity ทันที
- ✅ **ไม่ต้องเปลี่ยน architecture**
- ✅ **Response time เท่าเดิม** - user ไม่ต้องรอ
- ✅ **Official solution** - stable, reliable
- ✅ **Implementation เร็ว** - 1 ชั่วโมง

### 📉 ข้อเสีย
- ❌ **มีค่าใช้จ่ายต่อเนื่อง** - ฿60-1,500/เดือน
- ❌ **ยังมี limit** - Flash = 60 RPM (20 concurrent requests)

### 💰 ต้นทุน
- **Development**: 1 ชั่วโมง
- **Operational**: ฿60-1,500/เดือน
- **Break-even**: ถ้าเก็บเงิน user ≥฿100/เดือน

### 🎯 เหมาะกับ
- ✅ มี budget ≥฿1,000/เดือน
- ✅ Service เก็บเงิน user
- ✅ ต้องการ quick fix ภายใน 1 วัน

---

## 🔄 Option 3: Multiple API Keys Rotation

### 📝 ภาพรวม
ใช้หลาย API keys หมุนเวียนกัน เพื่อเพิ่ม total rate limit

### 🔧 Implementation

```go
// internal/ai/key_rotation.go
type KeyRotator struct {
    keys        []string
    currentIdx  int
    mu          sync.Mutex
    keyLimiters map[string]*ratelimit.RateLimiter
}

func (kr *KeyRotator) GetNextKey() string {
    kr.mu.Lock()
    defer kr.mu.Unlock()
    
    key := kr.keys[kr.currentIdx]
    kr.currentIdx = (kr.currentIdx + 1) % len(kr.keys)
    
    // Wait for rate limit on this key
    kr.keyLimiters[key].WaitForRateLimit()
    
    return key
}
```

### 📊 Capacity Scaling
| จำนวน Keys | Total RPM | Concurrent Requests |
|------------|-----------|---------------------|
| 1 key      | 15 RPM    | 5 requests          |
| 3 keys     | 45 RPM    | 15 requests         |
| 5 keys     | 75 RPM    | 25 requests         |
| 10 keys    | 150 RPM   | 50 requests         |

### 📈 ข้อดี
- ✅ **เพิ่ม capacity แบบ linear**
- ✅ **ใช้ free tier** - ไม่ต้องจ่ายค่า API
- ✅ **Implementation ง่าย** - แค่ rotate keys
- ✅ **Scalable** - เพิ่ม keys ได้เรื่อยๆ

### 📉 ข้อเสีย
- ❌ **ต้องจัดการหลาย accounts** - Google accounts แยกกัน
- ❌ **Against ToS** - อาจถูก ban ถ้า Google detect
- ❌ **Maintenance overhead** - keys หมดอายุแยกกัน
- ❌ **Risk สูง** - ถ้า 1 key ถูก ban, กระทบทั้งระบบ

### 💰 ต้นทุน
- **Development**: 2-3 วัน
- **Operational**: ฟรี (แต่ต้องจัดการหลาย accounts)
- **Risk**: อาจถูก ban

### 🎯 เหมาะกับ
- ⚠️ **ไม่แนะนำ** สำหรับ production (violate ToS)
- ✅ Development/Testing environment เท่านั้น

---

## ⚙️ Option 4: Optimize API Calls (3→2 calls)

### 📝 ภาพรวม
รวม Pure OCR + Template Matching เป็น 1 call เพื่อลดจำนวน API calls

### 🔧 Implementation

#### Before (3 calls):
```
Request → Pure OCR (call 1) → Template Matching (call 2) → Accounting (call 3) → Response
```

#### After (2 calls):
```
Request → OCR + Template (call 1) → Accounting (call 2) → Response
```

### 📊 ผลลัพธ์
- **Throughput**: 5 → 7.5 requests/minute (+50%)
- **API calls**: ลด 33%
- **Cost**: ลด 33%
- **Response time**: เร็วขึ้น 2-3 วินาที

### 🔧 ต้องแก้
```go
// Redesign prompt to return both OCR + Template in 1 response
{
    "document_text": "...",
    "matched_template": {
        "template_id": "...",
        "confidence": 95
    }
}
```

### 📈 ข้อดี
- ✅ **เพิ่ม throughput 50%**
- ✅ **ลด cost 33%**
- ✅ **Response time เร็วขึ้น**
- ✅ **ใช้ free tier ได้นานขึ้น**

### 📉 ข้อเสีย
- ❌ **ต้อง redesign prompt** - ซับซ้อนขึ้น
- ❌ **Accuracy อาจลด** - 1 prompt ทำ 2 งาน
- ❌ **Testing ใหม่** - ต้องทดสอบ accuracy
- ❌ **Development time** - 3-4 วัน

### 💰 ต้นทุน
- **Development**: 3-4 วัน
- **Operational**: ฟรี
- **Testing**: 2-3 วัน (ทดสอบ accuracy)

### 🎯 เหมาะกับ
- ✅ ต้องการเพิ่ม throughput แต่ไม่จ่ายเงิน
- ✅ มี time พัฒนา 1 สัปดาห์
- ⚠️ ยอมรับความเสี่ยง accuracy ลดลง

---

## 💾 Option 5: Caching Strategy

### 📝 ภาพรวม
Cache OCR results และ Template matching เพื่อลด API calls สำหรับ duplicate requests

### 🔧 Implementation

#### Cache Layers
1. **Image Hash Cache**: 
   - Key: `SHA256(image_data)`
   - Value: Full OCR result
   - TTL: 24 hours

2. **Template Cache**:
   - Key: `document_type:vendor_name`
   - Value: Template match result
   - TTL: 7 days

3. **Account Mapping Cache**:
   - Key: `template_id:transaction_type`
   - Value: Account entries
   - TTL: 30 days

#### Architecture
```go
// internal/cache/ocr_cache.go
type OCRCache struct {
    imageCache    map[string]CachedResult
    templateCache map[string]TemplateMatch
    mu            sync.RWMutex
}

func (c *OCRCache) GetOrProcess(imageHash string, processor func() Result) Result {
    // Check cache first
    if cached, ok := c.Get(imageHash); ok {
        return cached
    }
    
    // Process and cache
    result := processor()
    c.Set(imageHash, result)
    return result
}
```

### 📊 Cache Hit Rate (ประมาณการ)
| Scenario | Cache Hit Rate | API Calls Saved |
|----------|----------------|-----------------|
| User upload ซ้ำ | 10-20% | 10-20% |
| Same vendor/template | 30-50% | Template calls only |
| Production (1 month) | 5-15% | 5-15% overall |

### 📈 ข้อดี
- ✅ **ลด API calls** สำหรับ duplicate/similar requests
- ✅ **Response time เร็วขึ้น** - instant สำหรับ cache hit
- ✅ **ใช้ร่วมกับ option อื่นได้**
- ✅ **Implementation ไม่ยาก** - 2-3 วัน

### 📉 ข้อเสีย
- ❌ **Cache hit rate ต่ำ** - ใบเสร็จแต่ละใบไม่ซ้ำกัน
- ❌ **Memory usage** - ต้องเก็บ cache
- ❌ **Stale data risk** - ถ้า template เปลี่ยน
- ❌ **ไม่แก้ root cause** - ยังติด limit อยู่

### 💰 ต้นทุน
- **Development**: 2-3 วัน
- **Operational**: ฟรี (in-memory) หรือ ฿200-500/เดือน (Redis)
- **Benefit**: ลด API calls 5-15%

### 🎯 เหมาะกับ
- ✅ ใช้ร่วมกับ Option 1 หรือ Option 4
- ✅ มี pattern ของ duplicate requests
- ⚠️ ไม่ควรใช้ standalone

---

## 🏗️ Option 6: Horizontal Scaling + Distributed Rate Limiting

### 📝 ภาพรวม
Deploy หลาย API servers + Shared Rate Limiter (Redis) + Load Balancer

### 🔧 Architecture

```
                        Load Balancer
                             |
              +-------+-------+-------+
              |       |       |       |
           Server1  Server2  Server3  Server4
              |       |       |       |
              +-------+-------+-------+
                      |
                 Redis Cluster
              (Shared Rate Limiter)
```

### 📊 Infrastructure
- **Load Balancer**: Nginx/HAProxy
- **API Servers**: 4 instances (Docker/K8s)
- **Redis**: Cluster mode (3 nodes)
- **Monitoring**: Prometheus + Grafana

### 📈 ข้อดี
- ✅ **True scalability** - scale ได้ตามต้องการ
- ✅ **High availability** - server ตายไม่กระทบ
- ✅ **Fair rate limiting** - shared across servers
- ✅ **Production ready**

### 📉 ข้อเสีย
- ❌ **Architecture ซับซ้อน**
- ❌ **Infrastructure cost** - VM/K8s + Redis
- ❌ **Maintenance overhead** - monitoring, deployment
- ❌ **Development time** - 2-3 สัปดาห์

### 💰 ต้นทุน
- **Development**: 2-3 สัปดาห์
- **Infrastructure**: ฿2,000-5,000/เดือน
- **Maintenance**: ต้องมี DevOps engineer

### 🎯 เหมาะกับ
- ✅ Enterprise scale (1000+ requests/hour)
- ✅ มี DevOps team
- ✅ มี budget ≥฿5,000/เดือน

---

## ✅ สถานะการแก้ไข (Updated: 15 ธันวาคม 2025)

### 🎉 Implemented Solutions

**1. Sequential Processing** ✅
- เปลี่ยนจาก 3 workers → **1 worker** ([handlers.go:499](../internal/api/handlers.go#L499))
- หลีกเลี่ยง burst traffic ที่ทำให้เกิด 429 errors
- รองรับ 5 requests/minute (ตาม Gemini Free Tier limit)

**2. Rate Limiter Optimization** ✅
- เปลี่ยนจาก 15 tokens, 4s → **12 tokens, 5s** ([rate_limiter.go:79](../internal/ratelimit/rate_limiter.go#L79))
- 20% safety margin สำหรับ network latency
- Token bucket algorithm ทำงานได้ดี

**3. Smart Retry Logic** ✅
- Exponential backoff พร้อม **30-90 second delay** ([gemini_retry.go:219](../internal/ai/gemini_retry.go#L219))
- Auto-retry สำหรับ 429, 500, timeout errors
- Maximum 3 attempts

**4. Phase-Level Rate Limiting** ✅
- Pure OCR - มี rate limiting ([gemini_retry.go:175](../internal/ai/gemini_retry.go#L175))
- Template Matching - มี rate limiting ([gemini_retry.go:175](../internal/ai/gemini_retry.go#L175))
- Accounting Analysis - มี rate limiting ([gemini.go:861](../internal/ai/gemini.go#L861))

**5. Testing Results** ✅
- ทดสอบ 8 รอบ (5 รอบก่อนแก้ + 3 รอบหลังแก้)
- **0 HTTP 429 errors** (100% success rate)
- ระยะเวลาประมวลผล: 15-16 วินาที (สม่ำเสมอ)

---

## 🎯 แนวทางแนะนำตามสถานการณ์ (Archive)

### 🚀 Short-term (Demo ใน 1-2 วัน) - ✅ **ทำแล้ว**
**ก่อนแก้**: Token bucket 6 tokens (สูงสุด 2 concurrent requests)
**หลังแก้**: Token bucket **12 tokens, 5s refill** (สูงสุด 5 requests/minute)

**ทำแล้ว**:
1. ✅ Sequential processing (1 worker)
2. ✅ Rate limiter optimization (12 tokens, 5s)
3. ✅ Smart retry (30-90s delay)
4. ✅ Phase-level rate limiting

---

### 📅 Medium-term (1-2 สัปดาห์)
**เลือก 1 ใน 3**:

#### Option A: Budget มี (≥฿1,000/เดือน)
→ **Upgrade to Gemini Flash** (60 RPM)
- Development: 1 ชั่วโมง
- ทดสอบได้ทันที
- รองรับ 20 concurrent users

#### Option B: Budget ไม่มี + มีเวลา
→ **Queue System** (Option 1) + **Caching** (Option 5)
- Development: 5-7 วัน
- User ต้องรอ 10-30 วินาที
- รองรับ unlimited users (FIFO)

#### Option C: ต้องการ throughput สูง + ไม่จ่ายเงิน
→ **Optimize API Calls** (Option 4) + **Caching** (Option 5)
- Development: 5-7 วัน
- เพิ่ม throughput 50%
- รองรับ 7-8 concurrent users

---

### 🏢 Long-term (1-2 เดือน)
**Enterprise Solution**:

→ **Paid Tier** + **Queue System** + **Caching** + **Monitoring**
- Best of all worlds
- Scalable to 100+ concurrent users
- ต้นทุน: ฿3,000-5,000/เดือน

---

## 📋 Decision Matrix

| Criteria | Queue | Paid Tier | Multi-Keys | Optimize | Caching | Scaling |
|----------|-------|-----------|------------|----------|---------|---------|
| **Development Time** | 5 วัน | 1 ชม | 3 วัน | 5 วัน | 3 วัน | 3 สัปดาห์ |
| **Cost/month** | ฿0 | ฿500-1,500 | ฿0 | ฿0 | ฿0-500 | ฿3,000+ |
| **User Wait Time** | 10-60s | 0s | 0s | 0s | 0s | 0s |
| **Max Concurrent** | ∞ | 20 | 15-50 | 7-8 | +15% | 100+ |
| **Implementation Risk** | Low | None | High | Medium | Low | High |
| **Maintenance** | Low | None | High | Low | Medium | High |
| **Scalability** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐ | ⭐ | ⭐⭐⭐⭐⭐ |

---

## 🎬 สำหรับ Demo (แนะนำ)

### ✅ สิ่งที่มีอยู่แล้ว (ใช้ได้เลย)
1. ✅ Token bucket rate limiter (6 tokens)
2. ✅ Auto retry on 429 error (10-60 second wait)
3. ✅ Error messages ให้ user เห็น

### 📝 ปรับปรุงเพิ่มเติม (Optional, 1-2 ชั่วโมง)

#### 1. Better Error Message
```go
// ปรับ error message ให้ user friendly
{
    "error": "ระบบกำลังประมวลผลคำขอจำนวนมาก",
    "message": "กรุณารอ 30 วินาที แล้วลองอีกครั้ง",
    "retry_after": 30,
    "queue_position": null
}
```

#### 2. Loading State ที่ Frontend
```javascript
// แสดงสถานะที่ชัดเจน
if (response.error === 'rate_limit') {
    showMessage('กำลังรอคิว... กรุณารอสักครู่');
    setTimeout(() => retryRequest(), 30000);
}
```

#### 3. Pre-demo Testing
- ทดสอบ 3-5 requests พร้อมกัน → ต้องรอหน่อย แต่ทำงานได้
- ชี้แจง stakeholder: "รองรับได้ 5 users พร้อมกัน, ถ้าเกินต้องรอ"

---

## 📞 ติดต่อขอคำปรึกษาเพิ่มเติม

**ถ้าต้องการ implement option ใดๆ**:
- Option 1-2: พร้อม implement ทันที
- Option 3: ไม่แนะนำ (violate ToS)
- Option 4-6: ต้อง discuss requirements เพิ่มเติม

**Questions to consider**:
1. มี budget สำหรับ API costs หรือไม่? (฿500-1,500/เดือน)
2. User base คาดว่าจะเป็นกี่คน? (peak concurrent users)
3. User ยอมรอได้นานแค่ไหน? (10s, 30s, 1min)
4. Service นี้เก็บเงิน user หรือ free?
5. Timeline การพัฒนา? (1 สัปดาห์, 1 เดือน, 3 เดือน)

---

**หมายเหตุ**: เอกสารนี้อ้างอิงจาก Gemini API limits ณ วันที่ 13 ธันวาคม 2025 - อาจมีการเปลี่ยนแปลงในอนาคต
