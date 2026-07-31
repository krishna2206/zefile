# Contributing to Zefile

Thanks for helping. This page is short on purpose.

## Development

```bash
make dist   # build the interface and a binary embedding it
make check  # what CI runs: fmt, vet, test, govulncheck
```

The interface lives in `web/`; run `pnpm dev` there against a local backend.

## Pull requests

Work however you like on your branch — commit as often and as messily as you
want. What matters is the **pull request title**, because we **squash-merge** and
that title becomes the single commit on `main`.

The title must be a [Conventional Commit](https://www.conventionalcommits.org/):

```
<type>: <short, lower-case summary>
```

Types: `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`,
`revert`. Use `feat!:` or a `BREAKING CHANGE:` footer for anything that breaks the
API or a deployment.

Examples:

- `feat: share a folder from the context menu`
- `fix: keep access when a shared folder is renamed`

A CI check validates the title, so you find out before a maintainer does.

## Releases

You do not release anything by hand, and you never edit version numbers.

1. When your PR merges, [release-please](https://github.com/googleapis/release-please)
   reads the merged titles and maintains a single open **release pull request**
   that bumps `version.txt` and updates `CHANGELOG.md`.
2. A maintainer merges that release PR when it is time to cut a version. That
   creates the `vX.Y.Z` tag and the GitHub release.

`version.txt` is the single source of truth for the version — the binary,
the Docker image and the interface all read from it. Do not edit it yourself;
release-please owns it.
