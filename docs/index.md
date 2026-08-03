---
layout: home

hero:
  name: Zefile
  text: The file server that should have existed
  tagline: A self-hosted, single-binary file server. Fast transfers, real permissions, sessions that actually end. Apache-2.0.
  actions:
    - theme: brand
      text: Get started
      link: /guide/installation
    - theme: alt
      text: Introduction
      link: /guide/introduction
    - theme: alt
      text: View on GitHub
      link: https://github.com/krishna2206/zefile

features:
  - title: One binary
    details: Embedded web UI, pure-Go SQLite, no runtime dependencies and no database server. Deploy one container.
  - title: Fast, resumable transfers
    details: Resumable uploads (tus) and streamed zip downloads that saturate the disk and network, not the CPU.
  - title: Real permissions
    details: Granular access per path, granted to a user or a group, enforced in the storage layer — never bypassable from an HTTP handler.
  - title: Sessions that end
    details: Opaque server-side tokens, never JWT. Logging out revokes access immediately — the defect at the heart of File Browser.
  - title: Sharing done right
    details: Revocable share links with expiry and optional password, served cookieless from a separate origin with full Range support.
  - title: No telemetry
    details: No usage metrics, no update checks, no outbound request you did not ask for.
---
