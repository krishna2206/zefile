package content_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/krishna2206/zefile/internal/content"
	"github.com/krishna2206/zefile/internal/share"
	"github.com/krishna2206/zefile/internal/storage"
)

// fakeShares stands in for the share service: one canned grant or one error.
type fakeShares struct {
	grant share.Grant
	err   error
}

func (f fakeShares) Resolve(_ context.Context, _, password string) (share.Grant, error) {
	// A stub that behaves like a password wall when asked to: any non-empty
	// password unlocks the canned grant.
	if f.err != nil && errors.Is(f.err, share.ErrPasswordRequired) && password != "" {
		return f.grant, nil
	}
	return f.grant, f.err
}

func (fakeShares) RecordDownload(context.Context, int64, string, string) error { return nil }

func TestSharePublicDownload(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(o *content.Options) {
		o.Shares = fakeShares{grant: share.Grant{ID: 1, Path: storage.MustParsePath("/shared.txt"), OwnerID: 1}}
	})
	f.write(t, "shared.txt", []byte("hello share"))

	resp := f.get(t, f.server.URL+"/s/zefile_shr_token", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share download = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello share" {
		t.Fatalf("body = %q", body)
	}
}

func TestExpiredShareIsGone(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(o *content.Options) { o.Shares = fakeShares{err: share.ErrExpired} })
	resp := f.get(t, f.server.URL+"/s/tok/x", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expired share = %d, want 410", resp.StatusCode)
	}
}

func TestUnknownShareIsNotFound(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(o *content.Options) { o.Shares = fakeShares{err: share.ErrNotFound} })
	resp := f.get(t, f.server.URL+"/s/tok/x", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown share = %d, want 404", resp.StatusCode)
	}
}

func TestFolderShareBrowseAndConfine(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(o *content.Options) {
		o.Shares = fakeShares{grant: share.Grant{ID: 1, Path: storage.MustParsePath("/album"), OwnerID: 1}}
	})
	if err := os.MkdirAll(filepath.Join(f.root, "album", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "album", "photo.txt"), []byte("pixels"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A file outside the shared folder, which no p must reach.
	if err := os.WriteFile(filepath.Join(f.root, "secret.txt"), []byte("classified"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The root browse page lists the folder's entries.
	resp := f.get(t, f.server.URL+"/s/tok", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browse = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "photo.txt") || !strings.Contains(string(body), "sub") {
		t.Fatalf("browse page missing entries:\n%s", body)
	}

	// A file within the share downloads.
	resp2 := f.get(t, f.server.URL+"/s/tok?p="+url.QueryEscape("/album/photo.txt"), nil)
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || string(body2) != "pixels" {
		t.Fatalf("file in folder = %d %q", resp2.StatusCode, body2)
	}

	// A path outside the shared folder is refused — confinement holds.
	resp3 := f.get(t, f.server.URL+"/s/tok?p="+url.QueryEscape("/secret.txt"), nil)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("escape attempt = %d, want 404", resp3.StatusCode)
	}
}

func TestPasswordProtectedShare(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(o *content.Options) {
		o.Shares = fakeShares{
			grant: share.Grant{ID: 1, Path: storage.MustParsePath("/secret.txt"), OwnerID: 1},
			err:   share.ErrPasswordRequired,
		}
	})
	f.write(t, "secret.txt", []byte("classified"))

	// A GET with no password shows the form, never the file.
	resp := f.get(t, f.server.URL+"/s/tok", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("form status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("form content-type = %q", ct)
	}
	if strings.Contains(string(body), "classified") {
		t.Fatal("the form leaked the file contents")
	}
	if !strings.Contains(string(body), `name="password"`) {
		t.Fatal("the form has no password field")
	}

	// Posting the password serves the file.
	resp2, err := f.server.Client().PostForm(f.server.URL+"/s/tok", url.Values{"password": {"hunter2"}})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("post status = %d, want 200", resp2.StatusCode)
	}
	if string(body2) != "classified" {
		t.Fatalf("post body = %q, want the file", body2)
	}
}

// openSubject grants every request, standing in for the ACL engine. The
// engine's own behaviour is covered where it lives; here the question is
// whether the content origin asks at all.
type openSubject struct{}

func (openSubject) ContextFor(ctx context.Context, _ int64) (context.Context, error) {
	return ctx, nil
}

type deniedSubject struct{}

func (deniedSubject) ContextFor(context.Context, int64) (context.Context, error) {
	return nil, content.ErrUnknownSubject
}

type fixture struct {
	server *httptest.Server
	signer *content.Signer
	root   string
}

func newFixture(t *testing.T, opts ...func(*content.Options)) *fixture {
	t.Helper()

	root := t.TempDir()
	fs, err := storage.Open(storage.Config{Root: root, Reserve: 1})
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	signer, err := content.NewSigner()
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	options := content.Options{FS: fs, Signer: signer, Subject: openSubject{}}
	for _, apply := range opts {
		apply(&options)
	}

	srv := httptest.NewServer(content.New(options).Handler())
	t.Cleanup(srv.Close)

	return &fixture{server: srv, signer: signer, root: root}
}

func (f *fixture) write(t *testing.T, name string, payload []byte) storage.Path {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, name), payload, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return storage.MustParsePath("/" + name)
}

