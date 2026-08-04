# API

Everything the interface does is a plain JSON API under `/api/v1`. This page is
a hand-written reference to the endpoints and how to authenticate. There is no
generated OpenAPI contract — the only clients are the first-party web and mobile
apps — but the API is stable and usable directly.

## Authentication

Two credentials are accepted and resolve to the same user:

- **Session cookie** — a browser signs in via `POST /api/v1/auth/login` and
  receives an `HttpOnly` cookie. This is what the web UI uses.
- **Bearer token** — a program sends an [API token](/reference/api-tokens) as
  `Authorization: Bearer zefile_live_…`. This is what scripts and integrations
  use.

A token acts with the full authority of the account that created it. Both paths
traverse exactly the same permission checks.

```sh
TOKEN=zefile_live_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
curl -H "Authorization: Bearer $TOKEN" \
  "https://zefile.example.tld/api/v1/fs?path=/"
```

## Errors

Failures return an [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem
document with a machine-readable `code`:

```json
{ "type": "…", "title": "Not signed in", "status": 401,
  "code": "unauthenticated", "detail": "…" }
```

Field-level validation errors add an `errors` map of field name to message.

## Authentication & account

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/auth/login` | Sign in; sets the session cookie, returns a token |
| `POST` | `/api/v1/auth/logout` | Revoke the current session |
| `GET` | `/api/v1/auth/me` | The signed-in account |
| `POST` | `/api/v1/auth/password` | Change password (ends other sessions) |
| `GET` | `/api/v1/auth/sessions` | List active sessions |
| `POST` | `/api/v1/auth/sessions/revoke-others` | Sign out every other device |
| `DELETE` | `/api/v1/auth/sessions/{id}` | End one session |

## Files

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/fs?path=` | List a directory |
| `GET` | `/api/v1/fs/stat?path=` | Metadata for one entry |
| `GET` | `/api/v1/fs/search?q=` | Recursive search (only what you may see) |
| `POST` | `/api/v1/fs/dirs` | Create a directory — `{ "path": "/x" }` |
| `POST` | `/api/v1/fs/move` | Move or rename |
| `POST` | `/api/v1/fs/copy` | Copy (large copies run as a background job) |
| `DELETE` | `/api/v1/fs?path=` | Move to trash |
| `GET` | `/api/v1/fs/link?path=` | Mint a short-lived signed download URL |
| `GET` | `/api/v1/fs/text?path=` | Read a text file (capped at 2 MiB) |
| `POST` | `/api/v1/fs/bundle` | Mint a signed zip link for a selection or folder |
| `GET` | `/api/v1/fs/thumb?path=` | Image/video thumbnail |
| `GET` | `/api/v1/fs/space` | Free space and read-only state |
| `GET` | `/api/v1/fs/checksum?path=` | SHA-256 of a file (cached, or a job to poll) |
| `GET` | `/api/v1/config` | Public instance capabilities and version |

## Sharing

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/shares` | List your share links |
| `POST` | `/api/v1/shares` | Create a share link |
| `DELETE` | `/api/v1/shares/{id}` | Revoke a share link |

## Trash

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/trash` | List trashed entries |
| `POST` | `/api/v1/trash/{id}/restore` | Restore an entry |
| `DELETE` | `/api/v1/trash/{id}` | Delete one entry permanently |
| `DELETE` | `/api/v1/trash` | Empty the trash |

## Background jobs

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/jobs` | List background jobs (copies, etc.) |
| `GET` | `/api/v1/jobs/{id}` | One job's status and progress |

## API tokens

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/tokens` | List your tokens (prefix only) |
| `POST` | `/api/v1/tokens` | Create a token (returned once) |
| `DELETE` | `/api/v1/tokens/{id}` | Revoke a token |

See [API tokens](/reference/api-tokens) for the full flow.

## Administration

These require an administrator account.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/users` | List accounts |
| `PATCH` | `/api/v1/users/{id}` | Promote, disable or update an account |
| `DELETE` | `/api/v1/users/{id}` | Remove an account |
| `GET` | `/api/v1/invitations` · `POST` · `DELETE /{id}` | Manage invitations |
| `GET` | `/api/v1/audit` | Read the audit log (keyset paginated) |
| `GET` | `/api/v1/groups` · `POST` · `DELETE /{id}` | Manage groups |
| `PUT`/`DELETE` | `/api/v1/groups/{id}/members/{userID}` | Group membership |

## Permissions

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/permissions?path=` | Effective permissions at a path |
| `GET` | `/api/v1/access?path=` | Access rules at a path |
| `POST` | `/api/v1/access` | Grant access to a user or group |
| `DELETE` | `/api/v1/access/{id}` | Revoke an access rule |

## Uploads

Large uploads use the resumable [tus](https://tus.io) protocol, so a transfer
that drops resumes where it left off.

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/uploads` | Open a resumable upload session |
| `HEAD` | `/api/v1/uploads/{token}` | Current offset |
| `PATCH` | `/api/v1/uploads/{token}` | Send the next chunk |
| `DELETE` | `/api/v1/uploads/{token}` | Abort an upload |

## Content origin

These are served from the content host (or the app host in single-origin mode),
without a session cookie, with full `Range` support.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/d/{token}/{name}` | Authenticated signed download |
| `GET` | `/z/{token}/{name}.zip` | Streamed zip of a selection or folder |
| `GET` | `/s/{token}` | Public share link (optionally `?p=` subpath) |
