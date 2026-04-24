# Changelog

All notable changes to this project will be documented in this file.

## [0.12.1] - 2026-04-24

### Changed
- Pinned all Go build/test images to Go 1.26.2. The production Docker build
  and the integration-test image now use the same Go 1.26 line.

## [0.12.0] - 2026-04-22

### Added
- **README.md** with a setup-oriented overview, explicit link to
  [9seconds/mtg](https://github.com/9seconds/mtg) as the MTProto library,
  and a clear "Telegram bot is optional" callout.
- **MTProto FQDN bindings**: `MTPROTO_SUBDOMAINS` entries that contain a dot
  are accepted as fully qualified hostnames (e.g. a sibling zone), bypassing
  prefix-mode substitution.
- **Per-kind Telegram message dedup**: the bot tracks one live message per
  `(chat_id, kind)` and deletes the prior post when a new one is published
  for the same kind. The startup binding announcement and the rotation
  message for the same FQDN collapse into a single chat message.

### Fixed
- **Compose env-var forwarding**: MTProto / Telegram / CATCHALL variables
  are now listed in `docker-compose.yaml`'s environment block. Previously,
  setting them via `stevedore param set` had no effect on the container.
- **Container data paths**: `DYNDNS_DATA` / `DYNDNS_LOGS` now point at the
  container-side mount paths (`/data`, `/var/log/dyndns`) instead of the
  host path, so MTProto secrets persist across rebuilds.
- **Telegram API decoder**: accept unknown fields in Bot-API responses —
  `getMe` and `sendMessage` return many optional fields we don't model and
  strict decoding broke the bot on startup.
- **Caddy TLS policy ordering**: direct-mode, MTProto-bound, and catchall
  sites now emit `client_auth { mode request }` as a policy differentiator.
  Without it, Caddyfile merged non-mTLS sites into an empty catch-all
  policy that lost the SNI-match walk to the `*.domain` wildcard's
  require_and_verify policy, forcing plain browser clients to fail with
  `tlsv13 alert certificate required`.

## [0.11.0] - 2026-04-22

### Added
- **MTProto FakeTLS Dispatcher (library integration)**: new `internal/mtproto` package embeds `github.com/9seconds/mtg/v2` as a library. When `MTPROTO_DISPATCHER=true`, dyndns binds `:443`, peeks the TLS ClientHello, and for bound SNIs hands the connection to a per-subdomain `mtglib.Proxy`. Non-MTProto traffic is byte-forwarded to Caddy on a loopback port (default `127.0.0.1:8443`). Browser traffic to a bound subdomain reaches Caddy via the domain-fronting path and is served the site's `respond "OK" 200` (no `reverse_proxy`).
- **Per-subdomain secrets**: subdomains listed in `MTPROTO_SUBDOMAINS` get a 16-byte key auto-generated on first run and persisted under `MTPROTO_DATA_DIR` (default `${DataDir}/mtproto`). `<sub>.secret` holds the hex secret and `<sub>.tg` holds the `tg://proxy?...` import URL (both `0600`).
- **Caddy global options**: generator now emits `https_port`, `default_bind 127.0.0.1`, and the bound subdomains as dedicated direct-mode sites when the dispatcher is enabled. Existing deployments (dispatcher off) are unchanged.
- **Telegram bot (optional)**: new `internal/telegram` package. Gated by `TELEGRAM_BOT_TOKEN`; hardcoded user allow-list in `internal/telegram/allowlist.go` (zero placeholder is ignored). Responds to `/status` and `/rotate <subdomain>` in DMs with allow-listed users only; write-only in groups. Broadcasts secret generation / rotation to `TELEGRAM_BOT_CHAT_IDS`. `/rotate` replaces the secret on disk, emits the new `tg://` URL, and cancels the root context so stevedore restarts the service with the fresh secret.
- **`/status` JSON endpoint**: now includes an `mtproto` array with `{subdomain, fqdn, fingerprint}` for each binding (secret itself is never returned over the endpoint).

### Changed
- **Go toolchain**: `go.mod` declared version bumped to `1.26` so upstream `github.com/9seconds/mtg/v2` is usable. CI/Dockerfile already use Go 1.26.
- **`mtglib` adapters**: reuse upstream `network/v2`, `antireplay`, `events`, `ipblocklist`, and write only a thin `slog` logger wrapper — avoids reimplementing five library interfaces.

### Notes
- The dispatcher enforces its own concurrency gate (`MTPROTO_MAX_CONNECTIONS`, default 8192) because `mtglib.Proxy.ServeConn` bypasses mtg's internal listener-side limiter.
- The dispatcher closes non-TLS connections rather than forwarding plaintext to Caddy; Caddy's HTTPS listener can't serve raw bytes meaningfully.
- Allow-list for the bot is compile-time on purpose: a stevedore-param leak cannot grant access.

## [0.10.0] - 2026-04-22

### Added
- **Mixed Proxy/Direct Mode**: Per-service `stevedore.ingress.direct=true` label (and matching structured-ingress field / `STEVEDORE_INGRESS_<SVC>_DIRECT` parameter) publishes a subdomain as grey-cloud (Cloudflare `Proxied=false`). Caddy serves such sites with their own Let's Encrypt cert via DNS-01 and no origin mTLS, while other subdomains keep the existing CF-proxy + authenticated-origin-pull path.
- **Host / SNI Validation**: Unknown Host values inside the wildcard block now respond `451` instead of `404`.
- **451 Catchall Site**: Optional `CATCHALL_SUBDOMAIN` env var enables a dedicated site (own LE cert) plus `default_sni`; any TLS handshake whose SNI does not match a configured site completes against the catchall's cert and receives a `451` response. Caddy emits an AAAA record for direct subdomains in addition to A records.
- **Cloudflare Per-Record Proxy**: New `UpdateRecordProxied` lets callers mix orange-cloud and grey-cloud records within the same zone.
- **Unit Tests**: Direct-mode site emission, 451 fallback, catchall default-SNI, and per-record proxied flag are covered by new tests.

## [0.9.5] - 2026-01-06

### Added
- **Access Log Integration Test**: Verifies access logs are emitted to stdout and include request host
- **Integration Test Runner**: Added Dockerfile.test for cached Docker-based integration runs

### Fixed
- **Discovery Poll Churn**: Skip Caddy regeneration when the effective service list is unchanged
- **Config Reload Guard**: Skip Caddy reload when the generated config is identical
- **Access Log Visibility**: Caddy logs now emit to stdout only (container-native logging)

### Changed
- **Go Toolchain**: Updated CI and Docker build to use Go 1.25

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
