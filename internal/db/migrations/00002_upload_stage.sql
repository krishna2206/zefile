-- The staged file backing an upload needs its own column.
--
-- It was briefly packed into `checksum` alongside the client's declared digest.
-- Two meanings in one column is the kind of shortcut that survives until
-- someone writes a query against it and gets nonsense, so it is undone here
-- while the project has no release to be compatible with.

-- +goose Up
ALTER TABLE uploads ADD COLUMN stage_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE uploads DROP COLUMN stage_id;
