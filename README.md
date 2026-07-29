# Zefile

> Because it's not just another file browser — it's *Ze* file browser.

A self-hosted file server in a single binary. Store, organise and share large files
on your own server, with a security model and an API designed in from the start.

---

> [!WARNING]
> **Zefile is not usable yet.** Design is complete, implementation has just begun.
> There is no working server, no release, and no upgrade path. Watch the repository
> if you want to know when that changes.

---

## Why

[File Browser](https://github.com/filebrowser/filebrowser) is archived on 1 September 2026.
Its author explained clearly why it could not be fixed by patching: written without a security
model or API design, it would need a rewrite. Known vulnerabilities remain public and unfixed.

Zefile is that rewrite — a new project, not a fork. Nothing is inherited from the original
codebase except the lessons it documented.

## What it is

- **One binary.** Embedded web UI, no runtime dependencies, no database server.
- **Sessions that actually end.** Opaque server-side tokens, never JWT. Logging out revokes
  the token immediately.
- **Content served from a separate origin**, so an uploaded file can never reach a session.
- **Share links that work with download managers** — no JavaScript, no cookies, correct
  `Range` support, parallel connections.
- **Granular permissions** per path, for users and groups.
- **Resumable uploads**, because a 40 GB transfer that fails at 90% must not start over.
- **No telemetry.** No usage metrics, no update checks, no outbound request you did not ask for.

## What it is not

Stated plainly so nobody has to ask:

- Not a collaborative document editor (no OnlyOffice, no Collabora)
- Not a groupware suite — no calendar, contacts, notes or chat
- No plugin marketplace
- No bidirectional desktop sync client

Zefile aims to be the File Browser that should have existed, not a Nextcloud alternative.

## Design documents

The design is written down before the code, and kept current.

| Document | Contents |
| --- | --- |
| [`docs/conception.html`](docs/conception.html) | Scope, architecture, data model, security, API, deployment |
| [`docs/roadmap.html`](docs/roadmap.html) | 36 work items across 5 phases, with completion criteria |
| [`docs/ui.html`](docs/ui.html) | Interface design, component inventory, states |

## Stack

Go 1.24+ · SQLite (`modernc.org/sqlite`, pure Go) · React + Vite ·
[Material 3 Expressive](https://m3e.language-lit.com/) · Tailwind CSS 4

## Building

```sh
make build
```

Requires Go 1.24 or newer. The web interface is not versioned in the repository — building it
requires Node and pnpm. See `make help` for the available targets.

## Licence

[Apache-2.0](LICENSE).
