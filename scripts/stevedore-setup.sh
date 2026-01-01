#!/bin/bash
# Stevedore DynDNS - Setup Script
#
# This script helps set up stevedore-dyndns as a Stevedore deployment.
# Run this on the host machine where Stevedore is installed.
#
# Usage:
#   ./scripts/stevedore-setup.sh
#
# Environment variables (optional):
#   DEPLOYMENT_NAME - Name for the deployment (default: dyndns)
#   GIT_URL - Git repository URL (default: git@github.com:jonnyzzz/stevedore-dyndns.git)
#   GIT_BRANCH - Git branch (default: main)

set -e

# Configuration
DEPLOYMENT_NAME="${DEPLOYMENT_NAME:-dyndns}"
GIT_URL="${GIT_URL:-git@github.com:jonnyzzz/stevedore-dyndns.git}"
GIT_BRANCH="${GIT_BRANCH:-main}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if stevedore is installed
check_stevedore() {
    log_info "Checking Stevedore installation..."
    if ! command -v stevedore &> /dev/null; then
        log_error "Stevedore is not installed or not in PATH"
        echo "Install Stevedore first: https://github.com/jonnyzzz/stevedore"
        exit 1
    fi

    # Check if daemon is running
    if ! stevedore healthz &> /dev/null; then
        log_error "Stevedore daemon is not running"
        echo "Start it with: systemctl start stevedore"
        exit 1
    fi

    log_success "Stevedore is installed and running"
}

# Add repository
add_repository() {
    log_info "Adding repository: $DEPLOYMENT_NAME"

    # Check if deployment already exists
    if stevedore status "$DEPLOYMENT_NAME" &> /dev/null; then
        log_warn "Deployment '$DEPLOYMENT_NAME' already exists"
        read -p "Do you want to continue and update it? [y/N] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 0
        fi
    else
        # Add new repository
        stevedore repo add "$DEPLOYMENT_NAME" "$GIT_URL" --branch "$GIT_BRANCH"
        log_success "Repository added"

        # Show deploy key
        echo
        log_info "Add this deploy key to your GitHub repository:"
        echo "Settings -> Deploy keys -> Add deploy key"
        echo
        stevedore repo key "$DEPLOYMENT_NAME"
        echo
        read -p "Press Enter after adding the deploy key to GitHub..."
    fi
}

# Configure parameters
configure_parameters() {
    log_info "Configuring deployment parameters..."

    echo
    echo "You need to provide the following configuration:"
    echo

    # Cloudflare API Token
    if [ -z "$CLOUDFLARE_API_TOKEN" ]; then
        echo "CLOUDFLARE_API_TOKEN:"
        echo "  Create at: https://dash.cloudflare.com/profile/api-tokens"
        echo "  Required permissions: Zone:Zone:Read, Zone:DNS:Edit"
        read -sp "Enter Cloudflare API Token: " CLOUDFLARE_API_TOKEN
        echo
    fi
    stevedore param set "$DEPLOYMENT_NAME" CLOUDFLARE_API_TOKEN "$CLOUDFLARE_API_TOKEN"
    log_success "Set CLOUDFLARE_API_TOKEN"

    # Cloudflare Zone ID
    if [ -z "$CLOUDFLARE_ZONE_ID" ]; then
        echo
        echo "CLOUDFLARE_ZONE_ID:"
        echo "  Found in Cloudflare dashboard -> Your domain -> Overview (right sidebar)"
        read -p "Enter Cloudflare Zone ID: " CLOUDFLARE_ZONE_ID
    fi
    stevedore param set "$DEPLOYMENT_NAME" CLOUDFLARE_ZONE_ID "$CLOUDFLARE_ZONE_ID"
    log_success "Set CLOUDFLARE_ZONE_ID"

    # Domain
    if [ -z "$DOMAIN" ]; then
        echo
        echo "DOMAIN:"
        echo "  Your domain name (e.g., example.com)"
        read -p "Enter Domain: " DOMAIN
    fi
    stevedore param set "$DEPLOYMENT_NAME" DOMAIN "$DOMAIN"
    log_success "Set DOMAIN"

    # ACME Email
    if [ -z "$ACME_EMAIL" ]; then
        echo
        echo "ACME_EMAIL:"
        echo "  Email for Let's Encrypt certificate notifications"
        read -p "Enter ACME Email: " ACME_EMAIL
    fi
    stevedore param set "$DEPLOYMENT_NAME" ACME_EMAIL "$ACME_EMAIL"
    log_success "Set ACME_EMAIL"

    # Optional: Fritzbox host
    echo
    read -p "Configure Fritzbox? (for IP auto-detection) [y/N] " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        read -p "Enter Fritzbox IP [192.168.178.1]: " FRITZBOX_HOST
        FRITZBOX_HOST="${FRITZBOX_HOST:-192.168.178.1}"
        stevedore param set "$DEPLOYMENT_NAME" FRITZBOX_HOST "$FRITZBOX_HOST"
        log_success "Set FRITZBOX_HOST"
    fi

    echo
    log_success "All required parameters configured!"
}

