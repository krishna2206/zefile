-- Recovery codes let a user reset a forgotten password without email, which the
-- server deliberately never sends. Each code is single-use. The hash is stored
-- with Argon2id, like a password, because a code is short enough to be typed and
-- therefore low-entropy enough to be worth hashing slowly.

-- +goose Up
CREATE TABLE recovery_codes (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    used_at    INTEGER
) STRICT;

CREATE INDEX recovery_codes_user_idx ON recovery_codes (user_id);

-- +goose Down
DROP TABLE recovery_codes;
