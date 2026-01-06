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

---

## Stevedore Follow-ups

### [ ] Stevedore deploy should be idempotent/attachable
**GitHub**: https://github.com/jonnyzzz/stevedore/issues/12

stevedore `deploy sync`/`deploy up` should attach to an in-flight operation if re-run
and avoid launching duplicate concurrent jobs. The sequence
`stevedore deploy sync dyndns && stevedore deploy up dyndns && stevedore status dyndns`
should be resumable if interrupted.

### [ ] Stevedore CLI guidance for AI-friendly usage
**GitHub**: https://github.com/jonnyzzz/stevedore/issues/13

Stevedore should expose recommended workflows, log locations, and machine-readable
status hints in CLI help/output so automated agents can follow the correct patterns.
