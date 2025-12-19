# 🚀 Docker Deployment Guide

## Quick Start

### 1. Setup Environment Variables

```bash
# Copy .env.example to .env
cp .env.example .env

# Edit .env and fill in your credentials
nano .env
```

**Required Configuration:**
- `GEMINI_API_KEY` - Your Gemini API key (REQUIRED)
- `MONGO_URI` - MongoDB connection string
- `MONGO_DB_NAME` - Database name

### 2. Build and Run with Docker Compose

```bash
# Build and start
docker-compose up -d

# View logs
docker-compose logs -f

# Stop
docker-compose down
```

### 3. Test the API

```bash
# Health check
curl http://localhost:8080/health

# Analyze receipt (supports both PDF and Image)
curl -X POST http://localhost:8080/api/v1/analyze-receipt \
  -H "Content-Type: application/json" \
  -d '{
    "shopid": "SHOP001",
    "imagereferences": [{
      "documentimageguid": "test-guid",
      "imageuri": "https://your-blob-storage.com/receipt.jpg"
    }]
  }'

# Analyze PDF receipt
curl -X POST http://localhost:8080/api/v1/analyze-receipt \
  -H "Content-Type: application/json" \
  -d '{
    "shopid": "SHOP001",
    "imagereferences": [{
      "documentimageguid": "test-pdf-guid",
      "imageuri": "https://your-blob-storage.com/receipt.pdf"
    }]
  }'
```

---

## Configuration Reference

All configuration is managed through `.env` file:

| Variable | Default | Description |
|----------|---------|-------------|
| `GEMINI_API_KEY` | *required* | Gemini AI API key |
| **Phase-Specific Models** | | |
| `OCR_MODEL_NAME` | `gemini-2.5-flash-lite` | OCR model (Phase 1) |
| `TEMPLATE_MODEL_NAME` | `gemini-2.5-flash-lite` | Template matching model (Phase 2) |
| `ACCOUNTING_MODEL_NAME` | `gemini-2.5-flash` | Accounting analysis model (Phase 3) |
| `MODEL_NAME` | `gemini-2.5-flash-lite` | (Deprecated) Fallback model |
| **Pricing** | | |
| `OCR_INPUT_PRICE_PER_MILLION` | `0.10` | OCR input price (USD/1M tokens) |
| `OCR_OUTPUT_PRICE_PER_MILLION` | `0.40` | OCR output price (USD/1M tokens) |
| `TEMPLATE_INPUT_PRICE_PER_MILLION` | `0.10` | Template input price (USD/1M tokens) |
| `TEMPLATE_OUTPUT_PRICE_PER_MILLION` | `0.40` | Template output price (USD/1M tokens) |
| `ACCOUNTING_INPUT_PRICE_PER_MILLION` | `0.30` | Accounting input price (USD/1M tokens) |
| `ACCOUNTING_OUTPUT_PRICE_PER_MILLION` | `2.50` | Accounting output price (USD/1M tokens) |
| `USD_TO_THB` | `36.0` | USD to THB exchange rate |
| **Server** | | |
| `PORT` | `8080` | Server port |
| `GIN_MODE` | `release` | Gin mode (debug/release) |
| `MONGO_URI` | - | MongoDB connection URI |
| `MONGO_DB_NAME` | `smldevdb` | Database name |
| `ALLOWED_ORIGINS` | `*` | CORS allowed origins |
| `UPLOAD_DIR` | `uploads` | Upload directory |
| `ENABLE_IMAGE_PREPROCESSING` | `true` | Enable image preprocessing |
| `MAX_IMAGE_DIMENSION` | `2000` | Max image dimension (px) |

---

## Environment-Specific Configuration

### Development
```env
GIN_MODE=debug
ALLOWED_ORIGINS=*
PORT=8080
```

### Production
```env
GIN_MODE=release
ALLOWED_ORIGINS=https://yourdomain.com
PORT=8080
```

### Testing
```env
GIN_MODE=debug
ALLOWED_ORIGINS=*
PORT=8081
```

---

## Docker Commands

### Build from scratch
```bash
docker-compose build --no-cache
```

### View logs
```bash
# All logs
docker-compose logs

# Follow logs
docker-compose logs -f

# Specific service logs
docker-compose logs app
```

### Restart service
```bash
docker-compose restart
```

### Stop and remove
```bash
docker-compose down -v
```

---

## Troubleshooting

### Issue: Cannot connect to MongoDB
**Solution:** Check `MONGO_URI` in `.env` file. Ensure password is URL-encoded.

```env
# Correct (URL-encoded) - Use %40 for @ symbol
MONGO_URI=mongodb://username:encoded%40password@host:27017/?authSource=admin

# Wrong (not encoded)
MONGO_URI=mongodb://username:password@with@symbol@host:27017/?authSource=admin
```

### Issue: Port already in use
**Solution:** Change `PORT` in `.env` file or stop conflicting service.

```bash
# Check what's using port 8080
lsof -i :8080

# Kill process
kill -9 <PID>
```

### Issue: Gemini API errors
**Solution:** Verify `GEMINI_API_KEY` is correct and has proper permissions.

---

## Security Best Practices

1. **Never commit `.env` file** - Already in `.gitignore`
2. **Use strong passwords** - Change default credentials
3. **Restrict CORS** - Set `ALLOWED_ORIGINS` to specific domain in production
4. **Use secrets management** - For production, use Docker secrets or external vault
5. **Keep API keys secure** - Rotate keys regularly

---

## Production Deployment

### Using Docker Secrets (Recommended)

```yaml
# docker-compose.prod.yml
version: '3.8'

services:
  app:
    build: .
    secrets:
      - gemini_api_key
      - mongo_uri
    environment:
      - GEMINI_API_KEY_FILE=/run/secrets/gemini_api_key
      - MONGO_URI_FILE=/run/secrets/mongo_uri

secrets:
  gemini_api_key:
    external: true
  mongo_uri:
    external: true
```

### Using External Configuration

```bash
# Load from external config
docker run -d \
  --name receipt-parser \
  --env-file /secure/path/.env \
  -p 8080:8080 \
  receipt-parser:latest
```

---

## Monitoring

### Health Check
```bash
curl http://localhost:8080/health
```

### Container Stats
```bash
docker stats go-receipt-parser
```

### Resource Usage
```bash
docker-compose ps
docker-compose top
```

---

## Backup & Recovery

### Backup .env file
```bash
# Backup (encrypted)
tar czf config-backup.tar.gz .env
openssl enc -aes-256-cbc -salt -in config-backup.tar.gz -out config-backup.tar.gz.enc
rm config-backup.tar.gz
```

### Restore .env file
```bash
# Decrypt and restore
openssl enc -d -aes-256-cbc -in config-backup.tar.gz.enc -out config-backup.tar.gz
tar xzf config-backup.tar.gz
```
