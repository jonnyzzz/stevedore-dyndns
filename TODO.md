# TODO

## Security Issues

### [x] Bind health check ports to localhost only
**Priority**: Medium
**Reported**: 2026-01-06
**Fixed**: v0.9.2

Ports 8080 and 8081 are now bound to `127.0.0.1` only.

---

### [x] Cloudflare API timeout handling
**Priority**: Low
**Reported**: 2026-01-06
**Fixed**: v0.9.3

Occasional Cloudflare API timeouts observed:
```
Failed to get existing DNS records: HTTP request failed: Get "https://api.cloudflare.com/...": dial tcp: lookup api.cloudflare.com: i/o timeout
```

Added retry logic with backoff for transient network failures.

---

### [x] Caddy access logs missing WebSocket traffic
**Priority**: Medium
**Reported**: 2026-01-06
**GitHub**: https://github.com/jonnyzzz/stevedore-dyndns/issues/6
**Fixed**: v0.9.3

The Caddy access log file contains only a handful of `/` and `/health` entries,
making it hard to trace `/ws` upgrade requests. Investigate whether per-site
access logging is enabled and ensure WebSocket upgrades are logged.

---

### [x] Stream Caddy access logs to container stdout
**Priority**: Medium
**Reported**: 2026-01-06
**GitHub**: https://github.com/jonnyzzz/stevedore-dyndns/issues/7
**Fixed**: v0.9.3

We need Caddy access logs in `docker logs` output to trace traffic without
shelling into the container. Add a test, implement log streaming, and verify
that access logs include `/ws` upgrades.
