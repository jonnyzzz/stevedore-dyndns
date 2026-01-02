# Changelog

All notable changes to this project will be documented in this file.

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
