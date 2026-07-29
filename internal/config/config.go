// Package config reads and validates the settings a Zefile instance runs on.
//
// Everything comes from the environment. A self-hosted service is deployed from
// a compose file far more often than from a configuration file, and one source
// of truth avoids the question of which wins.
//
// Validation is strict and happens at startup: a misconfigured instance should
// refuse to start with a clear message rather than run and fail later in a way
// that looks like a bug.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/krishna2206/zefile/internal/storage"
)

// Environment variables read by [Load].
const (
	EnvAppURL     = "ZEFILE_APP_URL"
	EnvContentURL = "ZEFILE_CONTENT_URL"
	EnvDataDir    = "ZEFILE_DATA_DIR"
	EnvConfigDir  = "ZEFILE_CONFIG_DIR"
	EnvListen     = "ZEFILE_LISTEN"
	EnvReserve    = "ZEFILE_RESERVE_BYTES"
	EnvReadOnly   = "ZEFILE_READ_ONLY"
)

// DefaultListen is the address served when none is given.
const DefaultListen = ":8080"

// Config is a validated instance configuration.
type Config struct {
	// AppURL is the public address of the application origin, used to build
	// links and to decide whether cookies may be marked Secure.
	AppURL *url.URL

	// ContentURL is the public address of the content origin. When empty the
	// instance runs in single-origin mode, described in the design document as
	// a deliberate, hardened degradation rather than an accident.
	ContentURL *url.URL

	// DataDir is the storage root: the directory users browse.
	DataDir string

	// ConfigDir holds the database. It must not be inside DataDir, which
	// [db.Open] enforces.
	ConfigDir string

	// Listen is the address the HTTP server binds.
	Listen string

	// Reserve is the free space kept in hand before refusing writes.
	Reserve uint64

	// ReadOnly refuses every write regardless of free space.
	ReadOnly bool
}

// SingleOrigin reports whether user content is served from the application
// origin, which requires the hardening described in the design document.
func (c Config) SingleOrigin() bool { return c.ContentURL == nil }

// SecureCookies reports whether session cookies may carry the Secure attribute.
//
// It follows the scheme of the public address rather than being a setting of
// its own. A Secure cookie sent over plain HTTP is simply discarded by the
// browser, which presents as an unexplained inability to sign in — deriving it
// removes a way to misconfigure the instance into that state.
func (c Config) SecureCookies() bool { return c.AppURL.Scheme == "https" }

// Load reads the configuration from the environment.
func Load() (Config, error) {
	cfg := Config{
		DataDir:   os.Getenv(EnvDataDir),
		ConfigDir: os.Getenv(EnvConfigDir),
		Listen:    valueOr(os.Getenv(EnvListen), DefaultListen),
		Reserve:   storage.DefaultReserve,
	}

	var err error
	if cfg.AppURL, err = requiredURL(EnvAppURL); err != nil {
		return Config{}, err
	}
	if raw := os.Getenv(EnvContentURL); raw != "" {
		if cfg.ContentURL, err = parseURL(EnvContentURL, raw); err != nil {
			return Config{}, err
		}
		if cfg.ContentURL.Host == cfg.AppURL.Host {
			return Config{}, fmt.Errorf(
				"config: %s and %s have the same host %q; separate origins are what keep an uploaded file from reaching a session",
				EnvAppURL, EnvContentURL, cfg.AppURL.Host)
		}
	}

	if cfg.DataDir == "" {
		return Config{}, fmt.Errorf("config: %s is required", EnvDataDir)
	}
	if cfg.ConfigDir == "" {
		return Config{}, fmt.Errorf("config: %s is required", EnvConfigDir)
	}
	if err := mustBeDirectory(EnvDataDir, cfg.DataDir); err != nil {
		return Config{}, err
	}

	if raw := os.Getenv(EnvReserve); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("config: %s must be a number of bytes: %w", EnvReserve, err)
		}
		cfg.Reserve = n
	}
	if raw := os.Getenv(EnvReadOnly); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: %s must be true or false: %w", EnvReadOnly, err)
		}
		cfg.ReadOnly = b
	}

	return cfg, nil
}

func requiredURL(key string) (*url.URL, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return nil, fmt.Errorf("config: %s is required", key)
	}
	return parseURL(key, raw)
}

func parseURL(key, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil {
		return nil, fmt.Errorf("config: %s is not a valid URL: %w", key, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("config: %s must use http or https, got %q", key, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("config: %s has no host", key)
	}
	if u.Path != "" {
		// Serving from a sub-path is not supported, and accepting the value
		// while ignoring it would produce links that quietly point elsewhere.
		return nil, fmt.Errorf("config: %s must not include a path, got %q", key, u.Path)
	}
	return u, nil
}

func mustBeDirectory(key, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Creating it here would turn a typo into a silently empty
			// instance, which looks like data loss to whoever deployed it.
			return fmt.Errorf("config: %s %q does not exist", key, dir)
		}
		return fmt.Errorf("config: %s %q: %w", key, dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config: %s %q is not a directory", key, dir)
	}
	return nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
