#!/bin/sh
set -eu

# Adopt the user id that owns the mounted data, rather than demanding the host
# adopt ours.
#
# A bind-mounted directory keeps its host ownership, so a container running as
# a fixed user writes into it only by luck. Getting this wrong surfaces as an
# unreadable permission error on the first upload, which is the single most
# common way a self-hosted deployment fails.
PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

if [ "$(id -u)" = "0" ]; then
    if [ "$(id -g zefile)" != "$PGID" ]; then
        delgroup zefile 2>/dev/null || true
        addgroup -g "$PGID" zefile
    fi
    if [ "$(id -u zefile)" != "$PUID" ]; then
        deluser zefile 2>/dev/null || true
        adduser -D -u "$PUID" -G zefile zefile
    fi

    chown "$PUID:$PGID" "${ZEFILE_CONFIG_DIR:-/config}" 2>/dev/null || true

    # The data directory is only adopted while it is empty, which is exactly the
    # case Docker creates by making a missing bind-mount target owned by root —
    # after which nothing can write to it and the first upload fails for a
    # reason nobody would guess.
    #
    # An existing tree is left alone. It is the user's own, possibly holding
    # terabytes, and walking it on every start would be both slow and
    # presumptuous about files this service did not put there.
    DATA="${ZEFILE_DATA_DIR:-/data}"
    if [ -d "$DATA" ] && [ -z "$(ls -A "$DATA" 2>/dev/null)" ]; then
        chown "$PUID:$PGID" "$DATA" 2>/dev/null || true
    elif ! su-exec "$PUID:$PGID" test -w "$DATA" 2>/dev/null; then
        echo "zefile: $DATA is not writable by ${PUID}:${PGID}." >&2
        echo "zefile: run 'chown -R ${PUID}:${PGID} <host directory>' or set PUID/PGID to its owner." >&2
    fi

    exec su-exec "$PUID:$PGID" "$@"
fi

# Already unprivileged, because the deployment set `user:` itself. Nothing to
# drop, and nothing to complain about.
exec "$@"
