// Command zefile is a self-hosted file server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/api"
	"github.com/krishna2206/zefile/internal/audit"
	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/checksum"
	"github.com/krishna2206/zefile/internal/config"
	"github.com/krishna2206/zefile/internal/content"
	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/fetch"
	"github.com/krishna2206/zefile/internal/geoip"
	"github.com/krishna2206/zefile/internal/job"
	"github.com/krishna2206/zefile/internal/settings"
	"github.com/krishna2206/zefile/internal/share"
	"github.com/krishna2206/zefile/internal/storage"
	"github.com/krishna2206/zefile/internal/trash"
	"github.com/krishna2206/zefile/internal/upload"
)

// version is overridden at build time with -ldflags.
var version = "dev"

// shutdownGrace is how long in-flight requests have to finish.
//
// Generous on purpose: a download is a request, and cutting one at five
// seconds would make every restart look like a broken transfer.
const shutdownGrace = 30 * time.Second

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Printf("zefile %s\n", version)
		return
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// The default command is to serve. backup and restore are maintenance
	// commands run against the same instance's configuration, so they read the
	// database location from the same environment the server does.
	var err error
	switch cmd := flag.Arg(0); cmd {
	case "", "serve":
		err = run()
	case "backup":
		err = runBackup(flag.Arg(1))
	case "restore":
		err = runRestore(flag.Arg(1))
	default:
		usage()
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		slog.Error("zefile: command failed", "error", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `zefile — self-hosted file server

Usage:
  zefile [serve]              start the server (default)
  zefile backup [file]        write a consistent database snapshot
  zefile restore <file>       replace the database with a snapshot (stop the server first)
  zefile -version             print the version

backup and restore read `+config.EnvConfigDir+` from the environment.
`)
}

// runBackup writes a snapshot of the database. With no destination it lands in a
// timestamped file under <config>/backups, which is the common case for a cron.
func runBackup(dest string) error {
	dir := os.Getenv(config.EnvConfigDir)
	if dir == "" {
		return fmt.Errorf("%s is not set", config.EnvConfigDir)
	}
	if dest == "" {
		dest = filepath.Join(dir, "backups", "zefile-"+time.Now().Format("2006-01-02-150405")+".db")
	}

	if err := db.BackupTo(context.Background(), db.DBPath(dir), dest); err != nil {
		return err
	}
	if info, err := os.Stat(dest); err == nil {
		fmt.Printf("backup written to %s (%d KiB)\n", dest, info.Size()/1024)
	} else {
		fmt.Printf("backup written to %s\n", dest)
	}
	return nil
}

