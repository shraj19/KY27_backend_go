# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./cmd

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# ca-certificates: verify HTTPS to Cashfree API
# tzdata: proper timezone handling for payment timestamps
RUN apk add --no-cache ca-certificates tzdata

# Copy binary from builder
COPY --from=builder /app/server /app/server

# Copy migrations (for running goose in init containers)
COPY --from=builder /app/migrations /app/migrations

# Don't run as root
RUN adduser -D -u 1000 appuser
USER appuser

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8081/health || exit 1

EXPOSE 8081 50051

CMD ["/app/server"]
