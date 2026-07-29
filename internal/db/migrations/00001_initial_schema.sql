-- Initial schema.
--
-- The whole model from the design document is created in one migration, even
-- though several tables stay unused until their phase. Splitting it would mean
-- a stream of migrations contradicting each other while the design is still
-- being implemented, and would leave sqlc generating against a partial schema.
--
-- Conventions, applied without exception:
--   * timestamps are INTEGER Unix seconds, UTC
--   * every secret is stored hashed, never in clear
--   * paths are NFC key form, exactly as internal/storage produces them
--   * ON DELETE is always explicit, so nothing is left to a default

-- +goose Up

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    email         TEXT    UNIQUE,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    totp_secret   TEXT,
    disabled      INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

-- Sessions are opaque tokens looked up on every request, which is what makes
-- logging out immediate. The row is the session: deleting it ends access.
CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   BLOB    NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    user_agent   TEXT    NOT NULL DEFAULT '',
    ip           TEXT    NOT NULL DEFAULT '',
    revoked_at   INTEGER
) STRICT;

CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);

-- Programmatic access. Scopes are a comma-separated list rather than a bitmask:
-- they are read by humans in the interface far more often than by the resolver,
-- and the set will grow in ways a fixed-width mask would not accommodate.
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    token_hash   BLOB    NOT NULL UNIQUE,
    prefix       TEXT    NOT NULL,
    scopes       TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    expires_at   INTEGER,
    revoked_at   INTEGER
) STRICT;

CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);

-- The inviter is kept for the audit trail, so removing an account must not
-- erase who invited whom: the reference is nulled instead of cascading.
CREATE TABLE invitations (
    id         INTEGER PRIMARY KEY,
    token_hash BLOB    NOT NULL UNIQUE,
    email      TEXT,
    inviter_id INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
) STRICT;

CREATE TABLE groups (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE group_members (
    group_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
) STRICT, WITHOUT ROWID;

-- Access control. Resolution walks a path from the root down, collecting every
-- applicable entry, so the index on path is the hot one.
--
-- subject_id is deliberately not a foreign key: it points at either users or
-- groups depending on subject_type, which SQL cannot express. Cleanup is the
-- application's responsibility and is covered by tests.
CREATE TABLE acl (
    id           INTEGER PRIMARY KEY,
    subject_type TEXT    NOT NULL CHECK (subject_type IN ('user', 'group')),
    subject_id   INTEGER NOT NULL,
    path         TEXT    NOT NULL,
    perms        INTEGER NOT NULL,
    recursive    INTEGER NOT NULL DEFAULT 1 CHECK (recursive IN (0, 1)),
    deny         INTEGER NOT NULL DEFAULT 0 CHECK (deny IN (0, 1)),
    created_at   INTEGER NOT NULL,
    UNIQUE (subject_type, subject_id, path, deny)
) STRICT;

CREATE INDEX acl_path_idx ON acl (path);
CREATE INDEX acl_subject_idx ON acl (subject_type, subject_id);

-- Ownership is sparse: only files uploaded through Zefile have a row. Anything
-- placed by other means simply has no owner and falls back to ACL rules.
--
-- Keyed by path, so an entry left behind by a move performed outside the
-- application is an orphan, collected periodically.
CREATE TABLE file_owners (
    path       TEXT    PRIMARY KEY,
    owner_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX file_owners_owner_idx ON file_owners (owner_id);

-- Share links. The token is hashed like any other secret, so a database leak
-- exposes no working link.
CREATE TABLE shares (
    id             INTEGER PRIMARY KEY,
    token_hash     BLOB    NOT NULL UNIQUE,
    owner_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    path           TEXT    NOT NULL,
    perms          INTEGER NOT NULL,
    password_hash  TEXT,
    max_downloads  INTEGER,
    download_count INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    expires_at     INTEGER,
    revoked_at     INTEGER
) STRICT;

CREATE INDEX shares_owner_idx ON shares (owner_id);
CREATE INDEX shares_path_idx ON shares (path);

CREATE TABLE share_access_log (
    id         INTEGER PRIMARY KEY,
    share_id   INTEGER NOT NULL REFERENCES shares (id) ON DELETE CASCADE,
    at         INTEGER NOT NULL,
    ip         TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    bytes_sent INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX share_access_log_share_idx ON share_access_log (share_id, at);

-- Trash. trash_name is the entry's name inside the hidden trash directory;
-- original_path is where restoring puts it back.
CREATE TABLE trash (
    id            INTEGER PRIMARY KEY,
    trash_name    TEXT    NOT NULL UNIQUE,
    original_path TEXT    NOT NULL,
    deleted_by    INTEGER REFERENCES users (id) ON DELETE SET NULL,
    deleted_at    INTEGER NOT NULL,
    is_dir        INTEGER NOT NULL DEFAULT 0 CHECK (is_dir IN (0, 1))
) STRICT;

CREATE INDEX trash_deleted_at_idx ON trash (deleted_at);

-- In-flight resumable uploads. received is the offset a client gets back when
-- it asks where to resume.
CREATE TABLE uploads (
    id          INTEGER PRIMARY KEY,
    token       TEXT    NOT NULL UNIQUE,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_path TEXT    NOT NULL,
    size        INTEGER NOT NULL,
    received    INTEGER NOT NULL DEFAULT 0,
    checksum    TEXT,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX uploads_expiry_idx ON uploads (expires_at);

-- Background work. Persisted so that a restart during a three-hour transfer
-- does not lose it.
CREATE TABLE jobs (
    id           INTEGER PRIMARY KEY,
    type         TEXT    NOT NULL,
    payload      TEXT    NOT NULL DEFAULT '{}',
    status       TEXT    NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled')),
    progress     REAL    NOT NULL DEFAULT 0,
    error        TEXT,
    created_at   INTEGER NOT NULL,
    started_at   INTEGER,
    finished_at  INTEGER
) STRICT;

CREATE INDEX jobs_status_idx ON jobs (status, created_at);

-- Security-relevant actions. actor_id is nulled rather than cascaded: the whole
-- point of an audit trail is that it outlives the account it describes.
CREATE TABLE audit_log (
    id       INTEGER PRIMARY KEY,
    at       INTEGER NOT NULL,
    actor_id INTEGER REFERENCES users (id) ON DELETE SET NULL,
    actor_ip TEXT    NOT NULL DEFAULT '',
    action   TEXT    NOT NULL,
    target   TEXT    NOT NULL DEFAULT '',
    details  TEXT    NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX audit_log_at_idx ON audit_log (at);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id, at);

-- Search index over names. The filesystem remains the source of truth; this is
-- a cache reconciled in the background, so it may lag and must never be
-- consulted to decide whether a file exists.
-- +goose StatementBegin
CREATE VIRTUAL TABLE file_index USING fts5 (
    path,
    name,
    tokenize = "unicode61 remove_diacritics 2"
);
-- +goose StatementEnd

-- +goose Down

DROP TABLE file_index;
DROP TABLE audit_log;
DROP TABLE jobs;
DROP TABLE uploads;
DROP TABLE trash;
DROP TABLE share_access_log;
DROP TABLE shares;
DROP TABLE file_owners;
DROP TABLE acl;
DROP TABLE group_members;
DROP TABLE groups;
DROP TABLE invitations;
DROP TABLE api_tokens;
DROP TABLE sessions;
DROP TABLE users;
