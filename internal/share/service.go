// Package share implements public share links.
//
// A share is a capability: an opaque token, stored only as a hash, that lets
// anyone holding it download one file without an account. It is served from the
// content origin — cookieless and isolated — so a share can be handed to a
// download manager without exposing the session, and it can carry an expiry, a
// download limit, and be revoked at any time.
package share

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
	"github.com/krishna2206/zefile/internal/storage"
)

// readPerm is acl.PermRead. It is written as a literal rather than imported
// because a share only stores the value; it never interprets it here.
const readPerm = 1

// Reasons a share cannot be served. They are distinct so the public endpoint
// can tell a holder why a link they had stopped working.
var (
	ErrNotFound  = errors.New("share: no such link")
	ErrExpired   = errors.New("share: link has expired")
	ErrRevoked   = errors.New("share: link was revoked")
	ErrExhausted = errors.New("share: download limit reached")
	ErrNotFile   = errors.New("share: only files can be shared")
)

// Share is a link as its owner sees it.
type Share struct {
	ID            int64
	Path          storage.Path
	OwnerID       int64
	MaxDownloads  int64 // 0 means unlimited
	DownloadCount int64
	CreatedAt     time.Time
	ExpiresAt     time.Time // zero means never
	RevokedAt     time.Time // zero means active
}

// CreateOptions are the limits set when a link is made.
type CreateOptions struct {
	ExpiresAt    time.Time // zero means never
	MaxDownloads int64     // 0 means unlimited
}

// Grant is what the content origin needs to serve a shared file.
type Grant struct {
	ID      int64
	Path    storage.Path
	OwnerID int64
}

// Resolver is the content origin's view: turn a token into a file to serve, and
// record that it was downloaded. It is an interface so the content package
// depends on the capability, not on this whole service.
type Resolver interface {
	Resolve(ctx context.Context, token string) (Grant, error)
	RecordDownload(ctx context.Context, shareID int64, ip, userAgent string) error
}

// Service manages shares.
type Service struct {
	fs     storage.FS
	reads  *sqlcgen.Queries
	writes *sqlcgen.Queries
	now    func() time.Time
}

var _ Resolver = (*Service)(nil)

// New builds a Service over the given database and storage.
func New(database *db.DB, fs storage.FS) *Service {
	return &Service{
		fs:     fs,
		reads:  sqlcgen.New(database.Read),
		writes: sqlcgen.New(database.Write),
		now:    time.Now,
	}
}

// Create makes a link to a file and returns the token once — it is never stored
// or shown again. Folders are refused: browsing a shared tree is a separate,
// larger feature.
func (s *Service) Create(ctx context.Context, ownerID int64, p storage.Path, opts CreateOptions) (string, Share, error) {
	info, err := s.fs.Stat(ctx, p) // authorises read for the caller
	if err != nil {
		return "", Share{}, err
	}
	if info.IsDir {
		return "", Share{}, ErrNotFile
	}

	token, hash, err := auth.NewToken(auth.SharePrefix)
	if err != nil {
		return "", Share{}, err
	}

	var expires sql.NullInt64
	if !opts.ExpiresAt.IsZero() {
		expires = sql.NullInt64{Int64: opts.ExpiresAt.Unix(), Valid: true}
	}
	var maxDownloads sql.NullInt64
	if opts.MaxDownloads > 0 {
		maxDownloads = sql.NullInt64{Int64: opts.MaxDownloads, Valid: true}
	}

	row, err := s.writes.CreateShare(ctx, sqlcgen.CreateShareParams{
		TokenHash:    hash,
		OwnerID:      ownerID,
		Path:         p.String(),
		Perms:        readPerm,
		PasswordHash: sql.NullString{},
		MaxDownloads: maxDownloads,
		CreatedAt:    s.now().Unix(),
		ExpiresAt:    expires,
	})
	if err != nil {
		return "", Share{}, err
	}
	return token, toShare(row), nil
}

// List returns the owner's active links, newest first.
func (s *Service) List(ctx context.Context, ownerID int64) ([]Share, error) {
	rows, err := s.reads.ListSharesByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]Share, 0, len(rows))
	for _, r := range rows {
		out = append(out, toShare(r))
	}
	return out, nil
}

// Revoke marks a link dead. It is scoped to the owner, so one account cannot
// revoke another's link.
func (s *Service) Revoke(ctx context.Context, ownerID, id int64) error {
	n, err := s.writes.RevokeShare(ctx, sqlcgen.RevokeShareParams{
		RevokedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		ID:        id,
		OwnerID:   ownerID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Resolve turns a token into a grant, or reports why it will not serve.
func (s *Service) Resolve(ctx context.Context, token string) (Grant, error) {
	row, err := s.reads.GetShareByHash(ctx, auth.HashToken(token))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Grant{}, ErrNotFound
		}
		return Grant{}, err
	}
	if row.RevokedAt.Valid {
		return Grant{}, ErrRevoked
	}
	if row.ExpiresAt.Valid && s.now().Unix() >= row.ExpiresAt.Int64 {
		return Grant{}, ErrExpired
	}
	if row.MaxDownloads.Valid && row.DownloadCount >= row.MaxDownloads.Int64 {
		return Grant{}, ErrExhausted
	}
	p, err := storage.ParsePath(row.Path)
	if err != nil {
		return Grant{}, err
	}
	return Grant{ID: row.ID, Path: p, OwnerID: row.OwnerID}, nil
}

// RecordDownload counts a served download and logs the access.
func (s *Service) RecordDownload(ctx context.Context, shareID int64, ip, userAgent string) error {
	if err := s.writes.IncrementShareDownloads(ctx, shareID); err != nil {
		return err
	}
	return s.writes.LogShareAccess(ctx, sqlcgen.LogShareAccessParams{
		ShareID:   shareID,
		At:        s.now().Unix(),
		Ip:        ip,
		UserAgent: userAgent,
		BytesSent: 0,
	})
}

func toShare(r sqlcgen.Share) Share {
	sh := Share{
		ID:            r.ID,
		OwnerID:       r.OwnerID,
		DownloadCount: r.DownloadCount,
		CreatedAt:     time.Unix(r.CreatedAt, 0),
	}
	if p, err := storage.ParsePath(r.Path); err == nil {
		sh.Path = p
	}
	if r.MaxDownloads.Valid {
		sh.MaxDownloads = r.MaxDownloads.Int64
	}
	if r.ExpiresAt.Valid {
		sh.ExpiresAt = time.Unix(r.ExpiresAt.Int64, 0)
	}
	if r.RevokedAt.Valid {
		sh.RevokedAt = time.Unix(r.RevokedAt.Int64, 0)
	}
	return sh
}
