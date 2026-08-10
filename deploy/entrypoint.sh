#!/bin/sh
set -eu

# Which user Zefile runs as is not something you configure: it adopts whoever
# owns the mounted data directory. A bind-mounted directory keeps its host
# ownership, so a container running as a fixed user writes into it only by luck —
# getting that wrong surfaces as an unreadable permission error on the first
# upload, the single most common way a self-hosted deployment fails. Adopting the
# directory's own owner makes it work with no ids to line up by hand.
#
# The one thing Zefile will not do is run as root. If the data directory is owned
# by root — some images populate it that way — chown it to any non-root user and
# Zefile will follow.

DATA="${ZEFILE_DATA_DIR:-/data}"
CONFIG="${ZEFILE_CONFIG_DIR:-/config}"

# A deployment that pinned `user:` itself is already unprivileged: nothing to
# adopt, nothing to drop.
if [ "$(id -u)" != "0" ]; then
    exec "$@"
fi

# The uid:gid to run as is the data directory's owner, unless that is root.
uid="$(stat -c %u "$DATA" 2>/dev/null || echo 0)"
gid="$(stat -c %g "$DATA" 2>/dev/null || echo 0)"

if [ "$uid" = "0" ]; then
    # A missing bind-mount target is created by Docker owned by root. That empty
    # case is normal on a fresh deployment: claim it for the image's built-in
    # unprivileged user so the first write succeeds. A root-owned directory that
    # already holds files is not ours to reown — warn and fall back, leaving the
    # fix (a one-time chown) to the operator.
    if [ -d "$DATA" ] && [ -z "$(ls -A "$DATA" 2>/dev/null)" ]; then
        uid=1000
        gid=1000
        chown "$uid:$gid" "$DATA" 2>/dev/null || true
    else
        echo "zefile: $DATA is owned by root, and Zefile will not run as root." >&2
        echo "zefile: chown it to any non-root user — e.g. 'chown -R 1000:1000 <host directory>' — and restart." >&2
        uid=1000
        gid=1000
    fi
fi

# Align the built-in zefile account to the chosen ids, so a listing on the host
# reads as that user rather than a bare number.
if [ "$(id -g zefile 2>/dev/null)" != "$gid" ]; then
    delgroup zefile 2>/dev/null || true
    addgroup -g "$gid" zefile 2>/dev/null || true
fi
if [ "$(id -u zefile 2>/dev/null)" != "$uid" ]; then
    deluser zefile 2>/dev/null || true
    adduser -D -u "$uid" -G zefile zefile 2>/dev/null || true
fi

chown "$uid:$gid" "$CONFIG" 2>/dev/null || true

exec su-exec "$uid:$gid" "$@"
