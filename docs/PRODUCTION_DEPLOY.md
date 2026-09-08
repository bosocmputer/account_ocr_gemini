# 🖥️ Production/Dev Deploy Runbook

คู่มือ deploy จริงของ service นี้ (repo name บน GitHub: `account_ocr_gemini`) — ต่างจาก
[`DOCKER_DEPLOY.md`](DOCKER_DEPLOY.md) ที่เป็นคู่มือทั่วไปสำหรับรัน Docker Compose บนเครื่องใดก็ได้
ไฟล์นี้บันทึกตำแหน่งและขั้นตอนที่ใช้จริงบน server ของทีม

อัปเดตล่าสุด: 2026-09-08 (verified ตรงกับ server จริง)

---

## 1. ภาพรวม service ที่รันอยู่

| | Dev | Prod |
|---|---|---|
| Container name | `billscan-api` | `billscan-api-prod` |
| Host port | `8280` | `8281` |
| Compose file | `/datasml/billscan/docker-compose.yml` | `/datasml/billscan-prod/docker-compose.yml` |
| MongoDB | `smldevdb` (dev DB) | production DB (connection ผ่าน `mongodb-ca.crt`) |
| Public URL | `https://ubtsmldev.dedecafe.com/billscan/*` | `https://ubtsmldev.dedecafe.com/billscan-prod/*` |
| Image | `ghcr.io/bosocmputer/account_ocr_gemini:latest` (ตัวเดียวกันทั้งคู่) | เดียวกัน |

ทั้งสอง container รัน image เดียวกันจาก GHCR แต่คนละ `.env`/`docker-compose.yml`/database — **ไม่มีความต่างที่ระดับโค้ด** ต่างกันแค่ config

---

## 2. ตำแหน่งจริงบน server

**Host**: `192.168.2.203` (LAN, hostname `smldev-System-Product-Name`)

```bash
ssh smldev@192.168.2.203
```
- User: `smldev`
- Auth: password (ถามผู้ดูแลระบบสำหรับรหัสปัจจุบัน — ไม่บันทึกไว้ในเอกสารนี้)
- Port: 22 (มาตรฐาน)
- Docker: v29.6.0 / Docker Compose v5.1.4 (ตรวจสอบด้วย `docker --version && docker compose version`)

### โครงสร้างไดเรกทอรีบน server

```
/datasml/billscan/               ← dev container
├── .env                         ← ค่าจริง ไม่ commit ขึ้น git (chmod 600)
└── docker-compose.yml

/datasml/billscan-prod/          ← prod container
├── .env                         ← ค่าจริง ไม่ commit ขึ้น git (chmod 600)
├── docker-compose.yml
└── mongodb-ca.crt               ← CA cert สำหรับต่อ MongoDB Atlas/managed cluster แบบ TLS
```

### `docker-compose.yml` จริงของแต่ละตัว (สำหรับอ้างอิง ไม่ต้องพิมพ์ซ้ำเอง)

**Dev** (`/datasml/billscan/docker-compose.yml`):
```yaml
services:
  app:
    image: ghcr.io/bosocmputer/account_ocr_gemini:latest
    container_name: billscan-api
    restart: unless-stopped
    ports:
      - "8280:8080"
    env_file:
      - .env
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

**Prod** (`/datasml/billscan-prod/docker-compose.yml`) — เหมือน dev ทุกอย่าง บวก mount CA cert:
```yaml
services:
  app:
    image: ghcr.io/bosocmputer/account_ocr_gemini:latest
    container_name: billscan-api-prod
    restart: unless-stopped
    ports:
      - "8281:8080"
    env_file:
      - .env
    volumes:
      - ./mongodb-ca.crt:/etc/billscan/mongodb-ca.crt:ro
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

### `.env` — key ที่ต้องมี (ดูค่าจริงบน server เท่านั้น อย่า commit ค่าเหล่านี้)

ทั้ง dev/prod มี key ชุดเดียวกัน (prod เพิ่ม `OCR_WORKER_COUNT`):

```
GEMINI_API_KEY=
MISTRAL_API_KEY=
MISTRAL_MODEL_NAME=
OCR_MODEL_NAME=
TEMPLATE_MODEL_NAME=
TEMPLATE_ACCOUNTING_MODEL_NAME=
ACCOUNTING_MODEL_NAME=
TEMPLATE_CONFIDENCE_THRESHOLD=
USD_TO_THB=
PORT=
GIN_MODE=
MONGO_URI=
MONGO_DB_NAME=
ALLOWED_ORIGINS=
UPLOAD_DIR=
ENABLE_IMAGE_PREPROCESSING=
MAX_IMAGE_DIMENSION=
OCR_WORKER_COUNT=          # prod เท่านั้น ตอนนี้
```

