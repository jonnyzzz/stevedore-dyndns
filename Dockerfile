# syntax=docker/dockerfile:1

# Stage 1: Build Caddy with Cloudflare DNS plugin
FROM caddy:2-builder AS caddy-builder
RUN xcaddy build \
    --with github.com/caddy-dns/cloudflare

# Stage 2: Build Go service
FROM golang:1.21-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /dyndns ./cmd/dyndns

# Stage 3: Final image
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    curl \
    tzdata \
    && rm -rf /var/cache/apk/*

# Copy binaries
COPY --from=caddy-builder /usr/bin/caddy /usr/bin/caddy
COPY --from=go-builder /dyndns /usr/bin/dyndns

# Copy configuration templates
COPY Caddyfile.template /etc/caddy/Caddyfile.template

# Create directories
RUN mkdir -p /data /config /var/log/dyndns

# Environment defaults
ENV CADDY_DATA=/data/caddy \
    CADDY_CONFIG=/config/caddy \
    DYNDNS_DATA=/data \
    DYNDNS_LOGS=/var/log/dyndns \
    LOG_LEVEL=info \
    IP_CHECK_INTERVAL=5m \
    FRITZBOX_HOST=192.168.178.1

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Expose ports
EXPOSE 80 443 8080

# Entry point
COPY scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