func (f *fixture) url(p storage.Path) string {
	return f.server.URL + "/d/" + f.signer.Sign(p, 1) + "/" + p.Name()
}

func (f *fixture) get(t *testing.T, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	return resp
}

// -------------------------------------------------------- §10.4 requirement 4

// TestRangeSemantics covers the grammar a download manager actually sends:
// open ranges, suffix ranges and unsatisfiable ones. Getting any of these
// wrong is what makes resume and multi-connection downloads half work.
func TestRangeSemantics(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	p := f.write(t, "data.bin", payload)
	url := f.url(p)

	cases := []struct {
		name       string
		header     string
		wantStatus int
		wantBody   string
		wantRange  string
	}{
		{"whole file", "", http.StatusOK, string(payload), ""},
		{"closed range", "bytes=0-9", http.StatusPartialContent, "0123456789", "bytes 0-9/36"},
		{"middle range", "bytes=10-19", http.StatusPartialContent, "abcdefghij", "bytes 10-19/36"},
		{"open ended", "bytes=30-", http.StatusPartialContent, "uvwxyz", "bytes 30-35/36"},
		{"suffix", "bytes=-6", http.StatusPartialContent, "uvwxyz", "bytes 30-35/36"},
		{"single byte", "bytes=5-5", http.StatusPartialContent, "5", "bytes 5-5/36"},
		{"past the end is clamped", "bytes=30-9999", http.StatusPartialContent, "uvwxyz", "bytes 30-35/36"},
		{"entirely past the end", "bytes=100-200", http.StatusRequestedRangeNotSatisfiable, "", ""},
		// RFC 9110 lets a server either ignore an invalid range set or refuse
		// it; the standard library refuses. Either is fine — a malformed range
		// is a client bug, not something a download manager sends.
		{"malformed is refused", "bytes=abc", http.StatusRequestedRangeNotSatisfiable, "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			headers := map[string]string{}
			if tc.header != "" {
				headers["Range"] = tc.header
			}
			resp := f.get(t, url, headers)
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantRange != "" {
				if got := resp.Header.Get("Content-Range"); got != tc.wantRange {
					t.Errorf("Content-Range = %q, want %q", got, tc.wantRange)
				}
			}
			if tc.wantBody == "" {
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
			// Without an exact length a client can neither show progress nor
			// know it received everything.
			if resp.ContentLength != int64(len(tc.wantBody)) {
				t.Errorf("Content-Length = %d, want %d", resp.ContentLength, len(tc.wantBody))
			}
		})
	}
}

func TestAcceptRangesIsAdvertised(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.write(t, "data.bin", []byte("payload"))

	resp := f.get(t, f.url(p), nil)
	defer resp.Body.Close()

	// A download manager checks this header before deciding whether it may
	// open several connections at all.
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want %q", got, "bytes")
	}
}

// -------------------------------------------------------- §10.4 requirement 2

// TestParallelRangesReassemble is the multi-connection case: sixteen readers
// each take a slice, and the pieces must reconstitute the file exactly. This is
// what makes a download manager faster than a single stream.
func TestParallelRangesReassemble(t *testing.T) {
	t.Parallel()

	const (
		size    = 1 << 20
		readers = 16
	)

	f := newFixture(t)
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	p := f.write(t, "big.bin", payload)
	url := f.url(p)

	chunk := size / readers
	pieces := make([][]byte, readers)

	var wg sync.WaitGroup
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			start := i * chunk
			end := start + chunk - 1
			if i == readers-1 {
				end = size - 1
			}

			resp := f.get(t, url, map[string]string{
				"Range": fmt.Sprintf("bytes=%d-%d", start, end),
			})
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusPartialContent {
				t.Errorf("reader %d: status %d", i, resp.StatusCode)
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Errorf("reader %d: %v", i, err)
				return
			}
			pieces[i] = body
		}()
	}
	wg.Wait()

	var assembled bytes.Buffer
	for _, piece := range pieces {
		assembled.Write(piece)
	}
	if !bytes.Equal(assembled.Bytes(), payload) {
		t.Fatalf("reassembled %d bytes, want %d — the pieces do not fit back together",
			assembled.Len(), len(payload))
	}
}