> ⚠️ `.env.example` ในโปรเจคนี้ยังไม่ได้อัปเดตให้ตรงกับ key ชุดนี้ทั้งหมด (ขาด `MISTRAL_*`, `TEMPLATE_*`,
> `ACCOUNTING_MODEL_NAME`, `TEMPLATE_CONFIDENCE_THRESHOLD`, `USD_TO_THB`) — ถ้าจะตั้ง server ใหม่
> ให้ตรวจสอบ key จริงบน `/datasml/billscan/.env` เป็นความจริงเสมอ ไม่ใช่ `.env.example`

---

## 3. Reverse proxy (Caddy)

Caddy รันบน host เดียวกัน อ่านค่าจาก `/etc/caddy/Caddyfile`, บล็อกของ domain `ubtsmldev.dedecafe.com`:

```caddyfile
handle /billscan/* {
    reverse_proxy 127.0.0.1:8280
}
handle /billscan-prod/* {
    uri replace /billscan-prod /billscan 1
    reverse_proxy 127.0.0.1:8281
}
```

จุดสำคัญ: container ทั้งสอง hardcode route ไว้ใต้ `/billscan/*` เท่านั้น (ดู `cmd/api/main.go`) — ตัว prod
เอง**ไม่รู้จัก** prefix `/billscan-prod` เลย ดังนั้น Caddy ต้อง **rewrite** prefix (`uri replace`) ก่อนส่งต่อ
ไม่ใช่แค่ strip แบบ `handle_path` — ถ้าใช้ `handle_path` เฉยๆ จะเหลือ path ที่ containerไม่รู้จักและ 404

> `/etc/caddy/Caddyfile` มีบล็อกอื่นที่ฝัง bearer-token secret ไว้ด้วย (ใกล้ๆ บล็อกนี้) — ถ้าต้องเปิดไฟล์
> เพื่อแก้ไข ให้ grep เฉพาะส่วน `ubtsmldev.dedecafe.com` แทนการเปิดทั้งไฟล์

Reload หลังแก้ Caddyfile:
```bash
sudo systemctl reload caddy
# หรือ
caddy reload --config /etc/caddy/Caddyfile
```

---

## 4. CI/CD — GitHub Actions

Workflow: `.github/workflows/docker-build.yml` (ชื่อ job: "Build and Push Docker Image")

```yaml
on:
  push:
    branches: [main, develop]
    tags: ['v*']
  pull_request:
    branches: [main, develop]
  workflow_dispatch:
```

**สำคัญ**: workflow นี้ trigger อัตโนมัติทันทีที่ `git push` ขึ้น `main` หรือ `develop` — **ไม่ต้องกด
"Run workflow" เอง** ยกเว้นต้องการ build ซ้ำโดยไม่มี commit ใหม่ (เช่น build ล้มเหลวจาก flaky step)

Tag `:latest` จะถูกสร้าง**เฉพาะเมื่อ push เข้า default branch** (`main`) เท่านั้น
(`type=raw,value=latest,enable={{is_default_branch}}` ใน `docker/metadata-action`) — push เข้า `develop`
จะได้ tag อื่น (`develop`, `develop-<sha>`) แต่ไม่อัปเดต `:latest`

Image ถูก build จาก `deployments/docker/Dockerfile` (ไม่ใช่ `Dockerfile` ที่ root) แล้ว push ไปที่
`ghcr.io/bosocmputer/account_ocr_gemini`

**ตรวจสอบสถานะ build**:
```bash
gh run list --repo bosocmputer/account_ocr_gemini --limit 5
```
ควรเห็น `completed / success` ตรงกับ commit ล่าสุดของคุณ ก่อนไปขั้นตอน deploy จริง

---

## 5. ขั้นตอน Deploy จริง (หลัง build image เสร็จ)

**⚠️ ข้อควรระวัง**: GitHub build เสร็จ ≠ deploy เสร็จ — image ใหม่จะอยู่บน GHCR เฉยๆ จนกว่าจะสั่ง
`docker compose pull` บน server ให้ดึงมารันจริง

### Deploy เฉพาะ dev
```bash
ssh smldev@192.168.2.203
cd /datasml/billscan
docker compose pull
docker compose up -d
```

### Deploy เฉพาะ prod
```bash
ssh smldev@192.168.2.203
cd /datasml/billscan-prod
docker compose pull
docker compose up -d
```

### Deploy ทั้งคู่ (คำสั่งเดียวจากเครื่อง local ผ่าน SSH)
```bash
ssh smldev@192.168.2.203 "cd /datasml/billscan && docker compose pull && docker compose up -d"
ssh smldev@192.168.2.203 "cd /datasml/billscan-prod && docker compose pull && docker compose up -d"
```

`docker compose up -d` จะ recreate container ใหม่โดยอัตโนมัติถ้า image เปลี่ยน (ไม่ต้อง `down` ก่อน)
ดาวน์ไทม์ระหว่าง recreate ปกติไม่ถึง 10 วินาที (health check `start_period: 10s`)

