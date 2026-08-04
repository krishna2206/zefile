// Package trash implements reversible deletion.
//
// Deleting moves an entry into a reserved directory and records where it came
// from; restoring puts it back; purging removes it for good. The filesystem
// stays the source of truth — the table holds only what the filesystem cannot
// say, which is where a trashed entry used to live.
package trash

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"time"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
	"github.com/krishna2206/zefile/internal/storage"
)

// ErrNotFound means the trashed item is unknown or already gone.
var ErrNotFound = errors.New("trash: no such item")

// Item is a trashed entry as presented to a caller.
type Item struct {
	ID           int64
	Name         string
	OriginalPath string
	IsDir        bool
	Size         int64
	DeletedAt    time.Time
}

// Service manages the trash.
type Service struct {
	fs     *storage.Local
	reads  *sqlcgen.Queries
	writes *sqlcgen.Queries
	now    func() time.Time
}

// New builds a Service over the given database and storage.
func New(database *db.DB, fs *storage.Local) *Service {
	return &Service{
		fs:     fs,
		reads:  sqlcgen.New(database.Read),
		writes: sqlcgen.New(database.Write),
		now:    time.Now,
	}
}

// Trash moves an entry into the trash and records how to restore it.
func (s *Service) Trash(ctx context.Context, userID int64, p storage.Path) error {
	id, err := s.fs.Trash(ctx, p)
	if err != nil {
		return err
	}

	isDir := false
	if info, err := s.fs.TrashStat(ctx, id); err == nil {
		isDir = info.IsDir()
	}

	_, err = s.writes.CreateTrash(ctx, sqlcgen.CreateTrashParams{
		TrashName:    string(id),
		OriginalPath: p.String(),
		DeletedBy:    sql.NullInt64{Int64: userID, Valid: userID != 0},
		DeletedAt:    s.now().Unix(),
		IsDir:        boolToInt(isDir),
	})
	if err != nil {
		// The move happened but the record did not. Put the entry back so it is
		// not stranded in the trash directory with nothing to restore it.
		_ = s.fs.Restore(ctx, id, p)
		return err
	}
	return nil
}

// List returns the trashed items, newest first, sized from disk.
func (s *Service) List(ctx context.Context) ([]Item, error) {
	rows, err := s.reads.ListTrash(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		var size int64
		if info, err := s.fs.TrashStat(ctx, storage.TrashID(r.TrashName)); err == nil {
			size = info.Size()
		}
		items = append(items, Item{
			ID:           r.ID,
			Name:         path.Base(r.OriginalPath),
			OriginalPath: r.OriginalPath,
			IsDir:        r.IsDir != 0,
			Size:         size,
			DeletedAt:    time.Unix(r.DeletedAt, 0),
		})
	}
	return items, nil
}

// Restore returns a trashed item to its original location and reports where it
// landed, so a caller can restore any bookkeeping keyed on the path.
func (s *Service) Restore(ctx context.Context, id int64) (storage.Path, error) {
	row, err := s.reads.GetTrash(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.Path{}, ErrNotFound
		}
		return storage.Path{}, err
	}
	dest, err := storage.ParsePath(row.OriginalPath)
	if err != nil {
		return storage.Path{}, err
	}
	if err := s.fs.Restore(ctx, storage.TrashID(row.TrashName), dest); err != nil {
		return storage.Path{}, err
	}
	if err := s.writes.DeleteTrash(ctx, id); err != nil {
		return dest, err
	}
	return dest, nil
}

// Purge removes one trashed item for good.
func (s *Service) Purge(ctx context.Context, id int64) error {
	row, err := s.reads.GetTrash(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := s.fs.PurgeTrash(ctx, storage.TrashID(row.TrashName)); err != nil {
		return err
	}
	return s.writes.DeleteTrash(ctx, id)
}

// PurgeExpired permanently removes items deleted before the cutoff, returning
// how many went. It backs the configurable trash retention.
func (s *Service) PurgeExpired(ctx context.Context, before time.Time) (int, error) {
	items, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, it := range items {
		if it.DeletedAt.Before(before) {
			if err := s.Purge(ctx, it.ID); err != nil {
				return purged, err
			}
			purged++
		}
	}
	return purged, nil
}

// Empty purges every trashed item. It stops at the first failure rather than
// pressing on, so a disk error surfaces instead of being buried under later
// successes.
func (s *Service) Empty(ctx context.Context) error {
	rows, err := s.reads.ListTrash(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.fs.PurgeTrash(ctx, storage.TrashID(r.TrashName)); err != nil {
			return err
		}
		if err := s.writes.DeleteTrash(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
