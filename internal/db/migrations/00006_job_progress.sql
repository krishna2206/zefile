-- Byte-level progress and a pause flag for background jobs.
--
-- bytes_done/bytes_total let the interface show a transfer rate rather than a
-- bare percentage: the client divides the change in bytes by the time between
-- polls. paused is a flag rather than a new status value so the status CHECK
-- constraint need not be rebuilt: a paused job keeps status 'pending' but the
-- worker skips it until the flag is cleared, at which point it is resumed.

-- +goose Up
ALTER TABLE jobs ADD COLUMN bytes_done  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN bytes_total INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN paused      INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE jobs DROP COLUMN bytes_done;
ALTER TABLE jobs DROP COLUMN bytes_total;
ALTER TABLE jobs DROP COLUMN paused;
