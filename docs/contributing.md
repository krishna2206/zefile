# Contributing

Zefile is Apache-2.0 and welcomes contributions. The full guide lives in the
repository at
[`CONTRIBUTING.md`](https://github.com/krishna2206/zefile/blob/main/CONTRIBUTING.md);
this page is a short orientation.

## Design documents

The design was written down before the code and is kept in the repository under
[`docs/design/`](https://github.com/krishna2206/zefile/tree/main/docs/design).
They are the reference for the *what* and the *why*:

- **conception.html** — scope, architecture, data model, security, API, deployment
- **roadmap.html** — the work plan and what has shipped
- **ui.html** — interface design, component inventory, states

## Working on the code

- Backend is Go 1.25 with pure-Go SQLite; run `make check` before a PR.
- Frontend is React + Vite in `web/`.
- Commits follow [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, …); releases are cut automatically by
  release-please from those messages.

## Working on these docs

This site is built with [VitePress](https://vitepress.dev). From `docs/`:

```sh
pnpm install
pnpm dev        # local preview with hot reload
pnpm build      # production build
```
