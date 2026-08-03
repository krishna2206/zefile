# API tokens

An API token is a long-lived credential you create to let a **program** act on
your behalf — a backup script, a CI job, an integration. A browser uses a
session cookie; a program uses a token.

## What a token can do

A token carries the **full authority of the account that created it** — the same
permissions and the same file access, resolved fresh on every request. There are
no per-token scopes: the model is deliberately simple, as
[Dokploy](https://dokploy.com) does it. Changing your account's rights takes
effect on its tokens immediately.

The mitigation for a leaked token is **revocation**, not restriction — so create
one token per use, and revoke the one that leaks.

## Creating a token

In the interface: **Settings → API tokens → New token**. Give it a name and an
optional expiry. The token is shown **once**, at creation — copy it then; only a
short prefix and the last-used time remain afterwards.

Tokens are recognisable by their `zefile_live_` prefix, so a leaked one is easy
to spot in a log or a repository.

## Using a token

Send it as a bearer credential:

```sh
TOKEN=zefile_live_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

# List a directory
curl -H "Authorization: Bearer $TOKEN" \
  "https://zefile.example.tld/api/v1/fs?path=/"

# Get a short-lived signed download link, then fetch the file
curl -H "Authorization: Bearer $TOKEN" \
  "https://zefile.example.tld/api/v1/fs/link?path=/reports/q3.pdf"
```

## Revoking a token

**Settings → API tokens → Revoke.** It stops working on the next request. If an
account is disabled or deleted, all of its tokens stop working at once.

## Lifecycle at a glance

| Event | Effect on the token |
| --- | --- |
| You change the account's permissions | Applied immediately, next request |
| You change your password | No effect — a token survives password changes |
| You revoke the token | Stops working immediately |
| The account is disabled or deleted | Stops working immediately |
