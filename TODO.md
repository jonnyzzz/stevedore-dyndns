# TODO

## Security Issues

### [x] Bind health check ports to localhost only
**Priority**: Medium
**Reported**: 2026-01-06
**Fixed**: v0.9.2

Ports 8080 and 8081 are now bound to `127.0.0.1` only.

---

### [ ] Cloudflare API timeout handling
**Priority**: Low
**Reported**: 2026-01-06

Occasional Cloudflare API timeouts observed:
```
Failed to get existing DNS records: HTTP request failed: Get "https://api.cloudflare.com/...": dial tcp: lookup api.cloudflare.com: i/o timeout
```

Consider adding retry logic for transient network failures.
