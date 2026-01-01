# Stevedore DynDNS

Dynamic DNS and HTTPS reverse proxy service for Stevedore deployments.

## Project Overview

This service acts as an ingress controller for Stevedore-managed services, providing:
- **Dynamic DNS**: Updates Cloudflare DNS records with the host's public IP (IPv4 and IPv6)
- **HTTPS Termination**: Wildcard Let's Encrypt certificates via Cloudflare DNS challenge
- **Reverse Proxy**: Routes subdomains to internal Docker services using Caddy
- **IP Detection**: Queries local router via TR-064/UPnP (Fritzbox) or falls back to manual configuration

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Internet                                  │
│                            │                                     │
│                    ┌───────▼───────┐                            │
│                    │   Cloudflare  │                            │
│                    │  DNS + Proxy  │                            │
│                    └───────┬───────┘                            │
│                            │                                     │
│              *.example.com → Public IP                          │
│                            │                                     │
├────────────────────────────┼────────────────────────────────────┤
│  Host (Raspberry Pi)       │                                     │
│                            │                                     │
│  ┌─────────────────────────▼─────────────────────────────────┐  │
│  │  stevedore-dyndns Container                               │  │
│  │                                                           │  │
│  │  ┌─────────────────┐  ┌────────────────────────────────┐ │  │
│  │  │  IP Detector    │  │  Caddy Reverse Proxy           │ │  │
│  │  │  (Go service)   │  │  - HTTPS on :443               │ │  │
│  │  │                 │  │  - Wildcard cert               │ │  │
│  │  │  - TR-064/UPnP  │  │  - Routes by subdomain         │ │  │
│  │  │  - Cloudflare   │  │                                │ │  │
│  │  │    DNS update   │  │  app1.example.com → app1:8080  │ │  │
│  │  └─────────────────┘  │  app2.example.com → app2:3000  │ │  │
│  │                       └────────────────────────────────┘ │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌───────────────────┐  ┌───────────────────┐                   │
│  │ stevedore-app1    │  │ stevedore-app2    │                   │
│  │ (port 8080)       │  │ (port 3000)       │                   │
│  └───────────────────┘  └───────────────────┘                   │
└──────────────────────────────────────────────────────────────────┘
```

## Technology Stack

- **Caddy**: Reverse proxy with automatic HTTPS (HTTP/2, HTTP/3, WebSocket support)
- **Go**: IP detection service and Cloudflare DNS management
- **Docker**: Containerized deployment compatible with Stevedore
- **Let's Encrypt**: Wildcard certificates via DNS-01 challenge
- **Cloudflare**: DNS management and optional CDN/proxy

## Configuration

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `CLOUDFLARE_API_TOKEN` | Yes | API token with Zone:DNS:Edit permissions |
| `CLOUDFLARE_ZONE_ID` | Yes | Zone ID from Cloudflare dashboard |
| `DOMAIN` | Yes | Base domain (e.g., `example.com`) |
| `ACME_EMAIL` | Yes | Email for Let's Encrypt notifications |
| `FRITZBOX_HOST` | No | Fritzbox IP (default: `192.168.178.1`) |
| `FRITZBOX_USER` | No | Fritzbox username (only if router requires auth) |
| `FRITZBOX_PASSWORD` | No | Fritzbox password (only if router requires auth) |
| `MANUAL_IPV4` | No | Manual IPv4 override |
| `MANUAL_IPV6` | No | Manual IPv6 override |
| `IP_CHECK_INTERVAL` | No | IP check interval (default: `5m`) |
| `LOG_LEVEL` | No | Log level: debug, info, warn, error (default: `info`) |

### Cloudflare API Token Permissions

Create a custom API token at https://dash.cloudflare.com/profile/api-tokens with:
- **Zone:Zone:Read** - Read zone information
- **Zone:DNS:Edit** - Edit DNS records

Restrict to specific zone(s) for minimal permissions.

### Mapping Table (`data/mappings.yaml`)

```yaml
# Subdomain to service mappings
mappings:
  # Route to Docker Compose service by project and service name
  - subdomain: app1
    compose_project: stevedore-myapp
    compose_service: web

  # Route to specific host:port
  - subdomain: api
    target: "192.168.1.100:8080"

  # Route to Docker container by name
  - subdomain: grafana
    container: grafana
    port: 3000

  # Route with custom options
  - subdomain: stream
    target: "media-server:8096"
    options:
      websocket: true
      buffer_requests: false
```

## Directory Structure

```
stevedore-dyndns/
├── CLAUDE.md              # This file (project documentation)
├── AGENTS.md              # Symlink to CLAUDE.md
├── docker-compose.yaml    # Stevedore deployment configuration
├── Dockerfile             # Multi-stage build for Caddy + Go service
├── Caddyfile.template     # Caddy configuration template
├── cmd/
│   └── dyndns/
│       └── main.go        # Main entry point
├── internal/
│   ├── config/            # Configuration loading
│   ├── cloudflare/        # Cloudflare API client
│   ├── ipdetect/          # IP detection (TR-064, UPnP, fallbacks)
│   ├── mapping/           # Mapping table management
│   └── caddy/             # Caddyfile generation
├── scripts/
│   ├── stevedore-setup.sh # Automated stevedore setup script
│   └── register-service.sh # Service registration helper
├── .github/
│   └── workflows/
│       └── ci.yaml        # GitHub Actions CI/CD pipeline
└── data/                  # Runtime data (gitignored)
    └── mappings.yaml      # Service mappings (example in repo)
