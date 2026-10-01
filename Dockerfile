# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install ca-certificates for HTTPS calls to payment gateways
RUN apk add --no-cache ca-certificates

# Copy binary from builder
COPY --from=builder /app/server /app/server

# Copy migrations (for running goose in init containers)
COPY --from=builder /app/migrations /app/migrations

# Don't run as root
RUN adduser -D -u 1000 appuser
USER appuser

EXPOSE 8081 50051

CMD ["/app/server"]
