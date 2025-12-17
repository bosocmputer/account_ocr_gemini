# Build stage
FROM golang:1.22-alpine3.19 AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/
COPY configs/ configs/

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd/api

# Runtime stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Create app directory
WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/main .

# Create uploads directory
RUN mkdir -p uploads

# Expose port
EXPOSE 8080

# Run the application
CMD ["./main"]
