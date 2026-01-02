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
| `CLOUDFLARE_PROXY` | No | Enable Cloudflare proxy mode with mTLS (default: `false`) |
| `SUBDOMAIN_PREFIX` | No | Use prefix mode for subdomains (default: `false`) |
| `DNS_TTL` | No | DNS record TTL in seconds (default: IP check interval, min 60) |
| `STEVEDORE_SOCKET` | No | Path to stevedore query socket (default: `/var/run/stevedore/query.sock`) |
| `STEVEDORE_TOKEN` | No | Auth token for service discovery (get via `stevedore token get dyndns`) |

## Two Operational Modes

This service supports two distinct operational modes. Choose based on your requirements:

---

### Mode 1: Direct Mode (Default)

**Configuration:** `CLOUDFLARE_PROXY=false` (default)

```
┌─────────────┐      DNS (grey cloud)      ┌─────────────────────┐
│   Client    │ ──────────────────────────▶│   Your Server       │
│             │         HTTPS              │   (Caddy + dyndns)  │
│             │ ──────────────────────────▶│                     │
└─────────────┘                            └─────────────────────┘
```

**How it works:**
- DNS records point directly to your server's public IP (grey cloud in Cloudflare)
- **Wildcard DNS**: Creates `*.home.example.com` and `home.example.com` A/AAAA records
- **Let's Encrypt certificates**: Wildcard cert obtained via DNS-01 challenge
- **TLS termination**: Caddy handles all HTTPS on your server
- **IP visible**: Your server's IP is visible in DNS lookups

**Best for:**
- Simple setups with full control over certificates
- When you don't need DDoS protection
- When you want direct connections without intermediaries

**Required API token permissions:**
- Zone:Zone:Read
- Zone:DNS:Edit

---

### Mode 2: Cloudflare Proxy Mode (Orange Cloud + mTLS)

**Configuration:**
```bash
CLOUDFLARE_PROXY=true
SUBDOMAIN_PREFIX=true   # Required for Universal SSL on sub-subdomains
```

```
┌─────────────┐    DNS (orange cloud)    ┌─────────────┐      mTLS       ┌────────────────┐
│   Client    │ ────────────────────────▶│  Cloudflare │ ───────────────▶│  Your Server   │
│             │        HTTPS             │   Edge      │   (SSL Full)    │  (Caddy)       │
└─────────────┘                          └─────────────┘                 └────────────────┘
                                              │
                                              │ Authenticated Origin Pull
                                              │ (client certificate)
                                              ▼
                                         Only Cloudflare
                                         can connect to origin
```

**How it works:**
1. **Orange Cloud Enabled**: All DNS records proxied through Cloudflare
2. **Individual Subdomain Records**: Creates separate A records for each active service (not wildcards)
3. **SSL Mode "Full"**: Cloudflare connects to your origin on port 443 (auto-configured)
4. **Authenticated Origin Pull (mTLS)**: Caddy requires Cloudflare's client certificate
5. **Origin Protection**: Direct connections to your server are rejected (only Cloudflare allowed)
6. **IPv4 Only to Origin**: Cloudflare provides IPv6 to clients automatically

**Security layers:**
- DDoS protection via Cloudflare edge network
- Origin IP hidden from public DNS
- mTLS ensures only Cloudflare can reach your origin
- Direct connections get SSL handshake errors

**Best for:**
- Production deployments needing DDoS protection
- When you want to hide your origin IP
- Services exposed to the public internet

**Required API token permissions (for auto-configuration):**
- Zone:Zone:Read
- Zone:DNS:Edit
- Zone:Zone Settings:Edit (for SSL mode)
- Zone:SSL and Certificates:Edit (for Authenticated Origin Pull)

**Note:** If your token lacks SSL permissions, the service still works but you'll need to manually configure SSL mode and AOP in the Cloudflare dashboard.

---

### Subdomain Prefix Mode (SUBDOMAIN_PREFIX)

**Problem:** Cloudflare Universal SSL (free) only covers single-level subdomains:
- ✅ `app.example.com` - covered
- ❌ `app.home.example.com` - NOT covered (requires paid Advanced Certificate Manager)

**Solution:** Enable prefix mode to flatten subdomain hierarchy:
```bash
SUBDOMAIN_PREFIX=true
```

| Without Prefix | With Prefix | SSL Coverage |
|----------------|-------------|--------------|
| `app.home.example.com` | `app-home.example.com` | ✅ Free Universal SSL |
| `api.home.example.com` | `api-home.example.com` | ✅ Free Universal SSL |

**When to use:**
- Your base domain is already a subdomain (e.g., `home.example.com`)
- Using Cloudflare proxy mode (required for Universal SSL)
- You want free SSL without purchasing Advanced Certificate Manager

---

### All Cloudflare Features Used Are FREE

