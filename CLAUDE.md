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
| `FRITZBOX_USER` | No | Fritzbox username (if auth required) |
| `FRITZBOX_PASSWORD` | No | Fritzbox password (if auth required) |
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
└── data/                  # Runtime data (gitignored)
    └── mappings.yaml      # Service mappings (example in repo)
```

## Stevedore Integration

This project is designed to run as a Stevedore deployment:

1. **Environment Variables**: Use `stevedore param set` for secrets
2. **Persistent Storage**: Uses `${STEVEDORE_DATA}` for certificates and state
3. **Health Check**: Exposes `/health` endpoint for Stevedore monitoring
4. **Logging**: Writes to `${STEVEDORE_LOGS}` directory

### Deployment

```bash
# Add to Stevedore
stevedore repo add dyndns git@github.com:jonnyzzz/stevedore-dyndns.git

# Set required parameters
stevedore param set dyndns CLOUDFLARE_API_TOKEN "your-token"
stevedore param set dyndns CLOUDFLARE_ZONE_ID "your-zone-id"
stevedore param set dyndns DOMAIN "example.com"
stevedore param set dyndns ACME_EMAIL "[email protected]"

# Deploy
stevedore deploy sync dyndns
stevedore deploy up dyndns
```

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