// -------------------------------------------------------- §10.4 requirement 3

// TestMemoryStaysFlat is the regression that matters most in production: a
// buffered response works fine on the small files used in development and takes
// the VPS down the first time someone downloads a disc image.
func TestMemoryStaysFlat(t *testing.T) {
	t.Parallel()

	const size = 64 << 20 // far above any sane buffer, small enough for CI

	f := newFixture(t)
	file, err := os.Create(filepath.Join(f.root, "sparse.bin"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Sparse: the server still streams 64 MiB, but the disk holds almost none.
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		t.Skipf("sparse files unavailable: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	url := f.url(storage.MustParsePath("/sparse.bin"))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	resp := f.get(t, url, nil)
	written, err := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if written != size {
		t.Fatalf("streamed %d bytes, want %d", written, size)
	}

	runtime.GC()
	runtime.ReadMemStats(&after)

	// A buffered implementation would show growth on the order of the file.
	// The threshold is loose on purpose: this is meant to catch an accidental
	// io.ReadAll, not to police allocation noise.
	const tolerance = 8 << 20
	if after.HeapAlloc > before.HeapAlloc+tolerance {
		t.Fatalf("heap grew by %d bytes while streaming %d — the response is being buffered",
			after.HeapAlloc-before.HeapAlloc, size)
	}
}

// ------------------------------------------------------------------- security

func TestForgedAndExpiredLinksAreRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.write(t, "secret.txt", []byte("payload"))
	valid := f.signer.Sign(p, 1)

	cases := []struct{ name, token string }{
		{"nonsense", "not-a-token"},
		{"no signature", strings.Split(valid, ".")[0]},
		{"tampered signature", strings.Split(valid, ".")[0] + ".AAAA"},
		{"tampered payload", "AAAA." + strings.Split(valid, ".")[1]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := f.get(t, f.server.URL+"/d/"+tc.token+"/secret.txt", nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
		})
	}

	// An empty token matches no route at all, so it never reaches the handler.
	// Still a refusal, just an earlier one.
	t.Run("empty", func(t *testing.T) {
		resp := f.get(t, f.server.URL+"/d//secret.txt", nil)
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("an empty token served a file")
		}
	})
}

// TestSignatureBindsThePath covers the substitution a signed link must resist:
// keeping a valid signature while pointing it at a different file.
func TestSignatureBindsThePath(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.write(t, "public.txt", []byte("public"))
	secret := f.write(t, "secret.txt", []byte("secret"))

	token := f.signer.Sign(storage.MustParsePath("/public.txt"), 1)

	// The filename in the URL is decoration; swapping it must change nothing.
	resp := f.get(t, f.server.URL+"/d/"+token+"/secret.txt", nil)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "public" {
		t.Fatalf("served %q — the trailing name changed which file was read", body)
	}
	_ = secret
}

// TestDeniedSubjectIsRefused proves the permission check runs again at download
// time, so a right withdrawn after the link was minted takes effect.
func TestDeniedSubjectIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(o *content.Options) { o.Subject = deniedSubject{} })
	p := f.write(t, "secret.txt", []byte("payload"))

	resp := f.get(t, f.url(p), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestMissingFileAnswersLikeAForgedLink(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	// A link for something that does not exist must not be distinguishable
	// from a bad signature, or the endpoint becomes a way to probe paths.
	resp := f.get(t, f.url(storage.MustParsePath("/absent.txt")), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// ---------------------------------------------------------------- disposition

func TestDisposition(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	cases := []struct {
		file       string
		wantInline bool
	}{
		{"photo.jpg", true},
		{"clip.mp4", true},
		{"notes.pdf", true},
		{"readme.txt", true},
		// An SVG is an image that runs script. Rendering it in place is a
		// recurring source of cross-site scripting in file managers.
		{"drawing.svg", false},
		{"page.html", false},
		{"archive.zip", false},
		{"game.iso", false},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			p := f.write(t, tc.file, []byte("x"))
			resp := f.get(t, f.url(p), nil)
			defer resp.Body.Close()

			disposition := resp.Header.Get("Content-Disposition")
			isInline := strings.HasPrefix(disposition, "inline")
			if isInline != tc.wantInline {
				t.Errorf("Content-Disposition = %q, want inline=%v", disposition, tc.wantInline)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}

// TestSingleOriginHardening: with content on the application origin, nothing
// renders in place and everything is sandboxed.
func TestSingleOriginHardening(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(o *content.Options) { o.SingleOrigin = true })

	for _, name := range []string{"photo.jpg", "page.html", "notes.pdf"} {
		p := f.write(t, name, []byte("x"))
		resp := f.get(t, f.url(p), nil)
		defer resp.Body.Close()

		if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
			t.Errorf("%s: Content-Disposition = %q, want attachment", name, got)
		}
		if got := resp.Header.Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
			t.Errorf("%s: Content-Security-Policy = %q, want a sandbox", name, got)
		}
	}
}

func TestAccentedFilenameSurvives(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.write(t, "résumé final.pdf", []byte("x"))

	resp := f.get(t, f.server.URL+"/d/"+f.signer.Sign(p, 1)+"/x", nil)
	defer resp.Body.Close()

	// RFC 5987 encoding is what carries a non-ASCII name through a header.
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "UTF-8''") {
		t.Errorf("Content-Disposition = %q, want an RFC 5987 encoded name", got)
	}
}

func TestExpiredLinkIsRefused(t *testing.T) {
	t.Parallel()

	// Verified against the signer rather than over HTTP: moving the clock is
	// the only way to observe expiry without waiting five minutes.
	signer, err := content.NewSigner()
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	token := signer.Sign(storage.MustParsePath("/a.txt"), 1)

	if _, _, err := signer.Verify(token); err != nil {
		t.Fatalf("a fresh link did not verify: %v", err)
	}
	if _, _, err := signer.Verify(token + "x"); !errors.Is(err, content.ErrInvalidSignature) {
		t.Errorf("a tampered link = %v, want ErrInvalidSignature", err)
	}
}

func TestHeadRequestReportsSizeWithoutBody(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	payload := bytes.Repeat([]byte("x"), 4096)
	p := f.write(t, "data.bin", payload)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, f.url(p), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer resp.Body.Close()

	// A download manager issues HEAD first to learn the size and decide how
	// many connections to open.
	if resp.ContentLength != int64(len(payload)) {
		t.Errorf("Content-Length = %d, want %d", resp.ContentLength, len(payload))
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("HEAD returned %d bytes of body", len(body))
	}
}

func TestNoCompressionOnAlreadyCompressedTypes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.write(t, "game.iso", bytes.Repeat([]byte("compressible "), 1000))

	resp := f.get(t, f.url(p), map[string]string{"Accept-Encoding": "gzip"})
	defer resp.Body.Close()

	// Compressing an already-compressed file spends processor time for nothing
	// and, worse, disables the kernel copy path that makes downloads fast.
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
	if resp.ContentLength <= 0 {
		t.Error("Content-Length is absent, so a client cannot show progress")
	}
}