// runRestore replaces the database with a snapshot. It validates the snapshot
// and copies the current database aside before overwriting it.
func runRestore(src string) error {
	if src == "" {
		return errors.New("usage: zefile restore <backup-file>")
	}
	dir := os.Getenv(config.EnvConfigDir)
	if dir == "" {
		return fmt.Errorf("%s is not set", config.EnvConfigDir)
	}

	report, err := db.RestoreFrom(context.Background(), dir, os.Getenv(config.EnvDataDir), src)
	if err != nil {
		return err
	}
	if report.PreviousSaved != "" {
		fmt.Printf("previous database saved to %s\n", report.PreviousSaved)
	}
	fmt.Printf("restored %s from %s — restart zefile to use it\n", db.DBPath(dir), src)

	if len(report.Diverged) > 0 {
		const show = 20
		fmt.Printf("\nwarning: %d path(s) referenced by the restored database no longer exist on disk:\n", len(report.Diverged))
		for i, p := range report.Diverged {
			if i == show {
				fmt.Printf("  … and %d more\n", len(report.Diverged)-show)
				break
			}
			fmt.Printf("  %s\n", p)
		}
	}
	return nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// The database is opened with the storage root so it can refuse to start
	// when the configuration directory sits inside the browsable tree.
	database, err := db.Open(context.Background(), db.Config{
		Dir:         cfg.ConfigDir,
		StorageRoot: cfg.DataDir,
	})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	engine := acl.New(database)
	fs, err := storage.Open(storage.Config{
		Root:     cfg.DataDir,
		Guard:    engine,
		Reserve:  cfg.Reserve,
		ReadOnly: cfg.ReadOnly,
	})
	if err != nil {
		return err
	}
	defer func() { _ = fs.Close() }()

	signer, err := content.NewSigner()
	if err != nil {
		return err
	}
	authService := auth.New(database)
	if err := announceSetup(context.Background(), authService, cfg); err != nil {
		return err
	}
	warnAboutSingleOrigin(cfg)

	uploads := upload.New(database, fs)
	trashService := trash.New(database, fs)
	auditLog := audit.New(database)
	checksums := checksum.New(database, fs)
	settingsSvc := settings.New(database)

	// Offline IP-to-place lookup for the sessions screen. The published image
	// ships a database at the second path; a from-source run without one falls
	// back to showing no location. No path is a setting — both are conventions.
	locator := geoip.Open(
		filepath.Join(cfg.ConfigDir, "geoip.mmdb"),
		"/usr/local/share/zefile/geoip.mmdb",
	)
	defer func() { _ = locator.Close() }()
	if locator.Enabled() {
		slog.Info("geoip database loaded; sessions will show a location")
	} else {
		slog.Info("no geoip database found; sessions will show no location")
	}

	shareService := share.New(database, fs, share.GuardFunc(func(ctx context.Context, p storage.Path) (bool, error) {
		return engine.Allows(ctx, acl.PermShare, p)
	}))

	// The background worker runs until shutdown. It is started here rather than
	// inside serve so that a job in flight is cancelled when the process is
	// asked to stop, and requeued on the next start.
	jobs := job.New(database)
	jobs.Register(job.TypeCopy, copyJobHandler(fs, engine))
	jobs.Register(job.TypeChecksum, checksumJobHandler(checksums, engine))
	jobs.Register(job.TypeExtract, extractJobHandler(fs, engine))
	jobs.Register(job.TypeFetch, fetchJobHandler(fs, engine, fetch.New(fetch.DefaultPolicy())))
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go jobs.Run(workerCtx)

	startRetention(workerCtx, auditLog, trashService, settingsSvc)

	appHandler := api.New(api.Options{
		FS:            fs,
		Uploads:       uploads,
		Trash:         trashService,
		Shares:        shareService,
		Jobs:          jobs,
		Checksums:     checksums,
		GeoIP:         locator,
		Audit:         auditLog,
		Settings:      settingsSvc,
		Auth:          authService,
		ACL:           engine,
		Signer:        signer,
		ContentBase:   contentBase(cfg),
		Version:       version,
		SingleOrigin:  cfg.SingleOrigin(),
		SecureCookies: cfg.SecureCookies(),
	}).Handler()

	contentHandler := content.New(content.Options{
		FS:           fs,
		Signer:       signer,
		Subject:      acl.NewSubjectLoader(database, engine),
		Shares:       shareService,
		SingleOrigin: cfg.SingleOrigin(),
	}).Handler()

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           route(cfg, appHandler, contentHandler),
		ReadHeaderTimeout: 15 * time.Second,
		// No write timeout: a download of tens of gigabytes is a single
		// response, and a deadline here would sever it mid-transfer.
		IdleTimeout: 2 * time.Minute,
	}

	return serve(server, cfg)
}

// copyJobHandler runs a background copy: it recreates the caller's authority
// from the payload, copies the tree, and records ownership of the result. It is
// safe to retry — CopyTree refuses to overwrite an existing destination, so a
// job that already finished before a crash simply fails the second time and is
// marked failed rather than duplicating anything.
func copyJobHandler(fs *storage.Local, engine *acl.Engine) job.Handler {
	return func(ctx context.Context, payload string, report func(done, total int64)) error {
		var p job.CopyPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return fmt.Errorf("copy job: decode payload: %w", err)
		}

		subject, err := engine.LoadSubject(ctx, p.UserID, p.IsAdmin)
		if err != nil {
			return fmt.Errorf("copy job: load subject: %w", err)
		}
		ctx = acl.NewContext(ctx, subject)

		from, err := storage.ParsePath(p.From)
		if err != nil {
			return err
		}
		to, err := storage.ParsePath(p.To)
		if err != nil {
			return err
		}
		if err := fs.CopyTree(ctx, from, to, report); err != nil {
			return err
		}
		return engine.SetOwner(ctx, to, p.UserID)
	}
}

// extractJobHandler runs a background extraction: it recreates the caller's
// authority from the payload, unpacks the archive into a new directory, and
// records ownership of the result. It is safe to retry — ExtractZip refuses to
// overwrite an existing destination, so a job that already finished before a
// crash simply fails the second time rather than doubling anything.
func extractJobHandler(fs *storage.Local, engine *acl.Engine) job.Handler {
	return func(ctx context.Context, payload string, report func(done, total int64)) error {
		var p job.ExtractPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return fmt.Errorf("extract job: decode payload: %w", err)
		}

		subject, err := engine.LoadSubject(ctx, p.UserID, p.IsAdmin)
		if err != nil {
			return fmt.Errorf("extract job: load subject: %w", err)
		}
		ctx = acl.NewContext(ctx, subject)

		archive, err := storage.ParsePath(p.Archive)
		if err != nil {
			return err
		}
		dest, err := storage.ParsePath(p.Dest)
		if err != nil {
			return err
		}
		target, err := fs.ExtractZip(ctx, archive, dest, report)
		if err != nil {
			return err
		}
		return engine.SetOwner(ctx, target, p.UserID)
	}
}

