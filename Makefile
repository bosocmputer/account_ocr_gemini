.PHONY: help run build test clean install dev

# Default target
help:
	@echo "📋 Available commands:"
	@echo "  make run       - Run the application"
	@echo "  make build     - Build the application"
	@echo "  make test      - Run tests"
	@echo "  make clean     - Clean build artifacts and uploads"
	@echo "  make install   - Install dependencies"
	@echo "  make dev       - Run in development mode with auto-reload"
	@echo "  make docker    - Build Docker image"

# Run the application
run:
	@echo "🚀 Starting Go-Receipt-Parser..."
	@go run .

# Build the application
build:
	@echo "🔨 Building application..."
	@go build -o bin/go-receipt-parser .
	@echo "✅ Build complete: bin/go-receipt-parser"

# Run tests
test:
	@echo "🧪 Running tests..."
	@go test -v ./...

# Clean build artifacts
clean:
	@echo "🧹 Cleaning..."
	@rm -rf bin/
	@rm -rf uploads/*
	@echo "✅ Clean complete"

# Install dependencies
install:
	@echo "📦 Installing dependencies..."
	@go mod download
	@go mod tidy
	@echo "✅ Dependencies installed"

# Development mode (requires air for hot reload)
dev:
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "❌ 'air' not found. Install it with: go install github.com/air-verse/air@latest"; \
		echo "   Then add it to PATH: export PATH=\$$PATH:\$$(go env GOPATH)/bin"; \
	fi

# Build Docker image
docker:
	@echo "🐳 Building Docker image..."
	@docker build -t go-receipt-parser:latest .
	@echo "✅ Docker image built: go-receipt-parser:latest"

# Format code
fmt:
	@echo "🎨 Formatting code..."
	@go fmt ./...
	@echo "✅ Code formatted"

# Run linter
lint:
	@echo "🔍 Running linter..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "❌ 'golangci-lint' not found. Install it from: https://golangci-lint.run/usage/install/"; \
	fi

# Update dependencies
update:
	@echo "⬆️  Updating dependencies..."
	@go get -u ./...
	@go mod tidy
	@echo "✅ Dependencies updated"
