-- Cached file checksums. Computing a SHA-256 over a large file is read-heavy, so
-- it runs as a background job and the result is cached here, keyed by path and
-- invalidated when the file's size or modification time no longer match — the
-- filesystem stays the authority, a stale row is simply recomputed.

-- +goose Up
CREATE TABLE checksums (
    path        TEXT    PRIMARY KEY,
    algo        TEXT    NOT NULL,
    hash        TEXT    NOT NULL,
    size        INTEGER NOT NULL,
    modified_at INTEGER NOT NULL,
    computed_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE checksums;