---

## 6. วิธี Verify ว่า Deploy สำเร็จจริง

### 6.1 เช็คสถานะ container (ต้องเป็น `healthy`)
```bash
ssh smldev@192.168.2.203 "docker ps --filter name=billscan --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}'"
```
คาดหวัง:
```
NAMES               STATUS                    IMAGE
billscan-api-prod   Up X seconds (healthy)   ghcr.io/bosocmputer/account_ocr_gemini:latest
billscan-api        Up X seconds (healthy)   ghcr.io/bosocmputer/account_ocr_gemini:latest
```
ถ้าเห็น `(unhealthy)` หรือ `(health: starting)` ค้างนาน ให้ดู log ทันที (ข้อ 6.3)

### 6.2 เช็ค route จริงผ่าน public URL
```bash
curl -s -o /dev/null -w "dev: HTTP %{http_code}\n"  -X POST "https://ubtsmldev.dedecafe.com/billscan/api/v1/import-journal/validate"
curl -s -o /dev/null -w "prod: HTTP %{http_code}\n" -X POST "https://ubtsmldev.dedecafe.com/billscan-prod/api/v1/import-journal/validate"
```
คาดหวัง **`400`** พร้อม body `{"error":"shopid is required"}` — นี่คือสัญญาณว่า route ถูก wire
ไว้จริงและ handler ทำงาน (ไม่ใช่ routing 404) ปกติ endpoint นี้ต้องการ multipart form + shopid ดังนั้น
400 คือ "ถูกต้องตามคาด" ไม่ใช่ error

ถ้าได้ `404 page not found` แปลว่า route ไม่ถูก register จริง (โค้ดคนละเวอร์ชัน หรือ path ผิด) —
ต้องเช็คโค้ด ไม่ใช่แค่ retry

### 6.3 เช็ค log การ start
```bash
ssh smldev@192.168.2.203 "docker logs billscan-api --tail 40"
ssh smldev@192.168.2.203 "docker logs billscan-api-prod --tail 40"
```
Log ที่ถูกต้องตอน start สำเร็จควรมี:
```
✓ Configuration loaded successfully
✅ Connected to MongoDB successfully!
Starting server on :8080
```
ถ้าเห็น error เชื่อมต่อ MongoDB หรือ config โหลดไม่ผ่าน ให้เช็ค `.env` ก่อน (มักเป็นปัญหา
`MONGO_URI` เข้ารหัสอักขระพิเศษไม่ครบ — ดู [`DOCKER_DEPLOY.md`](DOCKER_DEPLOY.md#troubleshooting))

---

## 7. Rollback

ถ้า deploy แล้วพัง ให้ pin กลับไป image tag ก่อนหน้าแทนการ pull `:latest`:

```bash
# หา sha ของ commit ก่อนหน้าที่รู้ว่าใช้งานได้
git log --oneline -5

# แก้ image ใน docker-compose.yml ชั่วคราวเป็น tag เจาะจง (ไม่ใช่ :latest)
# image: ghcr.io/bosocmputer/account_ocr_gemini:main-<sha สั้น 7 ตัว>

ssh smldev@192.168.2.203
cd /datasml/billscan          # หรือ billscan-prod
nano docker-compose.yml       # แก้ image tag ชั่วคราว
docker compose pull
docker compose up -d
```
เมื่อแก้โค้ดต้นเหตุเสร็จแล้ว อย่าลืมเปลี่ยน `docker-compose.yml` กลับเป็น `:latest` เหมือนเดิม
มิฉะนั้นจะไม่ได้รับ build ใหม่ในอนาคตโดยอัตโนมัติอีก

---

## 8. Checklist สรุปแบบเร็ว (คัดลอกไปใช้ได้เลย)

```bash
# 1. ตรวจว่า build สำเร็จหรือยัง
gh run list --repo bosocmputer/account_ocr_gemini --limit 3

# 2. Deploy ทั้ง dev + prod
ssh smldev@192.168.2.203 "cd /datasml/billscan && docker compose pull && docker compose up -d"
ssh smldev@192.168.2.203 "cd /datasml/billscan-prod && docker compose pull && docker compose up -d"

# 3. รอสัก 10-15 วินาที แล้ว verify
ssh smldev@192.168.2.203 "docker ps --filter name=billscan --format 'table {{.Names}}\t{{.Status}}'"
curl -s -o /dev/null -w "dev: HTTP %{http_code}\n"  -X POST "https://ubtsmldev.dedecafe.com/billscan/api/v1/import-journal/validate"
curl -s -o /dev/null -w "prod: HTTP %{http_code}\n" -X POST "https://ubtsmldev.dedecafe.com/billscan-prod/api/v1/import-journal/validate"
```
เห็น `healthy` ทั้งคู่ + `HTTP 400` ทั้งคู่ = deploy สำเร็จ
