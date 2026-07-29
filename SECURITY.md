# Security policy

## Current status

Zefile is in early development. **There is no release, and no version is supported for
production use.** Do not deploy it and do not expose it to the internet.

## Reporting a vulnerability

Report privately through
[GitHub Security Advisories](https://github.com/krishna2206/zefile/security/advisories/new).
Please do not open a public issue for a security problem.

Include what you can: affected component, reproduction steps, and impact. A proof of concept
helps but is not required.

You should get an acknowledgement within a week. Once a release exists, this policy will state
concrete response and disclosure timelines; until then no timeline is promised, because promising
one that is not met is worse than promising nothing.

## Scope

In scope: authentication and session handling, the permission engine, path handling and
directory traversal, share links, server-side tasks (URL fetch, archive extraction),
and content-origin isolation.

Out of scope: anything requiring an already-compromised host, denial of service through
legitimate resource use, and findings in dependencies that do not affect Zefile as used.

## Design commitments

These are properties Zefile intends to hold. A report showing any of them broken is a
vulnerability, even if nothing else looks wrong:

- Logging out invalidates the session token immediately and irreversibly.
- No path operation can escape the configured storage root.
- Permission checks happen in the storage layer, so no HTTP endpoint can bypass them.
- Nobody can grant, through a share, a permission they do not hold themselves.
- User-uploaded content cannot execute in the application's origin.
