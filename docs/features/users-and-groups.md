# Users & groups

Zefile is multi-user from the ground up. Accounts are created by invitation, and
access is granted to a user or to a whole group.

## Inviting users

There is no open sign-up. An administrator creates a **one-time invitation
link**; the invitee opens it and chooses their own username and password. No
secret ever circulates by email, and the link cannot be reused once accepted.

Administrators manage accounts from the **Members** screen: promote to admin,
disable (which ends every session immediately), or remove.

## Groups

A group is a named set of users. Granting access to a group grants it to
everyone in it — add a person to the group and they inherit every folder the
group can reach, with no per-folder busywork.

Groups are managed by administrators. A user's effective access is the union of
their own grants and those of every group they belong to. See
[Permissions](/features/permissions) for how that union is resolved.

## Two-factor authentication

Each user can add a second factor from **Settings → Two-factor authentication**.
Zefile uses **TOTP** (the six-digit codes from apps like Google Authenticator,
Aegis or 1Password): scan the QR code once, confirm with a code, and from then on
sign-in asks for a code after the password. Verification is offline — no SMS, no
email.

If you lose your authenticator, a [recovery code](#forgotten-passwords) works in
place of the code at sign-in, so a lost device is a recovery rather than a
lockout. Two-factor is per-user and opt-in.

## Forgotten passwords

Zefile never sends email, so password reset works with **recovery codes**
instead. When an account is created, it is shown a set of ten single-use codes
to save. To reset a forgotten password, use **Forgot your password?** on the
sign-in screen: a username and one recovery code set a new password, and every
session is ended in the process.

Each code works once. See how many remain, and generate a fresh set (which
replaces the old one), under **Settings → Recovery codes**. If an account has
run out of codes and forgotten its password, an administrator can invite it
again to restore access.
