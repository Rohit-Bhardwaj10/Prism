# ── Build stage ───────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download dependencies first (cached layer)
COPY go.mod go.sum* ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /prism ./cmd/server

# ── Runtime stage ─────────────────────────────────────────────
FROM alpine:3.20

# ca-certificates needed for HTTPS calls to Ollama / backend
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary
COPY --from=builder /prism .

# Copy config files (policies + synonyms)
COPY configs/ ./configs/

# Copy migrations (run by db.Migrate on startup)
COPY migrations/ ./migrations/

# Expose API and metrics ports
EXPOSE 8080 9090

ENTRYPOINT ["/app/prism"]
