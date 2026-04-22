# stevedore-dyndns

Single-container ingress for a home / small-fleet [Stevedore](https://github.com/jonnyzzz/stevedore)
deployment:

- **Dynamic DNS** — keeps Cloudflare A/AAAA records in sync with the
  host's public IP (TR-064/UPnP via Fritzbox, with fallback to an
  external IP-echo service).
- **HTTPS reverse proxy** — [Caddy](https://caddyserver.com/) terminates
  TLS with automatic Let's Encrypt certs via the Cloudflare DNS-01
  challenge and routes subdomains to Docker services.
- **Mixed proxy / direct mode** — some subdomains go through Cloudflare
  proxy (orange cloud + mTLS via Authenticated Origin Pull), others
  terminate TLS directly on the host with their own LE cert.
- **MTProto FakeTLS dispatch (optional)** — port 443 can carry both
  browser HTTPS and [Telegram MTProto proxy](https://github.com/9seconds/mtg)
  traffic on selected direct-mode subdomains.
- **Telegram bot (optional)** — reports generated MTProto secrets and
  exposes `/status` and `/rotate` commands for allow-listed admins.

Deep architecture, env variables, and development notes live in
[CLAUDE.md](./CLAUDE.md). What follows is the shortest path from nothing
to a running deployment.

## Quick Setup with Stevedore

```bash
# 1. Register the repo
stevedore repo add dyndns git@github.com:jonnyzzz/stevedore-dyndns.git

# 2. Core credentials (grab a zone-scoped Cloudflare token with
#    Zone:Read + DNS:Edit permission for your zone)
stevedore param set dyndns CLOUDFLARE_API_TOKEN "<token>"
stevedore param set dyndns CLOUDFLARE_ZONE_ID   "<zone id>"
stevedore param set dyndns DOMAIN               "home.example.com"
stevedore param set dyndns ACME_EMAIL           "[email protected]"

# 3. Optional: run behind Cloudflare orange cloud with Authenticated
#    Origin Pull. Required for multi-level subdomains under free SSL.
stevedore param set dyndns CLOUDFLARE_PROXY  true
stevedore param set dyndns SUBDOMAIN_PREFIX  true

# 4. Optional: service discovery via stevedore
stevedore token get dyndns    # copy the printed token
stevedore param set dyndns STEVEDORE_TOKEN  "<token>"

# 5. Deploy
stevedore deploy sync dyndns
stevedore deploy up   dyndns
stevedore status      dyndns
```

Logs are JSON on `docker logs stevedore-dyndns-dyndns-1`. A JSON status
snapshot is available at `http://127.0.0.1:8081/status` from inside the
host.

## Registering a Service for Ingress

Services opt in via Docker labels on their compose files:

```yaml
services:
  web:
    image: myapp:latest
    labels:
      - "stevedore.ingress.enabled=true"
      - "stevedore.ingress.subdomain=myapp"
      - "stevedore.ingress.port=8080"
      - "stevedore.ingress.websocket=false"
      - "stevedore.ingress.healthcheck=/health"
      - "stevedore.ingress.direct=false"    # true for grey-cloud + own LE cert
```

Or, when the image isn't yours (public image), via per-deployment
parameters (see [CLAUDE.md](./CLAUDE.md#method-2-stevedore-parameters)).

Subdomains dyndns has never seen before receive a `451` fallback so
unknown hosts answer cleanly rather than leaking errors.

## MTProto Proxy (Optional)

When `MTPROTO_DISPATCHER=true`, dyndns binds port 443 itself and peeks
the TLS `ClientHello` SNI:

- SNI is one of the MTProto-bound subdomains → hand the connection to an
  embedded instance of [9seconds/mtg](https://github.com/9seconds/mtg)
  which performs the FakeTLS handshake; non-MTProto-looking traffic
  falls back to Caddy via mtg's own domain-fronting mechanism.
- Everything else → raw TCP-splice to Caddy on an internal loopback port
  that dyndns picks automatically.

Each bound subdomain gets an auto-generated 16-byte secret on first run
plus a `tg://proxy?...` link, both persisted under
`<data-dir>/mtproto/<subdomain>.{secret,tg}` (mode `0600`). If a
Stevedore-discovered service claims the same subdomain, browser traffic
reverse-proxies to that service; otherwise the site responds
`OK, it's 451` so probes get a human-readable reply.

Enable:

```bash
stevedore param set dyndns MTPROTO_DISPATCHER   true
stevedore param set dyndns MTPROTO_SUBDOMAINS   "mtp.home.example.com"
# …then deploy as usual.
```

After deployment, read the secret:

```bash
docker exec stevedore-dyndns-dyndns-1 cat /data/mtproto/mtp.tg
# → tg://proxy?server=mtp.home.example.com&port=443&secret=ee…
```

Open that link on a device running Telegram to import it as a proxy.

Credits to [9seconds/mtg](https://github.com/9seconds/mtg) — that
project does the actual MTProto FakeTLS work; we vendor it as a library
and add the SNI-dispatcher + Caddy wiring.

## Telegram Bot (Optional)

Setting `TELEGRAM_BOT_TOKEN` enables a minimal bot that:

- Posts the current `tg://` link to every chat in
  `TELEGRAM_BOT_CHAT_IDS` on startup and after rotations; repeat posts
  delete the prior message so the chat stays a single live link per
  binding.
- Accepts `/status` and `/rotate <subdomain>` commands from Telegram
  user IDs listed in `TELEGRAM_BOT_ALLOWED_USERS` — only in private
  DMs; in groups the bot is write-only (can be added to post updates,
  never responds to messages).

You do NOT need the bot for the rest of the deployment. The secrets on
disk and the `/status` JSON endpoint are always available; the bot is
just an ergonomic channel to retrieve them.

Enable:

```bash
stevedore param set dyndns TELEGRAM_BOT_TOKEN         "<BotFather token>"
stevedore param set dyndns TELEGRAM_BOT_CHAT_IDS      "<chat id>"     # where messages go
stevedore param set dyndns TELEGRAM_BOT_ALLOWED_USERS "<user id>"     # who may command it
```

Retrieve your Telegram user ID by DMing `@userinfobot` first, or by
sending the new bot any message and reading `getUpdates`:

```bash
curl -sS "https://api.telegram.org/bot<token>/getUpdates"
```

## Development

- Requires Go 1.26 (the toolchain declared in `go.mod`).
- `go test ./...` for unit tests.
- `go test -tags=integration ./...` for the Docker-backed integration
  tests; they need a local Docker daemon.
- See [CLAUDE.md § Agent Recipes](./CLAUDE.md#agent-recipes) for the
  common CI + logs workflow.

## Links

- **Orchestration**: [Stevedore](https://github.com/jonnyzzz/stevedore).
- **Reverse proxy**: [Caddy](https://caddyserver.com/).
- **MTProto library**: [9seconds/mtg](https://github.com/9seconds/mtg).
- **DNS + optional proxy**: [Cloudflare](https://www.cloudflare.com/).

## License

MIT. mtg itself is also MIT (see
[9seconds/mtg/LICENSE](https://github.com/9seconds/mtg/blob/master/LICENSE)).
