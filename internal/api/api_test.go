package api_test

import (
	"bytes"
	"context"
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
	"time"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/api"
	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/job"
	"github.com/krishna2206/zefile/internal/share"
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

	// A running worker, so a background copy actually completes in tests.
	jobs := job.New(database, job.WithPollInterval(20*time.Millisecond))
	jobs.Register(job.TypeCopy, func(ctx context.Context, payload string, report func(float64)) error {
		var p job.CopyPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return err
		}
		subject, err := engine.LoadSubject(ctx, p.UserID, p.IsAdmin)
		if err != nil {
			return err
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
	})
	workerCtx, stopWorker := context.WithCancel(context.Background())
	t.Cleanup(stopWorker)
	go jobs.Run(workerCtx)

	srv := httptest.NewServer(api.New(api.Options{
		FS: fs, Auth: service, ACL: engine,
		Trash: trash.New(database, fs),
		Shares: share.New(database, fs, share.GuardFunc(func(ctx context.Context, p storage.Path) (bool, error) {
			return engine.Allows(ctx, acl.PermShare, p)
		})),
		Jobs:          jobs,
		ContentBase:   "https://content.example",
		SecureCookies: false,
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

type shareItem struct {
	ID   int64  `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

type shareList struct {
	Shares []shareItem `json:"shares"`
}

func TestShareLifecycle(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	if err := os.WriteFile(filepath.Join(c.root, "report.pdf"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Create a link to the file.
	resp, raw := c.do(http.MethodPost, "/api/v1/shares", map[string]any{"path": "/report.pdf"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d: %s", resp.StatusCode, raw)
	}
	sh := decode[shareItem](t, raw)
	prefix := "https://content.example/s/"
	if !strings.HasPrefix(sh.URL, prefix) || len(sh.URL)-len(prefix) < 20 {
		t.Fatalf("share url = %q, want a /s/<token> link", sh.URL)
	}
	if sh.Name != "report.pdf" {
		t.Fatalf("name = %q", sh.Name)
	}

	// A folder can be shared, but not with a password yet (that would store a
	// password the browse flow never checks).
	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/folder"})
	if resp2, _ := c.do(http.MethodPost, "/api/v1/shares", map[string]any{"path": "/folder", "password": "x"}); resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("password folder share = %d, want 400", resp2.StatusCode)
	}

	// It shows up in the owner's list, without the token.
	_, raw = c.do(http.MethodGet, "/api/v1/shares", nil)
	list := decode[shareList](t, raw)
	if len(list.Shares) != 1 || list.Shares[0].ID != sh.ID {
		t.Fatalf("list = %+v", list.Shares)
	}
	if list.Shares[0].URL != "" {
		t.Fatalf("list leaked a token: %q", list.Shares[0].URL)
	}

	// Revoke it; the list empties, and revoking again is a 404.
	if resp3, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/v1/shares/%d", sh.ID), nil); resp3.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d", resp3.StatusCode)
	}
	if _, raw := c.do(http.MethodGet, "/api/v1/shares", nil); len(decode[shareList](t, raw).Shares) != 0 {
		t.Fatalf("list not empty after revoke: %s", raw)
	}
	if resp4, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/v1/shares/%d", sh.ID), nil); resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("re-revoke = %d, want 404", resp4.StatusCode)
	}
}

func TestFolderShareCreate(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/album"})
	resp, raw := c.do(http.MethodPost, "/api/v1/shares", map[string]any{"path": "/album"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("folder share = %d: %s", resp.StatusCode, raw)
	}
	if sh := decode[shareItem](t, raw); !strings.HasPrefix(sh.URL, "https://content.example/s/") {
		t.Fatalf("folder share url = %q", sh.URL)
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

func TestSearchWalksTheTree(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/photos/2024"})
	for _, f := range []struct{ rel, data string }{
		{"photos/2024/beach-sunset.jpg", "x"},
		{"photos/vacation-notes.txt", "y"},
		{"report.pdf", "z"},
	} {
		if err := os.WriteFile(filepath.Join(c.root, filepath.FromSlash(f.rel)), []byte(f.data), 0o644); err != nil {
			t.Fatalf("seed %s: %v", f.rel, err)
		}
	}

	type searchResp struct {
		Query     string  `json:"query"`
		Results   []entry `json:"results"`
		Truncated bool    `json:"truncated"`
	}
	search := func(q string) searchResp {
		resp, raw := c.do(http.MethodGet, "/api/v1/fs/search?"+q, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search %q: %d %s", q, resp.StatusCode, raw)
		}
		return decode[searchResp](t, raw)
	}

	// A nested file is found from the root by a substring of its name.
	if got := search("q=beach"); len(got.Results) != 1 || got.Results[0].Path != "/photos/2024/beach-sunset.jpg" {
		t.Fatalf("beach = %+v, want the nested jpg", got.Results)
	}

	// A directory whose own name matches is a result too.
	if got := search("q=photos"); len(got.Results) != 1 || got.Results[0].Path != "/photos" || !got.Results[0].IsDir {
		t.Fatalf("photos = %+v, want the /photos folder", got.Results)
	}

	// A scoped search does not reach outside its root.
	if got := search("q=report&path=/photos"); len(got.Results) != 0 {
		t.Fatalf("scoped report = %+v, want nothing under /photos", got.Results)
	}

	// An empty query is a well-formed no-op, not an error.
	if got := search("q="); len(got.Results) != 0 {
		t.Fatalf("empty query = %+v, want no results", got.Results)
	}
}

func TestCopyFolderRunsAsBackgroundJob(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/src/inner"})
	if err := os.WriteFile(filepath.Join(c.root, "src", "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(c.root, "src", "inner", "b.txt"), []byte("bbbb"), 0o644); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	// Copying a directory is accepted as a background job, not refused.
	resp, raw := c.do(http.MethodPost, "/api/v1/fs/copy", map[string]string{"from": "/src", "to": "/dst"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("copy folder = %d, want 202: %s", resp.StatusCode, raw)
	}
	wrap := decode[struct {
		Job struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}](t, raw)
	if wrap.Job.ID == 0 {
		t.Fatalf("no job id in %s", raw)
	}

	// Poll the job until it finishes.
	var status string
	for i := 0; i < 200; i++ {
		r, body := c.do(http.MethodGet, fmt.Sprintf("/api/v1/jobs/%d", wrap.Job.ID), nil)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("job get = %d: %s", r.StatusCode, body)
		}
		status = decode[struct {
			Status string `json:"status"`
		}](t, body).Status
		if status == "done" || status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "done" {
		t.Fatalf("job status = %q, want done", status)
	}

	// The tree is reproduced under the destination.
	top := decode[listing](t, mustList(c, "/dst"))
	if !hasEntry(top.Entries, "a.txt") || !hasEntry(top.Entries, "inner") {
		t.Fatalf("/dst = %+v, want a.txt and inner", top.Entries)
	}
	inner := decode[listing](t, mustList(c, "/dst/inner"))
	if !hasEntry(inner.Entries, "b.txt") {
		t.Fatalf("/dst/inner = %+v, want b.txt", inner.Entries)
	}
}

func mustList(c *client, path string) []byte {
	c.t.Helper()
	resp, raw := c.do(http.MethodGet, "/api/v1/fs?path="+url.QueryEscape(path), nil)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("list %s = %d: %s", path, resp.StatusCode, raw)
	}
	return raw
}

func hasEntry(entries []entry, name string) bool {
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

func TestInvitationLifecycle(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp() // creates the admin and keeps its token

	// The admin mints an invite.
	resp, raw := c.do(http.MethodPost, "/api/v1/invitations", map[string]string{"email": "bob@example.com"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invite = %d: %s", resp.StatusCode, raw)
	}
	invite := decode[struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Token string `json:"token"`
	}](t, raw)
	if invite.Token == "" || invite.Email != "bob@example.com" {
		t.Fatalf("invite = %+v, want a token and the email", invite)
	}

	// The public check says it is usable.
	_, raw = c.do(http.MethodGet, "/api/v1/invitations/check?token="+url.QueryEscape(invite.Token), nil)
	if chk := decode[struct {
		Valid bool   `json:"valid"`
		Email string `json:"email"`
	}](t, raw); !chk.Valid || chk.Email != "bob@example.com" {
		t.Fatalf("check = %+v, want valid with the email", chk)
	}

	// Accepting it creates a standard, non-admin account and signs it in.
	resp, raw = c.do(http.MethodPost, "/api/v1/invitations/accept", map[string]string{
		"token": invite.Token, "username": "bob", "password": "correct horse battery",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("accept = %d: %s", resp.StatusCode, raw)
	}
	accepted := decode[struct {
		User  userJSON `json:"user"`
		Token string   `json:"token"`
	}](t, raw)
	if accepted.User.Username != "bob" || accepted.User.IsAdmin {
		t.Fatalf("accepted user = %+v, want a non-admin bob", accepted.User)
	}
	bobToken := accepted.Token

	// The same token cannot be used twice.
	_, raw = c.do(http.MethodGet, "/api/v1/invitations/check?token="+url.QueryEscape(invite.Token), nil)
	if chk := decode[struct {
		Valid bool `json:"valid"`
	}](t, raw); chk.Valid {
		t.Fatalf("used token still reports valid")
	}

	// A second invite can be listed and revoked.
	_, raw = c.do(http.MethodPost, "/api/v1/invitations", map[string]string{})
	second := decode[struct {
		ID int64 `json:"id"`
	}](t, raw)
	if list := decode[struct {
		Invitations []struct {
			ID int64 `json:"id"`
		} `json:"invitations"`
	}](t, mustGet(c, "/api/v1/invitations")); len(list.Invitations) != 1 || list.Invitations[0].ID != second.ID {
		t.Fatalf("pending list = %+v, want only the second invite", list.Invitations)
	}
	if resp, raw = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/invitations/%d", second.ID), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", resp.StatusCode, raw)
	}

	// A non-admin cannot invite.
	admin := c.token
	c.token = bobToken
	resp, raw = c.do(http.MethodPost, "/api/v1/invitations", map[string]string{})
	c.token = admin
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin invite = %d, want 403: %s", resp.StatusCode, raw)
	}
}

type userJSON struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

func mustGet(c *client, path string) []byte {
	c.t.Helper()
	resp, raw := c.do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, raw)
	}
	return raw
}

func TestPermissionsEnforceSharing(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp() // admin

	if err := os.WriteFile(filepath.Join(c.root, "doc.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Bring in a non-admin through an invite.
	_, raw := c.do(http.MethodPost, "/api/v1/invitations", map[string]string{})
	inviteToken := decode[struct {
		Token string `json:"token"`
	}](t, raw).Token
	_, raw = c.do(http.MethodPost, "/api/v1/invitations/accept", map[string]string{
		"token": inviteToken, "username": "bob", "password": "correct horse battery",
	})
	bob := decode[struct {
		User  userJSON `json:"user"`
		Token string   `json:"token"`
	}](t, raw)
	admin := c.token

	// A fresh non-admin holds nothing, and admin-only endpoints refuse them.
	c.token = bob.Token
	if ps := decode[struct{ Read, Share bool }](t, mustGet(c, "/api/v1/permissions?path=/")); ps.Read || ps.Share {
		t.Fatalf("fresh non-admin perms = %+v, want none", ps)
	}
	if resp, _ := c.do(http.MethodGet, "/api/v1/users", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin GET /users = %d, want 403", resp.StatusCode)
	}
	if resp, _ := c.do(http.MethodPost, "/api/v1/access", map[string]any{
		"subject_id": bob.User.ID, "path": "/", "perms": map[string]bool{"read": true},
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin grant = %d, want 403", resp.StatusCode)
	}

	// Admin grants bob recursive read — but not share.
	c.token = admin
	if resp, raw := c.do(http.MethodPost, "/api/v1/access", map[string]any{
		"subject_id": bob.User.ID, "path": "/", "perms": map[string]bool{"read": true}, "recursive": true,
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("grant read = %d: %s", resp.StatusCode, raw)
	}

	// Bob can now read, and sharing is refused for lack of PermShare.
	c.token = bob.Token
	if ps := decode[struct{ Read, Share bool }](t, mustGet(c, "/api/v1/permissions?path=/doc.txt")); !ps.Read || ps.Share {
		t.Fatalf("after read grant, perms = %+v, want read only", ps)
	}
	if resp, raw := c.do(http.MethodPost, "/api/v1/shares", map[string]string{"path": "/doc.txt"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("share without PermShare = %d, want 403: %s", resp.StatusCode, raw)
	}

	// Admin grants share; now bob can create the link.
	c.token = admin
	c.do(http.MethodPost, "/api/v1/access", map[string]any{
		"subject_id": bob.User.ID, "path": "/", "perms": map[string]bool{"read": true, "share": true}, "recursive": true,
	})
	c.token = bob.Token
	if resp, raw := c.do(http.MethodPost, "/api/v1/shares", map[string]string{"path": "/doc.txt"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("share with PermShare = %d, want 201: %s", resp.StatusCode, raw)
	}
	c.token = admin
}

func TestUserManagement(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp() // admin "krishna"
	adminID := decode[userJSON](t, mustGet(c, "/api/v1/auth/me")).ID

	// Invite a non-admin.
	_, raw := c.do(http.MethodPost, "/api/v1/invitations", map[string]string{})
	inviteToken := decode[struct {
		Token string `json:"token"`
	}](t, raw).Token
	_, raw = c.do(http.MethodPost, "/api/v1/invitations/accept", map[string]string{
		"token": inviteToken, "username": "bob", "password": "correct horse battery",
	})
	bob := decode[struct {
		User  userJSON `json:"user"`
		Token string   `json:"token"`
	}](t, raw)

	// Give bob a rule, so deletion has ACL state to clean up.
	c.do(http.MethodPost, "/api/v1/access", map[string]any{
		"subject_id": bob.User.ID, "path": "/", "perms": map[string]bool{"read": true}, "recursive": true,
	})

	// You cannot change your own account here.
	if resp, _ := c.do(http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", adminID), map[string]any{"disabled": true}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self-disable = %d, want 400", resp.StatusCode)
	}

	// Promote bob.
	if got := decode[userJSON](t, mustPatch(c, fmt.Sprintf("/api/v1/users/%d", bob.User.ID), map[string]any{"is_admin": true})); !got.IsAdmin {
		t.Fatalf("promote: is_admin = %v, want true", got.IsAdmin)
	}

	// Disabling ends bob's sessions immediately.
	c.do(http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", bob.User.ID), map[string]any{"disabled": true})
	saved := c.token
	c.token = bob.Token
	if resp, _ := c.do(http.MethodGet, "/api/v1/auth/me", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled user's session = %d, want 401", resp.StatusCode)
	}
	c.token = saved

	// Delete bob: gone from the list, and their rule is cleaned up.
	if resp, raw := c.do(http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", bob.User.ID), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", resp.StatusCode, raw)
	}
	users := decode[struct {
		Users []userJSON `json:"users"`
	}](t, mustGet(c, "/api/v1/users"))
	for _, u := range users.Users {
		if u.ID == bob.User.ID {
			t.Fatalf("bob still listed after delete")
		}
	}
	rules := decode[struct {
		Rules []struct {
			SubjectID int64 `json:"subject_id"`
		} `json:"rules"`
	}](t, mustGet(c, "/api/v1/access?path=/"))
	for _, r := range rules.Rules {
		if r.SubjectID == bob.User.ID {
			t.Fatalf("bob's ACL rule survived deletion")
		}
	}
}

func mustPatch(c *client, path string, body any) []byte {
	c.t.Helper()
	resp, raw := c.do(http.MethodPatch, path, body)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("PATCH %s = %d: %s", path, resp.StatusCode, raw)
	}
	return raw
}

func TestListingTraversesToGrants(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/partage"})
	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/private"})
	if err := os.WriteFile(filepath.Join(c.root, "partage", "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Bring in a non-admin.
	_, raw := c.do(http.MethodPost, "/api/v1/invitations", map[string]string{})
	inviteToken := decode[struct {
		Token string `json:"token"`
	}](t, raw).Token
	_, raw = c.do(http.MethodPost, "/api/v1/invitations/accept", map[string]string{
		"token": inviteToken, "username": "bob", "password": "correct horse battery",
	})
	bob := decode[struct {
		User  userJSON `json:"user"`
		Token string   `json:"token"`
	}](t, raw)
	admin := c.token

	// With no grants, bob's root lists empty rather than failing.
	c.token = bob.Token
	if got := decode[listing](t, mustList(c, "/")); len(got.Entries) != 0 {
		t.Fatalf("no-grant root = %+v, want empty", got.Entries)
	}

	// Grant bob read on /partage.
	c.token = admin
	c.do(http.MethodPost, "/api/v1/access", map[string]any{
		"subject_id": bob.User.ID, "path": "/partage", "perms": map[string]bool{"read": true}, "recursive": true,
	})

	// Bob's root now shows the granted folder and nothing else.
	c.token = bob.Token
	root := decode[listing](t, mustList(c, "/"))
	if !hasEntry(root.Entries, "partage") || hasEntry(root.Entries, "private") {
		t.Fatalf("root = %+v, want partage only", root.Entries)
	}
	// He can list inside it,
	if inside := decode[listing](t, mustList(c, "/partage")); !hasEntry(inside.Entries, "note.txt") {
		t.Fatalf("/partage = %+v, want note.txt", inside.Entries)
	}
	// but a folder he has no path to is denied outright.
	if resp, _ := c.do(http.MethodGet, "/api/v1/fs?path=/private", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("/private = %d, want 403", resp.StatusCode)
	}
	c.token = admin
}

func TestRenamePreservesAccess(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	c.setUp()

	c.do(http.MethodPost, "/api/v1/fs/dirs", map[string]string{"path": "/partage"})
	if err := os.WriteFile(filepath.Join(c.root, "partage", "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, raw := c.do(http.MethodPost, "/api/v1/invitations", map[string]string{})
	inviteToken := decode[struct {
		Token string `json:"token"`
	}](t, raw).Token
	_, raw = c.do(http.MethodPost, "/api/v1/invitations/accept", map[string]string{
		"token": inviteToken, "username": "bob", "password": "correct horse battery",
	})
	bob := decode[struct {
		User  userJSON `json:"user"`
		Token string   `json:"token"`
	}](t, raw)
	admin := c.token

	c.do(http.MethodPost, "/api/v1/access", map[string]any{
		"subject_id": bob.User.ID, "path": "/partage", "perms": map[string]bool{"read": true}, "recursive": true,
	})

	// Rename the shared folder.
	if resp, body := c.do(http.MethodPost, "/api/v1/fs/move", map[string]string{"from": "/partage", "to": "/renamed"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename = %d: %s", resp.StatusCode, body)
	}

	// The rule moved with it: nothing left at the old path, bob's rule at the new.
	if old := decode[struct {
		Rules []struct {
			SubjectID int64 `json:"subject_id"`
		} `json:"rules"`
	}](t, mustGet(c, "/api/v1/access?path=/partage")); len(old.Rules) != 0 {
		t.Fatalf("rules stranded at old path: %+v", old.Rules)
	}
	now := decode[struct {
		Rules []struct {
			SubjectID int64 `json:"subject_id"`
		} `json:"rules"`
	}](t, mustGet(c, "/api/v1/access?path=/renamed"))
	if len(now.Rules) != 1 || now.Rules[0].SubjectID != bob.User.ID {
		t.Fatalf("rule did not follow the rename: %+v", now.Rules)
	}

	// Bob still reaches the folder under its new name.
	c.token = bob.Token
	if inside := decode[listing](t, mustList(c, "/renamed")); !hasEntry(inside.Entries, "note.txt") {
		t.Fatalf("bob lost access after rename: %+v", inside.Entries)
	}
	c.token = admin
}
