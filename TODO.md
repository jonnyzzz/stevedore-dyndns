# TODO

## Security Issues

### [x] Caddy access log file stays empty (no access entries)
**Priority**: High
**Reported**: 2026-01-06
**GitHub**: https://github.com/jonnyzzz/stevedore-dyndns/issues/8
**Fixed**: v0.9.5

The `caddy-access.log` file remained empty in production even though access logs
were configured and the entrypoint tailed the file to stdout. This made it
impossible to trace `/ws` upgrades or confirm host routing in logs.

Resolved by sending access logs to stdout directly (no access log file needed).

**Suggested fixes:**
1. Confirm the site `log` directive is active and emitting access logs.
2. Ensure the access log includes request host/domain in JSON output.
3. Add a regression test that hits `/` + `/ws` and asserts log output.

---

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

### [x] Stop discovery churn triggering Caddy reload loops
**Priority**: Medium
**Reported**: 2026-01-06
**GitHub**: https://github.com/jonnyzzz/stevedore-dyndns/issues/9
**Fixed**: v0.9.5

In production, the dyndns container repeatedly regenerates the Caddyfile every
poll cycle even when services have not changed, leading to frequent reloads.

**Suggested fixes:**
1. Only regenerate when the service list actually changes.
2. Ignore poll timestamps without service updates, or debounce reloads.
3. Add a test that ensures no reload occurs on repeated identical polls.

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
