# Sharing

A share link gives access to a file or folder without an account. Links are
unique, revocable, and served cookieless from the content origin.

## Creating a link

Share any file or folder you can read. A link can carry:

- an **expiry**, after which it stops working;
- an optional **password**, hashed with Argon2id;
- for a folder, browsing confined to the shared subtree.

You cannot share more than you hold — [permissions](/features/permissions) are
enforced at creation.

## Built for download managers

Share links work with download managers: no JavaScript, no cookies, correct
`Range` support, and parallel connections that count as one download rather than
sixteen. Serving them from a separate origin means a shared file can never reach
a session.

## Revoking

Revoking a link takes effect immediately — it even cuts a download already in
progress. Every access is recorded, so you can see whether and when a link was
used.
