# Configuration

Everything is configured through **environment variables**. A self-hosted
service is deployed from a compose file far more often than from a config file,
and one source of truth avoids the question of which wins.

Validation is strict and happens at startup: a misconfigured instance refuses to
start with a clear message rather than failing later in a way that looks like a
bug.

## Minimal configuration

The smallest valid configuration sets the public address and where data and the
database live:

```yaml
environment:
  ZEFILE_APP_URL: https://zefile.example.tld
  ZEFILE_DATA_DIR: /data
  ZEFILE_CONFIG_DIR: /config
```

`ZEFILE_CONFIG_DIR` must **not** be inside `ZEFILE_DATA_DIR` — a database users
can browse is a database they can download, password hashes included. Zefile
refuses to start otherwise.

## Key choices

- **Two origins vs one** — set `ZEFILE_CONTENT_URL` to a second hostname to
  enable in-place previews. See [One origin, or two?](/guide/installation#one-origin-or-two).
- **HTTPS** — session cookies are marked `Secure` automatically when
  `ZEFILE_APP_URL` uses `https`. A `Secure` cookie sent over plain HTTP is
  silently dropped by the browser, so always terminate TLS in front of Zefile
  in production.
- **File ownership** — nothing to configure. Zefile adopts whoever owns the data
  directory you mount, so a folder you own just works. It will not run as root:
  if the mounted folder is root-owned, `chown` it to any non-root user once.

The full list, with defaults and validation rules, is in the
[Environment variables reference](/reference/environment).
