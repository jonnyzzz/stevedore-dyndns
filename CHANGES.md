# Changelog

All notable changes to this project will be documented in this file.

## [0.9.4] - 2026-01-06

### Fixed
- **Health Listener HTTP**: Force HTTP on `127.0.0.1:8080` to prevent HTTPS mismatch in container health checks

## [0.9.3] - 2026-01-06

### Added
- **Access Log Streaming**: Caddy access logs are streamed to container stdout via entrypoint tailing
- **WebSocket Access Log Check**: Integration test verifies `/ws` upgrade requests are logged

### Fixed
- **Cloudflare Retries**: Added transient network retry handling for Cloudflare API calls
- **Caddy Access Logging**: Enabled per-site access logs so WebSocket upgrades are recorded

## [0.9.2] - 2026-01-06

### Fixed
- **IPv4 Loopback**: Use explicit `127.0.0.1` instead of `localhost` for upstream targets
  - Prevents failures when `localhost` resolves to `::1` (IPv6) but service only binds IPv4
  - Resolves #5
- **Discovery Poll Refresh**: Fetch services when `/poll` returns `changed=true` without services payload
  - Previously, service changes weren't detected if stevedore omitted the services list
  - Now explicitly calls `/services?ingress=true` when changes are detected
  - Resolves #4

### Security
- **Bind Health Ports to Localhost**: Health check endpoints now bind to `127.0.0.1` only
  - Ports 8080 (Caddy health) and 8081 (Go status) no longer exposed to external interfaces
  - Internal services can still access via localhost

## [0.9.1] - 2026-01-02

### Fixed
- **WebSocket Proxy**: Fixed WebSocket upgrade not working through Caddy proxy
  - Removed incorrect `header_up` directives for Connection/Upgrade headers
  - Changed `versions h1` to `versions 1.1` (correct Caddy syntax)
  - Caddy 2 automatically handles WebSocket upgrade with HTTP/1.1 transport

### Added
- **WebSocket Integration Test**: Comprehensive test verifying WebSocket proxying
  - Tests upgrade handshake, echo messaging, and multiple consecutive messages
  - Uses `solsson/websocat` Docker image as echo server
  - Added `github.com/gorilla/websocket` dependency for testing

## [0.9.0] - 2026-01-02

### Added
- **Automatic Service Discovery**: Services are automatically discovered via stevedore query socket
  - Docker labels (`stevedore.ingress.*`) for services with modifiable docker-compose
  - Stevedore parameters (`STEVEDORE_INGRESS_<SERVICE>_*`) for third-party images
  - Priority: container labels override parameters
- **Event Notifications**: Real-time change notifications from stevedore
  - Support for deployment.*, params.changed events
  - `PollWithEvents()` API for full event details
  - Debug logging for event observability
- **DNS Reconciliation**: Terraform-like DNS state management
  - `IsManagedRecord()` to detect owned DNS records
  - `GetManagedRecordFQDNs()` for FQDN-based cleanup
  - Automatic cleanup of stale DNS records on undeploy
  - Support for both normal mode and prefix mode FQDNs
- **Cloudflare Proxy Mode**: Full mTLS support with Authenticated Origin Pull
  - Automatic SSL mode configuration
  - Individual subdomain records (not wildcards) for Universal SSL
  - Origin protection via Cloudflare client certificate validation
- **Subdomain Prefix Mode**: Flatten subdomain hierarchy for free Universal SSL
  - `SUBDOMAIN_PREFIX=true` converts `app.zone.example.com` to `app-zone.example.com`
- **Version Tracking**: Added VERSION file and version command

### Fixed
- DNS records not cleaned up in prefix mode when services are undeployed
- Case-insensitive FQDN comparison for DNS reconciliation

### Documentation
- Comprehensive CLAUDE.md with architecture, security, and operational modes
- Automatic service discovery section with examples
- Security testing documentation with pentest harness

## [0.1.0] - Initial Development

- Basic dynamic DNS functionality
- Cloudflare DNS integration
- Caddy reverse proxy with automatic HTTPS
- Fritzbox TR-064/UPnP IP detection
- YAML-based service mappings (legacy mode)