# Sync and deploy
deploy() {
    log_info "Syncing repository..."
    stevedore deploy sync "$DEPLOYMENT_NAME"
    log_success "Repository synced"

    log_info "Deploying service..."
    stevedore deploy up "$DEPLOYMENT_NAME"
    log_success "Service deployed"

    # Wait for health check
    log_info "Waiting for service to become healthy..."
    for i in {1..30}; do
        if stevedore status "$DEPLOYMENT_NAME" 2>/dev/null | grep -q "healthy"; then
            log_success "Service is healthy!"
            break
        fi
        sleep 2
        echo -n "."
    done
    echo
}

# Create initial mappings file
create_mappings() {
    local STEVEDORE_ROOT="${STEVEDORE_ROOT:-/opt/stevedore}"
    local MAPPINGS_FILE="$STEVEDORE_ROOT/deployments/$DEPLOYMENT_NAME/data/mappings.yaml"
    local SHARED_MAPPINGS="$STEVEDORE_ROOT/shared/dyndns-mappings.yaml"

    log_info "Creating mappings configuration..."

    # Create shared mappings symlink
    if [ ! -f "$SHARED_MAPPINGS" ]; then
        cat > "$SHARED_MAPPINGS" << 'EOF'
# Stevedore DynDNS - Service Mappings
#
# This file maps subdomains to backend services.
# Other Stevedore deployments can add their entries here.
#
# Format:
#   mappings:
#     - subdomain: myapp          # -> myapp.yourdomain.com
#       target: "localhost:3000"  # Backend service (host:port)
#       options:
#         websocket: true         # Enable WebSocket support
#         health_path: /health    # Health check endpoint
#
# For services running in other Stevedore deployments:
#   - Use localhost:<port> since we're on host network
#   - Or use container name if on same Docker network

mappings: []
EOF
        log_success "Created shared mappings file: $SHARED_MAPPINGS"
    fi

    # Link to deployment data directory
    mkdir -p "$(dirname "$MAPPINGS_FILE")"
    if [ ! -L "$MAPPINGS_FILE" ]; then
        ln -sf "$SHARED_MAPPINGS" "$MAPPINGS_FILE"
        log_success "Linked mappings to shared location"
    fi

    echo
    log_info "Mappings file location: $SHARED_MAPPINGS"
    echo "Other deployments can add their services to this file."
}

# Show status
show_status() {
    echo
    echo "=========================================="
    echo "  Stevedore DynDNS Setup Complete!"
    echo "=========================================="
    echo
    stevedore status "$DEPLOYMENT_NAME"
    echo
    echo "Next steps:"
    echo "  1. Edit mappings: /opt/stevedore/shared/dyndns-mappings.yaml"
    echo "  2. Add entries for your services"
    echo "  3. The service will auto-reload when mappings change"
    echo
    echo "Example mapping for another Stevedore service:"
    echo '  - subdomain: myapp'
    echo '    target: "localhost:3000"'
    echo
    echo "View logs:"
    echo "  docker logs stevedore-$DEPLOYMENT_NAME-dyndns-1"
    echo
}

# Main
main() {
    echo "=========================================="
    echo "  Stevedore DynDNS Setup"
    echo "=========================================="
    echo

    check_stevedore
    add_repository
    configure_parameters
    deploy
    create_mappings
    show_status
}

main "$@"
