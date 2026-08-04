# Environment variables

Every setting comes from the environment and is validated at startup.

## Application

### `ZEFILE_APP_URL`

**Required.** The public address of the application origin (the UI and API),
e.g. `https://zefile.example.tld`. Used to build links and to decide whether
session cookies may be marked `Secure` — cookies are `Secure` when this URL uses
`https`.

### `ZEFILE_CONTENT_URL`

*Optional.* The public address of the content origin, on a **different host**
from `ZEFILE_APP_URL`. When set, files are served from this cookieless origin
and can be previewed in place. When unset, the instance runs in single-origin
mode: it hardens itself automatically but sends every file as a download.

Zefile refuses to start if this shares a host with `ZEFILE_APP_URL` — separate
origins are what keep an uploaded file from reaching a session.

## Storage

### `ZEFILE_DATA_DIR`

**Required.** The storage root — the directory users browse. Point it at a host
directory (a bind mount), not a Docker volume, to give Zefile the whole disk.

### `ZEFILE_CONFIG_DIR`

**Required.** Where the SQLite database lives. It must **not** be inside
`ZEFILE_DATA_DIR`; Zefile enforces this, because a database users can list is a
database they can download, password hashes included.

### `ZEFILE_RESERVE_BYTES`

*Optional.* Free space kept in hand before refusing writes. Below the threshold
the service goes read-only — reads, sign-in and deletes still work — rather than
letting the disk fill until SQLite can no longer write. Defaults to a built-in
reserve.

### `ZEFILE_READ_ONLY`

*Optional, default `false`.* When `true`, refuses every write regardless of free
space. Useful for maintenance or a read-only mirror.

::: tip Retention
How long the audit log and the trash are kept is **not** an environment setting —
it is configured at runtime under **Settings → Retention** (admin). See the
[audit log](/features/audit-log).
:::

## Server

### `ZEFILE_LISTEN`

*Optional, default `:8080`.* The address the HTTP server binds.

## Container (image only)

These are read by the container entrypoint, not the binary. They make the
container adopt the user that owns your host directory.

### `PUID` / `PGID` {#puid-pgid}

*Optional, default `1000`/`1000`.* The user and group id the server runs as. Set
them to match the owner of your host storage directory, or you will get a
permission error on the first upload. If the data directory is empty on first
start, Zefile adopts it automatically; an existing tree is left untouched.
