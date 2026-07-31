// Command zefile is a self-hosted file server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/api"
	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/config"
	"github.com/krishna2206/zefile/internal/content"
	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/job"
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
	flag.Parse()

	if *showVersion {
		fmt.Printf("zefile %s\n", version)
		return
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := run(); err != nil {
		slog.Error("zefile failed to start", "error", err)
		os.Exit(1)
	}
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
	shareService := share.New(database, fs, share.GuardFunc(func(ctx context.Context, p storage.Path) (bool, error) {
		return engine.Allows(ctx, acl.PermShare, p)
	}))

	// The background worker runs until shutdown. It is started here rather than
	// inside serve so that a job in flight is cancelled when the process is
	// asked to stop, and requeued on the next start.
	jobs := job.New(database)
	jobs.Register(job.TypeCopy, copyJobHandler(fs, engine))
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go jobs.Run(workerCtx)

	appHandler := api.New(api.Options{
		FS:            fs,
		Uploads:       uploads,
		Trash:         trashService,
		Shares:        shareService,
		Jobs:          jobs,
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
	return func(ctx context.Context, payload string, report func(float64)) error {
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
