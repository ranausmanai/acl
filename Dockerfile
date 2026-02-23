# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Download dependencies first (layer-cached separately from source)
COPY go.mod go.sum ./
RUN go mod download

# Build the binary (modernc.org/sqlite is pure Go — no CGO needed)
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/acl ./cmd/acl

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.21

LABEL org.opencontainers.image.source=https://github.com/ranausmanai/acl

# python3 is required only when code.run is used (ACL_CODE_RUN_ENABLED=1).
# Remove it if your ACL scripts don't use the code.run tool.
RUN apk add --no-cache python3 ca-certificates tzdata

COPY --from=builder /bin/acl /usr/local/bin/acl

# ACL files are mounted at runtime (see docker-compose.yml).
# Receipts, memory DB, and vector DB land in /data (mount a volume here).
WORKDIR /workspace

# Expose the serve port
EXPOSE 8080

ENTRYPOINT ["acl"]
CMD ["--help"]
