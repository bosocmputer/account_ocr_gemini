#!/bin/bash
# Test script for PDF support in bill_scan_project

echo "========================================="
echo "PDF Support Test Script"
echo "========================================="
echo ""

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Check if server is running
echo "📡 Checking if server is running on localhost:8080..."
if ! curl -s http://localhost:8080/health > /dev/null 2>&1; then
    echo -e "${RED}❌ Server is not running!${NC}"
    echo "Please start the server first:"
    echo "  go run cmd/api/main.go"
    exit 1
fi
echo -e "${GREEN}✅ Server is running${NC}"
echo ""

# Test 1: Test with PDF URL
echo "========================================="
echo "Test 1: Testing with PDF URL"
echo "========================================="
echo ""
echo "🧪 Sending request with PDF URL..."
echo "URL: https://dedeposblosstorage.blob.core.windows.net/dedeposdevcontainer/2V5zu2gmRgd7sgWj3g6gu7mxYk0/36xoVoP2BMAiIkYNbCqzLWf3evP.pdf"
echo ""

curl -X POST http://localhost:8080/api/v1/analyze-receipt \
  -H "Content-Type: application/json" \
  -d '{
    "shopid": "2V5zu2gmRgd7sgWj3g6gu7mxYk0",
    "imagereferences": [
        {
            "documentimageguid": "36xoW2CODcX4lwItEfZFTsIcDdp",
            "imageuri": "https://dedeposblosstorage.blob.core.windows.net/dedeposdevcontainer/2V5zu2gmRgd7sgWj3g6gu7mxYk0/36xoVoP2BMAiIkYNbCqzLWf3evP.pdf"
        }
    ]
}' | jq '.' > /tmp/pdf_test_result.json

echo ""
echo "========================================="
echo "Test Results"
echo "========================================="

if [ -f /tmp/pdf_test_result.json ]; then
    STATUS=$(cat /tmp/pdf_test_result.json | jq -r '.status // "error"')

    if [ "$STATUS" == "success" ]; then
        echo -e "${GREEN}✅ Test PASSED - PDF was processed successfully!${NC}"
        echo ""
        echo "📊 Summary:"
        cat /tmp/pdf_test_result.json | jq '{
            status: .status,
            vendor: .receipt.vendor_name,
            total: .receipt.total,
            date: .receipt.date,
            confidence: .validation.confidence.level,
            request_id: .metadata.request_id,
            processing_time: .metadata.duration_sec,
            tokens_used: .metadata.token_usage.total_tokens
        }'
        echo ""
        echo "📄 Full response saved to: /tmp/pdf_test_result.json"
    else
        echo -e "${RED}❌ Test FAILED${NC}"
        echo ""
        echo "Error details:"
        cat /tmp/pdf_test_result.json | jq '.'
    fi
else
    echo -e "${RED}❌ No response received${NC}"
fi

echo ""
echo "========================================="
echo "Test Complete"
echo "========================================="
