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

    # Only the configuration directory is adjusted. The data directory is the
    # user's own tree, possibly holding terabytes; walking it to change
    # ownership on every start would be both slow and presumptuous.
    chown "$PUID:$PGID" "${ZEFILE_CONFIG_DIR:-/config}" 2>/dev/null || true

    exec su-exec "$PUID:$PGID" "$@"
fi

# Already unprivileged, because the deployment set `user:` itself. Nothing to
# drop, and nothing to complain about.
exec "$@"