This service only uses features available on Cloudflare's **free plan**:
- ✅ DNS management (A/AAAA records)
- ✅ Proxy mode (orange cloud)
- ✅ Universal SSL (edge certificates)
- ✅ SSL/TLS encryption mode settings
- ✅ Authenticated Origin Pull (mTLS) - **FREE on all plans**
- ✅ DDoS protection (basic)

**No paid features required:**
- ❌ Advanced Certificate Manager (avoided via prefix mode)
- ❌ Argo Tunnel
- ❌ Load Balancing
- ❌ Rate Limiting (paid tier)

### Cloudflare API Token Permissions

Create a custom API token at https://dash.cloudflare.com/profile/api-tokens with:

**Basic (DNS only):**
- **Zone:Zone:Read** - Read zone information
- **Zone:DNS:Edit** - Edit DNS records

**Full (with proxy mode auto-configuration):**
- **Zone:Zone:Read** - Read zone information
- **Zone:DNS:Edit** - Edit DNS records
- **Zone:Zone Settings:Edit** - Configure SSL mode
- **Zone:SSL and Certificates:Edit** - Enable Authenticated Origin Pull

Restrict to specific zone(s) for minimal permissions.

**Note**: If the token lacks SSL permissions, the service will still work but won't auto-configure Cloudflare SSL settings. You'll need to manually configure:
- SSL/TLS encryption mode: "Full" or "Full (strict)"
- Authenticated Origin Pulls: Enabled

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
├── CHANGES.md             # Changelog with release notes
├── VERSION                # Current version (semver)
├── docker-compose.yaml    # Stevedore deployment configuration
├── Dockerfile             # Multi-stage build for Caddy + Go service
├── Caddyfile.template     # Caddy configuration template
├── cmd/
│   └── dyndns/
│       └── main.go        # Main entry point
├── internal/
│   ├── config/            # Configuration loading
│   ├── cloudflare/        # Cloudflare API client (with DNS reconciliation)
│   ├── discovery/         # Stevedore service discovery client
│   ├── ipdetect/          # IP detection (TR-064, UPnP, fallbacks)
│   ├── mapping/           # Mapping table management (legacy)
│   └── caddy/             # Caddyfile generation
├── scripts/
│   ├── entrypoint.sh      # Container entrypoint
│   ├── stevedore-setup.sh # Automated stevedore setup script
│   └── register-service.sh # Service registration helper (legacy)
├── .github/
│   └── workflows/
│       └── ci.yaml        # GitHub Actions CI/CD pipeline
└── data/                  # Runtime data (gitignored)
    └── mappings.yaml      # Service mappings (legacy, use discovery instead)
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

# Enable service discovery (recommended)
stevedore token get dyndns
stevedore param set dyndns STEVEDORE_TOKEN "<token-from-above>"

# Optional: Enable Cloudflare proxy mode (production recommended)
stevedore param set dyndns CLOUDFLARE_PROXY true
stevedore param set dyndns SUBDOMAIN_PREFIX true

# Optional: Configure Fritzbox (default: 192.168.178.1)
stevedore param set dyndns FRITZBOX_HOST "192.168.178.1"

# Deploy
stevedore deploy sync dyndns
stevedore deploy up dyndns

# Verify
stevedore status dyndns
```

### Cross-Deployment Service Registration (Legacy)

> **Note:** With STEVEDORE_TOKEN configured, services are discovered automatically via Docker labels or stevedore parameters. The methods below are legacy and only needed if automatic discovery is disabled.

Other Stevedore deployments can register their services with dyndns using the shared mappings file.

#### Public URL Configuration

Each service is responsible for configuring its own public URL. Since each service gets its own subdomain, there's no single shared domain that applies to all services. Configure the public URL at the service level:

```bash
# Example for a service called "roomtone" with subdomain "roomtone"
# When DOMAIN=home.example.com and SUBDOMAIN_PREFIX=false:
stevedore param set roomtone PUBLIC_URL "https://roomtone.home.example.com"

# When DOMAIN=home.example.com and SUBDOMAIN_PREFIX=true:
stevedore param set roomtone PUBLIC_URL "https://roomtone-home.example.com"
```

The URL pattern depends on your dyndns configuration:
- **Normal mode** (`SUBDOMAIN_PREFIX=false`): `https://<subdomain>.<DOMAIN>`
- **Prefix mode** (`SUBDOMAIN_PREFIX=true`): `https://<subdomain>-<zone>.<parent-domain>`

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

### API Token Permissions
- Cloudflare API token should have minimal permissions (Zone:DNS:Edit only)
- Token should be scoped to specific zone when possible

### Domain-Scoping Security Assertions
The Cloudflare client includes built-in security assertions that prevent DNS modifications outside the configured domain:

```go
// All DNS operations go through validateRecordName() which ensures:
// 1. Record name equals the configured domain, OR
// 2. Record name is a subdomain of the configured domain

// Examples for DOMAIN=home.example.com:
// ALLOWED: home.example.com, app.home.example.com, *.home.example.com
// BLOCKED: example.com, other.example.com, evil.com
```

This prevents:
- Accidental modification of parent domain records
- Attacks via prefix confusion (e.g., `fakehome.example.com`)
- Attacks via suffix confusion (e.g., `home.example.com.evil.com`)

The validation runs on every `UpdateRecord` and `DeleteRecord` call, and failures are logged with "SECURITY" prefix for easy monitoring.

### Other Security Measures
- Let's Encrypt certificates stored in `${STEVEDORE_DATA}/caddy`
- No secrets stored in repository
- Health endpoint is unauthenticated (by design, for Stevedore monitoring)
- Consider Cloudflare proxy mode for DDoS protection

## Security Testing

### Automated Security Test Harness

The `pentest-harness/` directory contains Docker-based security tests:

```bash
cd pentest-harness

# Configure targets
cp .env.example .env
# Edit .env with your deployment details

# Run quick tests (connectivity, headers, ports, TLS, Cloudflare proxy)
./scripts/run_all.sh --quick --proxy

# Run full test suite (includes ZAP, Nuclei)
./scripts/run_all.sh --proxy
```

### Test Categories

| Test | Description | Safe for Prod |
|------|-------------|---------------|
| Connectivity | DNS resolution, basic HTTP | Yes |
| HTTP Headers | Security headers (HSTS, CSP, etc.) | Yes |
| Port Scan | nmap open port detection | Yes |
| TLS Audit | Protocol/cipher analysis | Yes |
| Cloudflare Proxy | mTLS verification, origin protection | Yes |
| ZAP Baseline | OWASP passive web scan | Yes |
| Nuclei | Template vulnerability checks | Rate-limited |
| Feroxbuster | Content discovery | Aggressive |

### Cloudflare Proxy Mode Tests

Critical tests for proxy mode (`05_cloudflare_proxy.sh`):

1. **Proxy Active** - Verifies CF-Ray header is present
2. **mTLS Origin Protection** - Direct connection to origin MUST fail with SSL error
3. **Connection via Cloudflare** - Normal HTTPS access works
4. **SSL Mode** - Must be "Full" or "Full (strict)"
5. **Authenticated Origin Pull** - Must be enabled
6. **DNS Proxy Status** - DNS must resolve to Cloudflare IPs

### Integration Tests

Run Go integration tests to verify Cloudflare configuration:

```bash
# Run unit tests
go test ./internal/cloudflare/... ./internal/caddy/...

# Run integration tests (requires credentials)
export CLOUDFLARE_API_TOKEN="your-token"
export CLOUDFLARE_ZONE_ID="your-zone-id"
go test -v ./internal/cloudflare/...
```

### Manual mTLS Verification

To manually verify mTLS is working:

```bash
# This should FAIL with SSL error (origin is protected)
curl -v --resolve "your.domain:443:ORIGIN_IP" https://your.domain

# This should SUCCEED (via Cloudflare)
curl -v https://your.domain
```

If the direct connection succeeds, mTLS is NOT working - fix immediately.

## Automatic Service Discovery

Services can be automatically discovered and routed without manual configuration. Stevedore provides two methods:

### Method 1: Docker Labels (Recommended)

Add labels to your `docker-compose.yaml`:

```yaml
services:
  web:
    image: myapp:latest
    ports:
      - "8080:8080"
    labels:
      - "stevedore.ingress.enabled=true"
      - "stevedore.ingress.subdomain=myapp"
      - "stevedore.ingress.port=8080"
      - "stevedore.ingress.websocket=false"
      - "stevedore.ingress.healthcheck=/health"
```

| Label | Required | Description |
|-------|----------|-------------|
| `stevedore.ingress.enabled` | Yes | Must be `true` to enable routing |
| `stevedore.ingress.subdomain` | Yes | Subdomain for this service |
| `stevedore.ingress.port` | Yes | Container port to route to |
| `stevedore.ingress.websocket` | No | Enable WebSocket support (default: `false`) |
| `stevedore.ingress.healthcheck` | No | Health check path (default: `/health`) |

### Method 2: Stevedore Parameters

For deployments where you cannot modify the docker-compose file (public images, upstream repos), use stevedore parameters:

