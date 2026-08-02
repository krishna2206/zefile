// Package audit records who did what.
//
// An audit entry is a fact about the past: an administrator granted access, a
// user signed in, a share was revoked. It is written best-effort and never on
// the hot path of a read — recording an action must never be the reason the
// action fails, so a write error is logged and swallowed rather than returned.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// Actions. They are stable strings the interface groups and translates, so they
// may be added to but not renamed.
const (
	ActionLogin            = "auth.login"
	ActionLogout           = "auth.logout"
	ActionSetup            = "auth.setup"
	ActionPasswordChanged  = "auth.password_changed"
	ActionUserJoined       = "user.joined"
	ActionUserUpdated      = "user.updated"
	ActionUserDeleted      = "user.deleted"
	ActionInvitationCreate = "invitation.created"
	ActionInvitationRevoke = "invitation.revoked"
	ActionShareCreated     = "share.created"
	ActionShareRevoked     = "share.revoked"
	ActionAccessGranted    = "access.granted"
	ActionAccessRevoked    = "access.revoked"
	ActionGroupCreated     = "group.created"
	ActionGroupDeleted     = "group.deleted"
	ActionGroupMemberAdd   = "group.member_added"
	ActionGroupMemberDrop  = "group.member_removed"
	ActionFileTrashed      = "file.trashed"
	ActionTrashRestored    = "trash.restored"
	ActionTrashPurged      = "trash.purged"
	ActionTrashEmptied     = "trash.emptied"
)

// Entry is one recorded action.
type Entry struct {
	ID        int64
	At        time.Time
	ActorID   int64  // 0 when the actor is unknown or the account was deleted
	ActorName string // resolved username, empty when unknown or deleted
	ActorIP   string
	Action    string
	Target    string
	Details   json.RawMessage
}

// Service records and reads audit entries.
type Service struct {
	writes *sqlcgen.Queries
	reads  *sqlcgen.Queries
	now    func() time.Time
	log    *slog.Logger
}

// New builds a Service over an open database.
func New(database *db.DB) *Service {
	return &Service{
		writes: sqlcgen.New(database.Write),
		reads:  sqlcgen.New(database.Read),
		now:    time.Now,
		log:    slog.Default(),
	}
}

// Record writes an entry. It is best-effort: a failure is logged, never
// returned, so auditing can never be the reason an action fails. actorID may be
// zero for an unauthenticated action; details may be nil.
func (s *Service) Record(ctx context.Context, actorID int64, ip, action, target string, details map[string]any) {
	encoded := "{}"
	if len(details) > 0 {
		if b, err := json.Marshal(details); err == nil {
			encoded = string(b)
		}
	}

	var actor sql.NullInt64
	if actorID != 0 {
		actor = sql.NullInt64{Int64: actorID, Valid: true}
	}

	// Detach from request cancellation: the record should survive the client
	// hanging up, since it describes something that already happened.
	writeCtx := context.WithoutCancel(ctx)
	if err := s.writes.InsertAuditEntry(writeCtx, sqlcgen.InsertAuditEntryParams{
		At:      s.now().Unix(),
		ActorID: actor,
		ActorIp: ip,
		Action:  action,
		Target:  target,
		Details: encoded,
	}); err != nil {
		s.log.WarnContext(ctx, "audit: could not record", "action", action, "error", err)
	}
}

// List returns entries newest first, older than beforeID. Pass a large beforeID
// for the first page and the last returned id for the next.
func (s *Service) List(ctx context.Context, beforeID int64, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.reads.ListAuditEntries(ctx, sqlcgen.ListAuditEntriesParams{
		ID:    beforeID,
		Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{
			ID:      r.ID,
			At:      time.Unix(r.At, 0).UTC(),
			ActorIP: r.ActorIp,
			Action:  r.Action,
			Target:  r.Target,
			Details: json.RawMessage(r.Details),
		}
		if r.ActorID.Valid {
			e.ActorID = r.ActorID.Int64
		}
		if r.ActorUsername.Valid {
			e.ActorName = r.ActorUsername.String
		}
		out = append(out, e)
	}
	return out, nil
}
