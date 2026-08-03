# Audit log

A multi-user system has to be able to answer "what happened?". Zefile records
every sensitive action with a timestamp and the account that performed it.

## What is recorded

- Sign-ins, successful and failed
- Permission changes and access grants
- API token creation and revocation
- Account and group changes
- File deletions and permanent purges

## Reading it

The audit log is visible to administrators from the **Activity** screen, with
keyset pagination for a large history. Each entry carries the actor, the action,
the target and the client IP.

Because the IP is recorded, the log is data you should not keep indefinitely by
default. A configurable retention period is planned.