```bash
# Configure ingress for service "web" in deployment "nginx"
stevedore param set nginx STEVEDORE_INGRESS_WEB_ENABLED true
stevedore param set nginx STEVEDORE_INGRESS_WEB_SUBDOMAIN mysite
stevedore param set nginx STEVEDORE_INGRESS_WEB_PORT 80
stevedore param set nginx STEVEDORE_INGRESS_WEB_HEALTHCHECK /

# For service names with dashes (e.g., "my-api-server"):
# Convert to uppercase and replace dashes with underscores
stevedore param set app STEVEDORE_INGRESS_MY_API_SERVER_ENABLED true
stevedore param set app STEVEDORE_INGRESS_MY_API_SERVER_SUBDOMAIN api
stevedore param set app STEVEDORE_INGRESS_MY_API_SERVER_PORT 3000
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `STEVEDORE_INGRESS_<SERVICE>_ENABLED` | Yes | Must be `true` to enable routing |
| `STEVEDORE_INGRESS_<SERVICE>_SUBDOMAIN` | Yes | Subdomain for this service |
| `STEVEDORE_INGRESS_<SERVICE>_PORT` | Yes | Container port to route to |
| `STEVEDORE_INGRESS_<SERVICE>_WEBSOCKET` | No | Enable WebSocket support |
| `STEVEDORE_INGRESS_<SERVICE>_HEALTHCHECK` | No | Health check path |

**Service name convention:** Uppercase, dashes converted to underscores (e.g., `my-web-app` → `MY_WEB_APP`)

### Priority Rules

When both Docker labels and parameters exist for a service:
1. **Docker labels take precedence** - explicit labels in docker-compose override parameters
2. **Parameters as fallback** - applied when container has no ingress labels

### Example: Routing nginx (Public Image)

```bash
# Deploy nginx (no labels in official image)
stevedore repo add nginx https://github.com/myorg/nginx-deployment.git
stevedore deploy up nginx

# Configure ingress via parameters
stevedore param set nginx STEVEDORE_INGRESS_NGINX_ENABLED true
stevedore param set nginx STEVEDORE_INGRESS_NGINX_SUBDOMAIN www
stevedore param set nginx STEVEDORE_INGRESS_NGINX_PORT 80
stevedore param set nginx STEVEDORE_INGRESS_NGINX_HEALTHCHECK /

# Service is now accessible at https://www.home.example.com (or www-home.example.com in prefix mode)
```

## Future Enhancements (v2)

- [ ] Web UI for configuration
- [ ] Rate limiting per subdomain
- [ ] Authentication middleware (OAuth2, Basic Auth)
- [ ] Metrics and monitoring (Prometheus)
- [ ] Backup/restore for certificates

## Release Workflow

### Version Management

- Version is stored in `VERSION` file (single line, semver format: `MAJOR.MINOR.PATCH`)
- Increment VERSION when adding new features:
  - MAJOR: Breaking changes or major new functionality
  - MINOR: New features, significant improvements
  - PATCH: Bug fixes, documentation updates

### Changelog

Maintain `CHANGES.md` with release notes:
- Group changes by release version
- Use sections: Added, Fixed, Changed, Removed, Documentation
- Reference resolved issues with `Resolves #N`

### Release Process

1. Increment VERSION file
2. Update CHANGES.md with new release section
3. Run `go test ./...` and `go build ./...`
4. Commit: `git commit -m "release: vX.Y.Z"`
5. Push and wait for CI to pass
6. Deploy to production via `stevedore deploy sync dyndns && stevedore deploy up dyndns`
7. Verify deployment with `stevedore status dyndns`

### Build-time Version Injection

The Dockerfile injects version info via ldflags:
- `Version` - from VERSION file
- `GitCommit` - short git commit hash
- `BuildDate` - UTC build timestamp

Version is logged at startup:
```json
{"level":"INFO","msg":"Starting stevedore-dyndns","version":"0.9.1","commit":"9db62af","build_date":"2026-01-02T18:19:52Z"}
```

## Development Practices

Following stevedore project conventions:

1. **Test-First Approach**: All bugs are fixed by first adding a failing test
2. **Documentation**: Keep CLAUDE.md and docs consistent with implementation
3. **Industry Best Practices**: Follow Go and DevOps conventions from Docker, Kubernetes ecosystem
4. **Integration Tests**: Verify end-to-end functionality
5. **No Ignored Warnings**: Fix all warnings properly

### Feature Implementation Checklist

- [ ] Read and understand requirements
- [ ] Write tests for new functionality
- [ ] Implement the feature
- [ ] Run `go build ./...` and `go test ./...`
- [ ] Update CLAUDE.md with feature documentation
- [ ] Update CHANGES.md with release notes
- [ ] Increment VERSION file
- [ ] Commit with descriptive message
- [ ] Push and verify CI passes
- [ ] Deploy to production and verify

## Related Projects

- [Stevedore](https://github.com/jonnyzzz/stevedore) - Container orchestration for this service
- [Caddy](https://caddyserver.com/) - The underlying reverse proxy
- [fritzconnection](https://fritzconnection.readthedocs.io/) - Fritzbox TR-064 protocol reference
