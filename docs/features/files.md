# Files

Browse as a list or a grid, sorted and grouped, with image and video thumbnails.
Everything you see is something you are allowed to see: [permissions](/features/permissions)
are enforced on every listing, and a recursive [search](/features/search) never
returns a path you could not otherwise reach.

## Uploading

Drag in files or a whole folder tree. Uploads are **resumable** (tus): a 40 GB
transfer that drops at 90% continues from where it stopped rather than starting
over. A partial upload lives outside any listing until it completes, then appears
in one atomic step.

## Copying and moving

Copy, move and rename in place. A rename or move is instant — it never falls
back to copying bytes across the disk. Copying a folder or a very large file
runs as a background [job](#background-jobs) so a long copy never ties up the
request.

## Downloading

Download through a short-lived **signed link** that works with download managers:
no cookies, correct `Range` support, parallel connections. A folder or a
multi-file selection downloads as a single **zip streamed on the fly** — no
temporary file, no compression, so throughput is bound by disk and network
rather than CPU. The zip switches to Zip64 automatically for large trees.

## Downloading from a URL

Paste a link and the server fetches it at **its own bandwidth** rather than
yours — the point when the file is tens of gigabytes. It lands in the current
folder as a background job.

The fetch is guarded against server-side request forgery. Only `http` and
`https` are allowed, and the real IP being dialed is checked on **every
connection and every redirect**, so a URL that resolves — or redirects — to a
loopback, private, link-local or cloud-metadata address is refused rather than
letting the server reach its own network. A download that needs credentials the
URL does not carry fails with the source's status (for example `403 Forbidden`)
rather than saving a login page.

## Extracting an archive

Extract a `.zip` into a new folder beside it, as a background job. A
booby-trapped archive cannot escape: each entry name is validated exactly like
any other path, so a `../` traversal is refused before it touches the disk, and
symlinks inside the archive are ignored. A decompression bomb is refused by caps
on the entry count, the total uncompressed size, and the per-entry expansion
ratio — the ratio checked against bytes actually written, not the header's claim.

## Checksums

Ask for a file's **SHA-256** from its menu. Hashing runs as a background job and
the result is cached — invalidated when the file's size or modification time
changes — so verifying even a very large file never blocks the server.

## Background jobs

A copy of a folder, an extraction, a URL download and a checksum all run in a
single background worker and report progress you can follow. A **download** shows
its live transfer rate and can be **paused, resumed or cancelled**: pausing keeps
what has arrived, resuming continues from that offset with an HTTP range request
(or restarts cleanly if the source does not support ranges), and cancelling
discards the partial. A job interrupted by a restart resumes on its own.
