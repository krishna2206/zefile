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

Back up two things separately: your **data directory** (the files — use your
usual disk backup) and the **database**, which holds every account, permission,
share and token. Losing the database loses all of that even though the files
survive, so it is the critical half.

Zefile has built-in commands for the database. `backup` is safe to run while the
server is live — it takes a consistent snapshot with SQLite's `VACUUM INTO`:

```sh
docker compose exec zefile zefile backup
# → /config/backups/zefile-2026-08-03-020000.db
```

Pass a path to choose where it lands — handy for a nightly cron:

```sh
docker compose exec zefile zefile backup /config/backups/nightly.db
```

The snapshot is a self-contained SQLite file you can copy off the host.

To restore, **stop the server first** — replacing the database under a running
instance would corrupt it. `restore` validates the snapshot and copies the
current database aside (as `zefile.db.pre-restore-…`) before replacing it:

```sh
docker compose stop zefile
docker compose run --rm zefile zefile restore /config/backups/nightly.db
docker compose start zefile
```
