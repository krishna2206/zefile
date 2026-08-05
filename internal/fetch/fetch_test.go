package fetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// testPolicy allows loopback (so httptest servers work) but lets a test mark
// specific addresses blocked, which is how the redirect-revalidation path is
// exercised without a real internal service.
func testPolicy(blocked func(netip.Addr) bool) Policy {
	p := DefaultPolicy()
	p.Blocked = blocked
	return p
}

func addrOf(t *testing.T, server *httptest.Server) netip.Addr {
	t.Helper()
	host, _, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split server addr: %v", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		t.Fatalf("parse server addr: %v", err)
	}
	return ip.Unmap()
}

func TestFetchPlain(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="image.iso"`)
		_, _ = io.WriteString(w, "payload")
	}))
	defer srv.Close()

	f := New(testPolicy(func(netip.Addr) bool { return false }))
	res, err := f.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer res.Body.Close()

	if res.Filename != "image.iso" {
		t.Errorf("filename = %q, want image.iso", res.Filename)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "payload" {
		t.Errorf("body = %q", body)
	}
}

// TestFetchRefusesRedirectToBlocked is the roadmap's acceptance criterion: a
// URL that redirects to a blocked (internal) address must be refused *after*
// the redirect, not only before. The first server is allowed; it redirects to
// the second, whose address is marked blocked. The fetch must fail with
// ErrBlocked, proving the hop was re-validated.
func TestFetchRefusesRedirectToBlocked(t *testing.T) {
	t.Parallel()

	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secret from the internal service")
	}))
	defer internal.Close()
	internalAddr := addrOf(t, internal)

	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer public.Close()

	// Block only the internal server's address; the public one stays reachable,
	// so the initial request succeeds and only the redirect target is refused.
	f := New(testPolicy(func(a netip.Addr) bool { return a == internalAddr }))

	res, err := f.Get(context.Background(), public.URL)
	if err == nil {
		res.Body.Close()
		t.Fatal("expected the redirect to an internal address to be refused")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestFetchRefusesDirectBlocked(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	addr := addrOf(t, srv)

	f := New(testPolicy(func(a netip.Addr) bool { return a == addr }))
	_, err := f.Get(context.Background(), srv.URL)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestFetchRefusesNonHTTPScheme(t *testing.T) {
	t.Parallel()
	f := New(DefaultPolicy())
	for _, raw := range []string{"file:///etc/passwd", "gopher://host/1", "ftp://host/x"} {
		if _, err := f.Get(context.Background(), raw); !errors.Is(err, ErrScheme) {
			t.Errorf("%s: err = %v, want ErrScheme", raw, err)
		}
	}
}

func TestFetchSurfacesStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	f := New(testPolicy(func(netip.Addr) bool { return false }))
	_, err := f.Get(context.Background(), srv.URL)
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *StatusError", err)
	}
}

func TestFetchCapsRedirects(t *testing.T) {
	t.Parallel()
	// A server that redirects to itself forever.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	p := testPolicy(func(netip.Addr) bool { return false })
	p.MaxRedirects = 3
	f := New(p)
	if _, err := f.Get(context.Background(), srv.URL); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("err = %v, want ErrTooManyRedirects", err)
	}
}

// TestBlockedAddr is the table the SSRF gate hinges on: every internal-reachable
// address is refused, a public one is allowed.
func TestBlockedAddr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		addr    string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.5.4", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"100.64.0.1", true},      // CGNAT
		{"0.0.0.0", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true}, // IPv6 ULA
		{"::ffff:127.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"8.8.8.8", false},
		{"93.184.216.34", false}, // example.com
		{"2606:2800:220:1:248:1893:25c8:1946", false},
	}
	for _, c := range cases {
		got := blockedAddr(netip.MustParseAddr(c.addr))
		if got != c.blocked {
			t.Errorf("blockedAddr(%s) = %v, want %v", c.addr, got, c.blocked)
		}
	}
}

func TestStallReaderCancels(t *testing.T) {
	t.Parallel()
	// A server that sends a header then hangs without sending a body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	p := testPolicy(func(netip.Addr) bool { return false })
	p.StallTimeout = 200 * time.Millisecond
	f := New(p)

	res, err := f.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer res.Body.Close()

	start := time.Now()
	_, err = io.ReadAll(res.Body)
	if err == nil {
		t.Fatal("expected a stall error, got nil")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("stall took %v, watchdog did not fire promptly", elapsed)
	}
}