```

## Stevedore Integration

This project is designed to run as a Stevedore deployment, providing ingress routing for all other Stevedore-managed services.

### Key Features

1. **Environment Variables**: Use `stevedore param set` for secrets
2. **Persistent Storage**: Uses `${STEVEDORE_DATA}` for certificates and state
3. **Shared Configuration**: Uses `${STEVEDORE_SHARED}` for cross-deployment mappings
4. **Health Check**: Exposes `/health` endpoint for Stevedore monitoring
5. **Logging**: Writes to `${STEVEDORE_LOGS}` directory
6. **Host Network**: Uses `network_mode: host` for direct Fritzbox access and simplified routing

### Quick Setup (Automated)

Use the provided setup script for a guided installation:

```bash
# Clone and run setup
git clone git@github.com:jonnyzzz/stevedore-dyndns.git
cd stevedore-dyndns
./scripts/stevedore-setup.sh
```

The script will:
1. Check Stevedore installation
2. Add the repository and configure deploy key
3. Prompt for Cloudflare credentials
4. Create the shared mappings file
5. Deploy the service

### Manual Deployment

```bash
# Add to Stevedore
stevedore repo add dyndns git@github.com:jonnyzzz/stevedore-dyndns.git

# Add deploy key to GitHub (shown by repo add command)
stevedore repo key dyndns

# Set required parameters
stevedore param set dyndns CLOUDFLARE_API_TOKEN "your-token"
stevedore param set dyndns CLOUDFLARE_ZONE_ID "your-zone-id"
stevedore param set dyndns DOMAIN "example.com"
stevedore param set dyndns ACME_EMAIL "[email protected]"

# Optional: Configure Fritzbox (default: 192.168.178.1)
stevedore param set dyndns FRITZBOX_HOST "192.168.178.1"

# Deploy
stevedore deploy sync dyndns
stevedore deploy up dyndns
```

### Cross-Deployment Service Registration

Other Stevedore deployments can register their services with dyndns using the shared mappings file.

#### Method 1: Registration Script

Use the provided helper script from any deployment:

```bash
# Register a service
/opt/stevedore/deployments/dyndns/scripts/register-service.sh myapp localhost:3000

# With WebSocket support
./scripts/register-service.sh chat localhost:8080 --websocket

# With custom health path
./scripts/register-service.sh api localhost:9000 --health-path /api/health

# List registered services
./scripts/register-service.sh --list

# Unregister
./scripts/register-service.sh myapp --remove
```

#### Method 2: Direct YAML Editing

Edit `/opt/stevedore/shared/dyndns-mappings.yaml`:

```yaml
mappings:
  - subdomain: myapp
    target: "localhost:3000"
  - subdomain: api
    target: "localhost:8080"
    options:
      websocket: true
      health_path: /healthz
```

Changes are automatically detected and applied (Caddy reloads within seconds).

### Stevedore Environment Variables

| Variable | Description |
|----------|-------------|
| `STEVEDORE_DEPLOYMENT` | Deployment name (auto-set by Stevedore) |
| `STEVEDORE_DATA` | Persistent data directory (certificates, state) |
| `STEVEDORE_LOGS` | Log files directory |
| `STEVEDORE_SHARED` | Shared storage for cross-deployment communication |

### Mappings File Location

The service checks for mappings in this order:
1. `MAPPINGS_FILE` environment variable (if set)
2. `${STEVEDORE_SHARED}/dyndns-mappings.yaml` (if exists)
3. `${STEVEDORE_DATA}/mappings.yaml` (fallback)

New installations default to the shared location for easier cross-deployment integration.

## Development

### Prerequisites
- Go 1.21+
- Docker with BuildKit
- Access to a Cloudflare account (for testing)

### Local Development
```bash
# Run locally
go run ./cmd/dyndns

# Build Docker image
docker build -t stevedore-dyndns .

# Run with docker-compose
docker-compose up
```

## Security Considerations

- Cloudflare API token should have minimal permissions (Zone:DNS:Edit only)
- Let's Encrypt certificates stored in `${STEVEDORE_DATA}/caddy`
- No secrets stored in repository
- Health endpoint is unauthenticated (by design, for Stevedore monitoring)
- Consider Cloudflare proxy mode for DDoS protection

## Future Enhancements (v2)

- [ ] Web UI for configuration
- [ ] Automatic service discovery from Docker labels
- [ ] Rate limiting per subdomain
- [ ] Authentication middleware (OAuth2, Basic Auth)
- [ ] Metrics and monitoring (Prometheus)
- [ ] Backup/restore for certificates

## Related Projects

- [Stevedore](https://github.com/jonnyzzz/stevedore) - Container orchestration for this service
- [Caddy](https://caddyserver.com/) - The underlying reverse proxy
- [fritzconnection](https://fritzconnection.readthedocs.io/) - Fritzbox TR-064 protocol reference
