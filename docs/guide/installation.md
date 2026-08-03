# Installation

Zefile ships as a single container image on GitHub Container Registry. The
quickest way to run it is Docker Compose.

## Requirements

- Docker with the Compose plugin
- A directory on the host for your files
- Two hostnames pointing at the server (recommended — see
  [why two origins](#one-origin-or-two))

## Docker Compose

Create a `docker-compose.yml`:

```yaml
services:
  zefile:
    image: ghcr.io/krishna2206/zefile:latest
    restart: unless-stopped
    environment:
      ZEFILE_APP_URL: https://zefile.example.tld
      ZEFILE_CONTENT_URL: https://dl.example.tld
      ZEFILE_DATA_DIR: /data
      ZEFILE_CONFIG_DIR: /config

      # The user that owns your storage directory on the host. Getting this
      # wrong shows up as a permission error on the first upload.
      PUID: 1000
      PGID: 1000

    volumes:
      # A directory on the host, not a Docker volume: that is what gives Zefile
      # the whole disk instead of a partition of it.
      - /mnt/storage:/data
      # The database lives apart from the browsable tree.
      - zefile-config:/config

    ports:
      - "8080:8080"

volumes:
  zefile-config:
```

Start it:

```sh
docker compose up -d
docker compose logs zefile
```

::: tip Pin a version
`:latest` follows every release. For a reproducible deployment, pin a tag such
as `ghcr.io/krishna2206/zefile:v0.6.0`.
:::

## First run

**There is no default account.** Shipping known credentials would mean every
instance is compromised between deployment and the moment someone changes them.

Instead, the log prints a **one-time setup link** on first start:

```
open http://localhost:8080/setup?token=… to create the administrator
```

Open it, create the administrator account, and you are in. The link expires
after 24 hours; restart the container to mint a fresh one if you miss it.

## One origin, or two?

Zefile serves two things: the **application** (the UI and API, which use a
session cookie) and the **content** (the files themselves). Serving them from
two different hostnames is what guarantees a downloaded or shared file can never
carry a session cookie.

- **Two origins (recommended).** Set both `ZEFILE_APP_URL` and
  `ZEFILE_CONTENT_URL` to different hostnames. Enables in-place preview of
  images, video, audio, text and PDF.
- **One origin (supported).** Leave `ZEFILE_CONTENT_URL` unset. The instance
  hardens itself automatically but sends every file as a download instead of
  previewing it in place.

See [Deployment](/guide/deployment) for a reverse-proxy example, and
[Environment variables](/reference/environment) for the full list.

## Building from source

To run exactly what is on a branch rather than a published image, the repository
ships a compose file that builds the image locally:

```sh
docker compose -f deploy/docker-compose.build.yml up -d
```

Or build the binary directly (requires Go 1.25 and pnpm):

```sh
make dist   # builds the web UI and embeds it, then compiles the binary
./bin/zefile
```
