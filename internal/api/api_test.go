package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/api"
	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/storage"
	"github.com/krishna2206/zefile/internal/trash"
)

// The tests here drive the server the way a client does: over HTTP, through the
// real router, storage layer and ACL engine. This is the lot's completion
// criterion — the tree navigable and manipulable from outside the process.

type client struct {
	t       *testing.T
	server  *httptest.Server
	token   string
	root    string
	auth    *auth.Service
	aclEngi *acl.Engine
}

func newClient(t *testing.T) *client {
	t.Helper()

	database, err := db.Open(t.Context(), db.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	engine := acl.New(database)
	root := t.TempDir()
	fs, err := storage.Open(storage.Config{Root: root, Guard: engine, Reserve: 1})
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	// Cheap hashing: these tests exercise routing and wiring, not Argon2.
	service := auth.New(database, auth.WithParams(auth.Params{
		Memory: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	}))

	srv := httptest.NewServer(api.New(api.Options{
		FS: fs, Auth: service, ACL: engine, Trash: trash.New(database, fs), SecureCookies: false,
	}).Handler())
	t.Cleanup(srv.Close)

	return &client{t: t, server: srv, root: root, auth: service, aclEngi: engine}
}

// do issues a request, attaching the bearer token once signed in.
func (c *client) do(method, path string, body any) (*http.Response, []byte) {
	c.t.Helper()

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode body: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(c.t.Context(), method, c.server.URL+path, payload)
	if err != nil {
		c.t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.server.Client().Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read body: %v", err)
	}
	return resp, raw
}

// setUp completes first-run setup and keeps the returned token.
func (c *client) setUp() {
	c.t.Helper()

	// Startup mints the first setup token rather than an endpoint, so the test
	// does the same thing the binary does.
	token, err := c.auth.IssueSetupToken(c.t.Context())
	if err != nil {
		c.t.Fatalf("IssueSetupToken: %v", err)
	}

	resp, raw := c.do(http.MethodPost, "/api/v1/setup", map[string]string{
		"token":    token,
		"username": "krishna",
		"password": "correct horse battery",
	})
	if resp.StatusCode != http.StatusCreated {
		c.t.Fatalf("setup returned %d: %s", resp.StatusCode, raw)
	}

	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &session); err != nil {
		c.t.Fatalf("decode setup response: %v", err)
	}
	c.token = session.Token
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

type problem struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
}

type entry struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

type listing struct {
	Path    string  `json:"path"`
	Entries []entry `json:"entries"`
}

