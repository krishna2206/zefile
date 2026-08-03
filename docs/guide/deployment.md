# Deployment

Zefile is one container behind a reverse proxy that terminates TLS. The only
Zefile-specific consideration is the two origins.

## Reverse proxy

Point both hostnames at the same container port (`8080`). Zefile decides which
role a request plays from its `Host` header, so no path rules are needed.

```
zefile.example.tld  ─┐
                     ├─►  zefile:8080
dl.example.tld      ─┘
```

Any proxy works (Caddy, Traefik, nginx). Terminate TLS at the proxy and forward
plain HTTP to the container. Because Zefile derives `Secure` cookies from
`ZEFILE_APP_URL`, make sure that variable uses `https`.

## Dokploy

Zefile deploys cleanly on [Dokploy](https://dokploy.com). Use the reference
compose file, set the two domains in the Dokploy UI, and point the storage
volume at a host directory. Dokploy handles TLS and routing for both hostnames.

## Forwarded headers

Zefile deliberately ignores `X-Forwarded-For` for rate limiting and audit: it is
trivially forged, and trusting it without knowing the proxy in front would let
anyone reset their own rate limit. If you need accurate client IPs in the audit
log, this is a known trade-off favouring safety.

## Backups

Back up the **config volume** (the database) and your **data directory**. The
database holds every account, permission, share and token — losing it loses all
of that even though the files themselves survive. A dedicated backup/restore
command is on the roadmap; until then, snapshot the config volume while the
container is stopped, or copy the SQLite file with the container's own tooling.