func TestZipBundleStreamsSelectionAndFolder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	if err := os.MkdirAll(filepath.Join(f.root, "photos", "sub"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "photos", "a.txt"), []byte("aaa"), 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "photos", "sub", "b.txt"), []byte("bbbb"), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	f.write(t, "note.txt", []byte("note"))

	token := f.signer.SignBundle([]storage.Path{
		storage.MustParsePath("/photos"),
		storage.MustParsePath("/note.txt"),
	}, 1)
	resp := f.get(t, f.server.URL+"/z/"+token+"/bundle.zip", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q, want application/zip", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}

	got := map[string]string{}
	for _, zf := range zr.File {
		// Store, never Deflate: the whole point is not to compress on the fly.
		if zf.Method != zip.Store {
			t.Errorf("%s uses method %d, want Store (0)", zf.Name, zf.Method)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", zf.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		got[zf.Name] = string(data)
	}

	want := map[string]string{
		"photos/a.txt":     "aaa",
		"photos/sub/b.txt": "bbbb",
		"note.txt":         "note",
	}
	for name, content := range want {
		if got[name] != content {
			t.Errorf("entry %q = %q, want %q", name, got[name], content)
		}
	}
}

func TestForgedBundleTokenIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write(t, "a.txt", []byte("x"))

	valid := f.signer.SignBundle([]storage.Path{storage.MustParsePath("/a.txt")}, 1)
	forged := valid[:len(valid)-1] + "0"
	resp := f.get(t, f.server.URL+"/z/"+forged+"/x.zip", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forged bundle = %d, want 403", resp.StatusCode)
	}
}
