# Permissions

Access is granted **per path**, to a user or a group, as a set of rights:
**read**, **write**, **delete**, **share** and **manage**. The check runs in the
storage layer on every operation — never in an HTTP handler — so no forgotten
endpoint can open a hole.

## How a decision is made

For a given path, Zefile walks from the root down to the path, accumulating the
rules that apply (the user's own and those of their groups). **The most specific
rule wins**, and an explicit deny always beats an inherited allow.

**Ownership** is layered on top: the owner of a file implicitly holds read,
write, delete and share on it. An explicit deny still wins, so an administrator
can lock a path even against the person who uploaded it.

## Traversal

A folder you were granted is reachable through the directories above it —
without those parents exposing their other contents. You see the path down to
your grant, and nothing you were not given.

## Explaining access

Because inheritance makes "who can do what" non-obvious, the interface can
**explain an effective permission**: for a user and a path, it points to the
exact rule that produced the result and at which level of the tree. A plain grid
of checkboxes cannot answer that once inheritance is involved.

## Two rules that never bend

- **The check lives in the storage layer**, not in HTTP handlers.
- **No escalation by sharing** — you cannot share more than you hold. A
  read-only user cannot create a writable link.
