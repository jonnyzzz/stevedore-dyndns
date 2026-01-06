#!/bin/sh
set -e

echo "=== Stevedore DynDNS Starting ==="
echo "Domain: ${DOMAIN:-not set}"
echo "Fritzbox Host: ${FRITZBOX_HOST:-192.168.178.1}"
echo "IP Check Interval: ${IP_CHECK_INTERVAL:-5m}"
echo "Log Level: ${LOG_LEVEL:-info}"

# Ensure data directories exist
mkdir -p /data/caddy /config/caddy /var/log/dyndns
touch /var/log/dyndns/caddy-access.log

# Stream Caddy access logs to stdout for docker logs visibility
tail -n0 -F /var/log/dyndns/caddy-access.log &
TAIL_PID=$!

# Set Caddy environment
export XDG_DATA_HOME=/data
export XDG_CONFIG_HOME=/config

# Check for required environment variables
if [ -z "$CLOUDFLARE_API_TOKEN" ]; then
    echo "ERROR: CLOUDFLARE_API_TOKEN is required"
    exit 1
fi

if [ -z "$CLOUDFLARE_ZONE_ID" ]; then
    echo "ERROR: CLOUDFLARE_ZONE_ID is required"
    exit 1
fi

if [ -z "$DOMAIN" ]; then
    echo "ERROR: DOMAIN is required"
    exit 1
fi

if [ -z "$ACME_EMAIL" ]; then
    echo "ERROR: ACME_EMAIL is required"
    exit 1
fi

# Create default mappings file if it doesn't exist
if [ ! -f /data/mappings.yaml ]; then
    echo "Creating default mappings.yaml..."
    cat > /data/mappings.yaml << 'EOF'
# Stevedore DynDNS - Service Mappings
# Edit this file to add subdomain -> service mappings
#
# Examples:
#
# mappings:
#   # Route to a specific host:port
#   - subdomain: app
#     target: "192.168.1.100:8080"
#
#   # Route to Docker Compose service
#   - subdomain: web
#     compose_project: stevedore-myapp
#     compose_service: frontend
#     port: 3000
#
#   # Route to Docker container by name
#   - subdomain: api
#     container: my-api-container
#     port: 8000
#
#   # Route with WebSocket support (for streaming)
#   - subdomain: stream
#     target: "media-server:8096"
#     options:
#       websocket: true
#       buffer_requests: false

mappings: []
EOF
fi

# Function to handle shutdown
shutdown() {
    echo "Shutting down..."
    kill -TERM "$TAIL_PID" 2>/dev/null || true
    kill -TERM "$CADDY_PID" 2>/dev/null || true
    kill -TERM "$DYNDNS_PID" 2>/dev/null || true
    wait
    echo "Goodbye!"
    exit 0
}

trap shutdown SIGTERM SIGINT

# Start the DynDNS service (manages IP detection and DNS updates)
echo "Starting DynDNS service..."
/usr/bin/dyndns &
DYNDNS_PID=$!

# Wait for Caddyfile to be generated
echo "Waiting for Caddyfile generation..."
for i in $(seq 1 30); do
    if [ -f /etc/caddy/Caddyfile ]; then
        echo "Caddyfile ready after ${i}s"
        break
    fi
    sleep 1
done

if [ ! -f /etc/caddy/Caddyfile ]; then
    echo "ERROR: Caddyfile not generated after 30s"
    exit 1
fi

# Start Caddy
echo "Starting Caddy reverse proxy..."
/usr/bin/caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
CADDY_PID=$!

echo "=== All services started ==="
echo "DynDNS PID: $DYNDNS_PID"
echo "Caddy PID: $CADDY_PID"

# Wait for either process to exit
wait -n $DYNDNS_PID $CADDY_PID

# If one exits, shut down the other
echo "A process exited, shutting down..."
shutdown
