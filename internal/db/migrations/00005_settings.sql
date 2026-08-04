-- Instance settings configured at runtime through the admin interface, rather
-- than at deploy time through the environment. A key-value table keeps it open
-- to new settings without a migration each time.

-- +goose Up
CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE settings;