// fetchJobHandler runs a background download: it recreates the caller's
// authority, streams the URL through the SSRF-guarded fetcher into a staged
// file, and commits it under the caller's ownership. The download is bounded by
// free space — the stage sits under the storage root, so a stream that would
// fill the disk fails as a partial file rather than after publication.
//
// It is safe to retry: a completed download whose destination now exists fails
// the commit the second time and is marked failed rather than duplicated.
// The download can be paused and resumed. Its staged file is named after the
// job, so a resumed run finds the bytes already fetched and continues from that
// offset with a ranged request; if the source does not honour the range, the
// partial is discarded and the download restarts cleanly. Pause keeps the
// stage, cancel discards it, and a crash mid-download resumes on the next start.
func fetchJobHandler(fs *storage.Local, engine *acl.Engine, fetcher *fetch.Fetcher) job.Handler {
	return func(ctx context.Context, payload string, report func(done, total int64)) error {
		var p job.FetchPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return fmt.Errorf("fetch job: decode payload: %w", err)
		}

		subject, err := engine.LoadSubject(ctx, p.UserID, p.IsAdmin)
		if err != nil {
			return fmt.Errorf("fetch job: load subject: %w", err)
		}
		ctx = acl.NewContext(ctx, subject)

		dir, err := storage.ParsePath(p.Dir)
		if err != nil {
			return err
		}

		// A stage named after the job, so pause/resume and crash recovery all
		// address the same partial file across runs.
		jobID, ok := job.IDFromContext(ctx)
		if !ok {
			return errors.New("fetch job: missing job id")
		}
		stage := storage.StageID(fmt.Sprintf("fetch%d", jobID))
		offset, err := fs.EnsureStage(ctx, stage)
		if err != nil {
			return err
		}

		res, err := fetcher.Get(ctx, p.URL, offset)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		// The source ignored our range and is sending the whole file: throw away
		// the partial and start over from zero rather than corrupt it.
		if offset > 0 && !res.Resumed {
			if err := fs.DiscardStage(ctx, stage); err != nil {
				return err
			}
			if offset, err = fs.EnsureStage(ctx, stage); err != nil {
				return err
			}
		}

		name := p.Name
		if name == "" {
			name = res.Filename
		}
		if name == "" {
			name = "download"
		}
		// Child validates the name, so a Content-Disposition or URL segment
		// carrying a separator or traversal is refused before it becomes a path.
		to, err := dir.Child(name)
		if err != nil {
			return err
		}

		// Bound the download by the free space above the reserve, refusing early
		// if the source already declares more than will fit. Without a limit a
		// source could stream without end and fill the disk down to the reserve.
		space, err := fs.Space(ctx)
		if err != nil {
			return err
		}
		// Free space above the reserve, as an int64 the reader can bound on. The
		// subtraction stays in the uint64 domain and is only narrowed once known
		// to fit, so a pathological free-space value cannot wrap into a negative
		// budget that would defeat the cap.
		var budget int64 = math.MaxInt64
		if space.Available <= space.Reserve {
			budget = 0
		} else if avail := space.Available - space.Reserve; avail < math.MaxInt64 {
			budget = int64(avail)
		}
		if res.Size > offset+budget {
			return fmt.Errorf("%w: the source declares %d bytes, more than the free space allows", storage.ErrNoSpace, res.Size)
		}

		reader := &progressReader{r: io.LimitReader(res.Body, budget+1), base: offset, total: res.Size, report: report}
		total, err := fs.AppendToStage(ctx, stage, offset, reader)
		if err != nil {
			// A user cancellation discards the partial; a pause keeps it so a
			// resume can continue. Both arrive as a cancelled context, told apart
			// by the cause the worker set.
			if errors.Is(context.Cause(ctx), job.ErrCancelled) {
				_ = fs.DiscardStage(ctx, stage)
			}
			return err
		}
		if total-offset > budget {
			return fmt.Errorf("%w: the download exceeds the free space allowed", storage.ErrNoSpace)
		}

		if err := fs.CommitStage(ctx, stage, to); err != nil {
			return err
		}
		return engine.SetOwner(ctx, to, p.UserID)
	}
}

// progressReader reports download progress as absolute bytes done — the bytes
// already on disk before this run plus what it has read — out of the total. A
// zero total means unknown, which the interface shows as an indeterminate state.
type progressReader struct {
	r      io.Reader
	base   int64 // bytes already staged before this run (a resume offset)
	read   int64
	total  int64
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.report != nil {
		p.report(p.base+p.read, p.total)
	}
	return n, err
}