func TestHealth(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	resp, raw := c.do(http.MethodGet, "/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
}

func TestUnauthenticatedIsRefused(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	for _, path := range []string{"/api/v1/fs", "/api/v1/fs/stat?path=/", "/api/v1/auth/me"} {
		resp, raw := c.do(http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s returned %d, want 401: %s", path, resp.StatusCode, raw)
		}
		if got := decode[problem](t, raw); got.Code != api.CodeUnauthenticated {
			t.Errorf("%s code = %q, want %q", path, got.Code, api.CodeUnauthenticated)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%s content type = %q, want application/problem+json", path, ct)
		}
	}
}

// TestFullLifecycle walks the tree the way a client does: sign in, create,
// list, copy, move, delete. It is the lot's completion criterion.
func TestFullLifecycle(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	// An empty instance lists an empty root rather than failing.
	resp, raw := c.do(http.MethodGet, "/api/v1/fs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list root: %d %s", resp.StatusCode, raw)
	}
	if got := decode[listing](t, raw); len(got.Entries) != 0 || got.Path != "/" {
		t.Fatalf("fresh root = %+v, want an empty listing at /", got)
	}

	// Nested creation in one call.
	resp, raw = c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/jeux/steam"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mkdir: %d %s", resp.StatusCode, raw)
	}

	// A file placed out of band must appear: the filesystem is the authority.
	if err := os.WriteFile(filepath.Join(c.root, "jeux", "steam", "doom.iso"), []byte("payload"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, raw = c.do(http.MethodGet, "/api/v1/fs?path=/jeux/steam", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d %s", resp.StatusCode, raw)
	}
	entries := decode[listing](t, raw).Entries
	if len(entries) != 1 || entries[0].Name != "doom.iso" || entries[0].Size != 7 {
		t.Fatalf("listing = %+v, want the file dropped in over SSH", entries)
	}

	resp, raw = c.do(http.MethodPost, "/api/v1/fs/copy",
		map[string]string{"from": "/jeux/steam/doom.iso", "to": "/jeux/steam/doom-copie.iso"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy: %d %s", resp.StatusCode, raw)
	}

	resp, raw = c.do(http.MethodPost, "/api/v1/fs/move",
		map[string]string{"from": "/jeux/steam/doom-copie.iso", "to": "/jeux/doom2.iso"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move: %d %s", resp.StatusCode, raw)
	}
	if got := decode[entry](t, raw); got.Path != "/jeux/doom2.iso" {
		t.Errorf("move returned %q", got.Path)
	}

	resp, raw = c.do(http.MethodDelete, "/api/v1/fs?path=/jeux/doom2.iso", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d %s", resp.StatusCode, raw)
	}
	if _, err := os.Stat(filepath.Join(c.root, "jeux", "doom2.iso")); !os.IsNotExist(err) {
		t.Error("the file survived the delete")
	}
}

type trashItem struct {
	ID           int64  `json:"id"`
	OriginalPath string `json:"original_path"`
	IsDir        bool   `json:"is_dir"`
}

type trashList struct {
	Items []trashItem `json:"items"`
}

func TestDeleteMovesToTrashAndRestores(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	// A non-empty directory: deleting it moves the whole tree at once, so the
	// old "recursive" flag no longer means anything.
	if _, raw := c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/dossier/sous"}); raw == nil {
		t.Fatal("mkdir returned nothing")
	}

	resp, raw := c.do(http.MethodDelete, "/api/v1/fs?path=/dossier", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", resp.StatusCode, raw)
	}

	// Gone from the listing...
	if _, raw := c.do(http.MethodGet, "/api/v1/fs?path=/", nil); strings.Contains(string(raw), "dossier") {
		t.Fatalf("deleted folder still listed: %s", raw)
	}

	// ...but sitting in the trash, remembering where it came from.
	_, raw = c.do(http.MethodGet, "/api/v1/trash", nil)
	list := decode[trashList](t, raw)
	if len(list.Items) != 1 || list.Items[0].OriginalPath != "/dossier" || !list.Items[0].IsDir {
		t.Fatalf("trash = %+v", list.Items)
	}
	id := list.Items[0].ID

	// Restoring puts it back where it was and empties the trash.
	resp, raw = c.do(http.MethodPost, fmt.Sprintf("/api/v1/trash/%d/restore", id), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("restore = %d: %s", resp.StatusCode, raw)
	}
	if _, raw := c.do(http.MethodGet, "/api/v1/fs?path=/", nil); !strings.Contains(string(raw), "dossier") {
		t.Fatalf("restored folder not listed: %s", raw)
	}
	if _, raw := c.do(http.MethodGet, "/api/v1/trash", nil); len(decode[trashList](t, raw).Items) != 0 {
		t.Fatalf("trash not empty after restore: %s", raw)
	}
}

func TestThumbnailResizesImages(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	// A 800x600 image written straight into the storage tree.
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for x := 0; x < 800; x++ {
		img.Set(x, 0, color.RGBA{R: uint8(x % 256), G: 120, B: 200, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(filepath.Join(c.root, "pic.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	resp, raw := c.do(http.MethodGet, "/api/v1/fs/thumb?path=/pic.png&s=256", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thumb = %d: %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type = %q, want image/jpeg", ct)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	// Capped at 256 on the long side; the landscape source scales to 256x192.
	if cfg.Width != 256 || cfg.Height != 192 {
		t.Fatalf("thumbnail is %dx%d, want 256x192", cfg.Width, cfg.Height)
	}

	// A non-image is refused rather than streamed as if it were one.
	if err := os.WriteFile(filepath.Join(c.root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	resp2, _ := c.do(http.MethodGet, "/api/v1/fs/thumb?path=/notes.txt", nil)
	if resp2.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("txt thumb = %d, want 415", resp2.StatusCode)
	}
}

func TestTrashPurgeRemovesForGood(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/jetable"})
	c.do(http.MethodDelete, "/api/v1/fs?path=/jetable", nil)

	_, raw := c.do(http.MethodGet, "/api/v1/trash", nil)
	list := decode[trashList](t, raw)
	if len(list.Items) != 1 {
		t.Fatalf("trash = %+v", list.Items)
	}

	resp, raw := c.do(http.MethodDelete, fmt.Sprintf("/api/v1/trash/%d", list.Items[0].ID), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("purge = %d: %s", resp.StatusCode, raw)
	}
	if _, raw := c.do(http.MethodGet, "/api/v1/trash", nil); len(decode[trashList](t, raw).Items) != 0 {
		t.Fatalf("trash not empty after purge: %s", raw)
	}
}

func TestPathValidationReachesTheClient(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	for _, bad := range []string{"/../etc/passwd", "relative", "/a//b", "/a/./b"} {
		resp, raw := c.do(http.MethodGet, "/api/v1/fs/stat?path="+url.QueryEscape(bad), nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q returned %d, want 400: %s", bad, resp.StatusCode, raw)
			continue
		}
		if got := decode[problem](t, raw); got.Code != api.CodeInvalidPath {
			t.Errorf("%q code = %q, want %q", bad, got.Code, api.CodeInvalidPath)
		}
	}
}

// TestLogoutEndsTheSessionImmediately is the File Browser defect checked at the
// HTTP boundary rather than in the domain: the very next request must fail.
func TestLogoutEndsTheSessionImmediately(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	if resp, raw := c.do(http.MethodGet, "/api/v1/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("me before logout: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := c.do(http.MethodPost, "/api/v1/auth/logout", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d %s", resp.StatusCode, raw)
	}

	resp, raw := c.do(http.MethodGet, "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the token still worked after logout: %d %s", resp.StatusCode, raw)
	}
}

func TestLoginAndCookie(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	resp, raw := c.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "krishna", "password": "correct horse battery"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %s", resp.StatusCode, raw)
	}

	var found bool
	for _, cookie := range resp.Cookies() {
		if cookie.Name != auth.SessionCookieName {
			continue
		}
		found = true
		if !cookie.HttpOnly {
			t.Error("the session cookie is readable by scripts")
		}
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
		}
	}
	if !found {
		t.Fatal("login set no session cookie")
	}

	if resp, raw = c.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "krishna", "password": "wrong"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401: %s", resp.StatusCode, raw)
	}
	if got := decode[problem](t, raw); got.Code != api.CodeInvalidCredentials {
		t.Errorf("code = %q, want %q", got.Code, api.CodeInvalidCredentials)
	}
}

func TestMalformedBodyIsRejected(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	// An unknown field is refused rather than ignored, so a client with a typo
	// is told instead of silently getting the default.
	resp, raw := c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"chemin": "/x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400: %s", resp.StatusCode, raw)
	}
}
