// Package settings stores instance settings that are configured at runtime,
// through the admin interface, rather than at deploy time through the
// environment. It is a thin typed layer over a key-value table.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

const (
	keyAuditRetentionDays = "audit_retention_days"
	keyTrashRetentionDays = "trash_retention_days"
)

// ErrInvalid means a setting was given an unacceptable value.
var ErrInvalid = errors.New("settings: invalid value")

// Retention is how long the audit log and the trash are kept. Zero means keep
// indefinitely.
type Retention struct {
	AuditDays int `json:"audit_days"`
	TrashDays int `json:"trash_days"`
}

// Service reads and writes instance settings.
type Service struct {
	reads  *sqlcgen.Queries
	writes *sqlcgen.Queries
	now    func() time.Time
}

// New builds a Service over the database.
func New(database *db.DB) *Service {
	return &Service{
		reads:  sqlcgen.New(database.Read),
		writes: sqlcgen.New(database.Write),
		now:    time.Now,
	}
}

// Retention returns the current retention policy, defaulting to zero (keep
// everything) for any value never set.
func (s *Service) Retention(ctx context.Context) (Retention, error) {
	audit, err := s.getInt(ctx, keyAuditRetentionDays)
	if err != nil {
		return Retention{}, err
	}
	trash, err := s.getInt(ctx, keyTrashRetentionDays)
	if err != nil {
		return Retention{}, err
	}
	return Retention{AuditDays: audit, TrashDays: trash}, nil
}

// SetRetention stores the retention policy. Negative values are refused.
func (s *Service) SetRetention(ctx context.Context, r Retention) error {
	if r.AuditDays < 0 || r.TrashDays < 0 {
		return ErrInvalid
	}
	if err := s.setInt(ctx, keyAuditRetentionDays, r.AuditDays); err != nil {
		return err
	}
	return s.setInt(ctx, keyTrashRetentionDays, r.TrashDays)
}

func (s *Service) getInt(ctx context.Context, key string) (int, error) {
	raw, err := s.reads.GetSetting(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("settings: read %q: %w", key, err)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, nil // a malformed stored value reads as the default
	}
	return n, nil
}

func (s *Service) setInt(ctx context.Context, key string, value int) error {
	if err := s.writes.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{
		Key:       key,
		Value:     strconv.Itoa(value),
		UpdatedAt: s.now().Unix(),
	}); err != nil {
		return fmt.Errorf("settings: write %q: %w", key, err)
	}
	return nil
}