// startRetention runs the purges — old audit entries and expired trash — a few
// times a day, reading the current policy from settings each time so a change
// made in the interface takes effect within hours without a restart. A zero
// policy keeps everything, so an unconfigured instance purges nothing.
func startRetention(ctx context.Context, auditLog *audit.Service, trashService *trash.Service, settingsSvc *settings.Service) {
	purge := func() {
		policy, err := settingsSvc.Retention(ctx)
		if err != nil {
			slog.WarnContext(ctx, "retention: could not read policy", "error", err)
			return
		}
		now := time.Now()
		if policy.AuditDays > 0 {
			cutoff := now.AddDate(0, 0, -policy.AuditDays)
			if n, err := auditLog.PurgeBefore(ctx, cutoff); err != nil {
				slog.WarnContext(ctx, "audit retention purge failed", "error", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "purged old audit entries", "count", n, "older_than_days", policy.AuditDays)
			}
		}
		if policy.TrashDays > 0 {
			cutoff := now.AddDate(0, 0, -policy.TrashDays)
			if n, err := trashService.PurgeExpired(ctx, cutoff); err != nil {
				slog.WarnContext(ctx, "trash retention purge failed", "error", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "purged expired trash", "count", n, "older_than_days", policy.TrashDays)
			}
		}
	}

	go func() {
		purge()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				purge()
			}
		}
	}()
}

// checksumJobHandler computes a file's SHA-256 in the background. Like the copy
// handler it rebuilds the caller's authority from the payload, so hashing is
// subject to the same read permission as any other access to the file.
func checksumJobHandler(checksums *checksum.Service, engine *acl.Engine) job.Handler {
	return func(ctx context.Context, payload string, _ func(done, total int64)) error {
		var p checksum.Payload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return fmt.Errorf("checksum job: decode payload: %w", err)
		}

		subject, err := engine.LoadSubject(ctx, p.UserID, p.IsAdmin)
		if err != nil {
			return fmt.Errorf("checksum job: load subject: %w", err)
		}
		ctx = acl.NewContext(ctx, subject)

		path, err := storage.ParsePath(p.Path)
		if err != nil {
			return err
		}
		_, err = checksums.Compute(ctx, path)
		return err
	}
}

// announceSetup prints the first-run link when no account exists yet.
//
// It goes to the log rather than to a default account: shipping known
// credentials means every instance is compromised until someone remembers to
// change them.
func announceSetup(ctx context.Context, service *auth.Service, cfg config.Config) error {
	needed, err := service.NeedsSetup(ctx)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}

	token, err := service.IssueSetupToken(ctx)
	if err != nil {
		return err
	}

	slog.Warn("this instance has no account yet — open the link below to create the administrator",
		"url", fmt.Sprintf("%s/setup?token=%s", cfg.AppURL, token),
		"expires_in", auth.DefaultSetupTTL.String(),
		"note", "this link is replaced every time zefile starts")
	return nil
}

func warnAboutSingleOrigin(cfg config.Config) {
	if !cfg.SingleOrigin() {
		return
	}
	slog.Warn("running with a single origin: user content is served from the application origin",
		"consequence", "inline preview is restricted and every file is sent as an attachment",
		"fix", "set "+config.EnvContentURL+" to a second hostname")
}

// route dispatches on the Host header, so one listener and one port serve both
// origins. Separating them costs a DNS record, not a second process.
//
// In single-origin mode there is no second host to compare against, and the
// content routes simply live alongside the API on the same one.
func route(cfg config.Config, app, files http.Handler) http.Handler {
	if cfg.SingleOrigin() {
		mux := http.NewServeMux()
		mux.Handle("/d/", files)
		mux.Handle("/z/", files)
		mux.Handle("/s/", files)
		mux.Handle("/", app)
		return mux
	}

	contentHost := cfg.ContentURL.Host
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.Host carries the port when the client sent one, so the comparison
		// is against the configured authority rather than the hostname alone.
		if strings.EqualFold(r.Host, contentHost) {
			files.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}

// contentBase is the public prefix links are built from.
func contentBase(cfg config.Config) string {
	if cfg.SingleOrigin() {
		return cfg.AppURL.String()
	}
	return cfg.ContentURL.String()
}

func serve(server *http.Server, cfg config.Config) error {
	// Signals are caught before the listener opens, so an interrupt arriving
	// during startup is not lost.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	failed := make(chan error, 1)
	go func() {
		slog.Info("zefile is listening",
			"address", cfg.Listen, "app_url", cfg.AppURL.String(),
			"data_dir", cfg.DataDir, "version", version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down", "grace", shutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
