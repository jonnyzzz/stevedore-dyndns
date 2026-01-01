#!/bin/bash
# Stevedore DynDNS - Service Registration Script
#
# Use this script to register a service with stevedore-dyndns.
# Run from any Stevedore deployment to add its service to the proxy.
#
# Usage:
#   ./scripts/register-service.sh <subdomain> <target> [options]
#
# Examples:
#   # Register a service running on port 3000
#   ./scripts/register-service.sh myapp localhost:3000
#
#   # Register with WebSocket support
#   ./scripts/register-service.sh chat localhost:8080 --websocket
#
#   # Register with custom health path
#   ./scripts/register-service.sh api localhost:9000 --health-path /api/health
#
#   # Unregister a service
#   ./scripts/register-service.sh myapp --remove

set -e

# Configuration
STEVEDORE_ROOT="${STEVEDORE_ROOT:-/opt/stevedore}"
MAPPINGS_FILE="$STEVEDORE_ROOT/shared/dyndns-mappings.yaml"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

usage() {
    echo "Usage: $0 <subdomain> <target> [options]"
    echo
    echo "Arguments:"
    echo "  subdomain    Subdomain name (e.g., 'myapp' for myapp.yourdomain.com)"
    echo "  target       Backend target (e.g., 'localhost:3000' or 'container:8080')"
    echo
    echo "Options:"
    echo "  --websocket          Enable WebSocket support"
    echo "  --health-path PATH   Custom health check path (default: /health)"
    echo "  --no-buffer          Disable request buffering (for streaming)"
    echo "  --remove             Remove the subdomain entry"
    echo "  --list               List all registered services"
    echo "  -h, --help           Show this help message"
    echo
    echo "Examples:"
    echo "  $0 myapp localhost:3000"
    echo "  $0 api localhost:8080 --websocket --health-path /healthz"
    echo "  $0 myapp --remove"
}

# Check dependencies
check_dependencies() {
    if ! command -v yq &> /dev/null; then
        echo -e "${YELLOW}[WARN]${NC} yq not found, using basic YAML manipulation"
        USE_YQ=false
    else
        USE_YQ=true
    fi
}

# List services
list_services() {
    if [ ! -f "$MAPPINGS_FILE" ]; then
        echo "No mappings file found at $MAPPINGS_FILE"
        exit 0
    fi

    echo "Registered services:"
    echo "===================="

    if $USE_YQ; then
        yq eval '.mappings[] | .subdomain + " -> " + .target' "$MAPPINGS_FILE" 2>/dev/null || echo "No services registered"
    else
        grep -E "^\s+subdomain:|^\s+target:" "$MAPPINGS_FILE" | paste - - | sed 's/subdomain://g; s/target://g; s/^\s*//g'
    fi
}

# Add service
add_service() {
    local subdomain="$1"
    local target="$2"
    local websocket="$3"
    local health_path="$4"
    local no_buffer="$5"

    # Create mappings file if it doesn't exist
    if [ ! -f "$MAPPINGS_FILE" ]; then
        mkdir -p "$(dirname "$MAPPINGS_FILE")"
        echo "mappings: []" > "$MAPPINGS_FILE"
    fi

    # Build the YAML entry
    local entry="  - subdomain: $subdomain
    target: \"$target\""

    local has_options=false
    local options=""

    if [ "$websocket" = "true" ]; then
        has_options=true
        options="$options
      websocket: true"
    fi

    if [ -n "$health_path" ]; then
        has_options=true
        options="$options
      health_path: $health_path"
    fi

    if [ "$no_buffer" = "true" ]; then
        has_options=true
        options="$options
      buffer_requests: false"
    fi

    if $has_options; then
        entry="$entry
    options:$options"
    fi

    # Check if subdomain already exists
    if grep -q "subdomain: $subdomain\$" "$MAPPINGS_FILE" 2>/dev/null; then
        echo -e "${YELLOW}[WARN]${NC} Subdomain '$subdomain' already exists, updating..."
        remove_service "$subdomain"
    fi

    # Add entry to mappings file
    if $USE_YQ; then
        # Use yq for proper YAML manipulation
        local yaml_entry
        yaml_entry="{\"subdomain\": \"$subdomain\", \"target\": \"$target\""
        if $has_options; then
            yaml_entry="$yaml_entry, \"options\": {"
            [ "$websocket" = "true" ] && yaml_entry="$yaml_entry\"websocket\": true,"
            [ -n "$health_path" ] && yaml_entry="$yaml_entry\"health_path\": \"$health_path\","
            [ "$no_buffer" = "true" ] && yaml_entry="$yaml_entry\"buffer_requests\": false,"
            yaml_entry="${yaml_entry%,}}"
        fi
        yaml_entry="$yaml_entry}"

        yq eval ".mappings += [$yaml_entry]" -i "$MAPPINGS_FILE"
    else
        # Fallback: append to YAML file
        # Remove the empty array notation if present
        sed -i 's/^mappings: \[\]$/mappings:/' "$MAPPINGS_FILE" 2>/dev/null || true

        # Append the new entry
        echo "$entry" >> "$MAPPINGS_FILE"
    fi

    echo -e "${GREEN}[SUCCESS]${NC} Registered: $subdomain -> $target"
}

# Remove service
remove_service() {
    local subdomain="$1"

    if [ ! -f "$MAPPINGS_FILE" ]; then
        echo -e "${RED}[ERROR]${NC} Mappings file not found"
        exit 1
    fi

    if $USE_YQ; then
        yq eval "del(.mappings[] | select(.subdomain == \"$subdomain\"))" -i "$MAPPINGS_FILE"
    else
        # Fallback: use sed (less reliable for complex YAML)
        # This is a simple implementation that works for basic cases
        local temp_file=$(mktemp)
        awk -v sub="$subdomain" '
            BEGIN { skip=0 }
            /^  - subdomain:/ {
                if ($3 == sub) { skip=1; next }
                else { skip=0 }
            }
            skip && /^  - / { skip=0 }
            !skip { print }
        ' "$MAPPINGS_FILE" > "$temp_file"
        mv "$temp_file" "$MAPPINGS_FILE"
    fi

    echo -e "${GREEN}[SUCCESS]${NC} Removed: $subdomain"
}

# Main
main() {
    check_dependencies

    # Parse arguments
    local subdomain=""
    local target=""
    local websocket="false"
    local health_path=""
    local no_buffer="false"
    local remove="false"
    local list="false"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -h|--help)
                usage
                exit 0
                ;;
            --websocket)
                websocket="true"
                shift
                ;;
            --health-path)
                health_path="$2"
                shift 2
                ;;
            --no-buffer)
                no_buffer="true"
                shift
                ;;
            --remove)
                remove="true"
                shift
                ;;
            --list)
                list="true"
                shift
                ;;
            *)
                if [ -z "$subdomain" ]; then
                    subdomain="$1"
                elif [ -z "$target" ]; then
                    target="$1"
                fi
                shift
                ;;
        esac
    done

    # Handle list command
    if [ "$list" = "true" ]; then
        list_services
        exit 0
    fi

    # Validate arguments
    if [ -z "$subdomain" ]; then
        echo -e "${RED}[ERROR]${NC} Missing subdomain"
        usage
        exit 1
    fi

    # Handle remove command
    if [ "$remove" = "true" ]; then
        remove_service "$subdomain"
        exit 0
    fi

    # Validate target for add
    if [ -z "$target" ]; then
        echo -e "${RED}[ERROR]${NC} Missing target"
        usage
        exit 1
    fi

    # Add the service
    add_service "$subdomain" "$target" "$websocket" "$health_path" "$no_buffer"
}

main "$@"
