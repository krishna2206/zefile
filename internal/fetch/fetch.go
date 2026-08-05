// Package fetch downloads a user-supplied URL from the server's own network,
// with the server-side request forgery defences §9.2 of the design requires.
//
// The central risk is that a URL lets a caller aim the server at its own
// network — internal services, sibling containers, and above all the cloud
// metadata endpoint that hands out credentials. The defence is not to resolve
// the name ourselves and then trust the HTTP client to connect to the same
// address: that is a time-of-check/time-of-use gap a rebinding DNS record walks
// straight through. Instead a dialer Control hook validates the *actual* IP the
// socket is about to connect to, for every connection the transport makes —
// including each redirect hop — so a redirect that resolves to an internal
// address is refused at connect time, on the address really being dialed.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Sentinel errors. The job layer records their message; the API layer maps the
// scheme error to a 400 before any work is queued.
var (
	// ErrScheme means the URL used a scheme other than http or https.
	ErrScheme = errors.New("fetch: only http and https URLs are allowed")

	// ErrBlocked means the URL resolved to an address that is not a public,
	// globally-routable one — loopback, private, link-local and the like.
	ErrBlocked = errors.New("fetch: the URL resolves to a non-public address")

	// ErrTooManyRedirects means the server redirected past the allowed count.
	ErrTooManyRedirects = errors.New("fetch: too many redirects")
)

// StatusError means the source answered with a non-2xx status. It carries the
// code so the interface can say "the source responded 403 Forbidden" — which is
// how a URL that needs authentication we do not have surfaces.
type StatusError struct{ Status string }

func (e *StatusError) Error() string { return "fetch: source responded " + e.Status }

// Policy tunes the fetcher. The zero value is not useful; use DefaultPolicy.
type Policy struct {
	// Blocked reports whether a resolved IP must not be dialed. It is the whole
	// SSRF gate, injected so tests can allow the loopback their servers run on
	// while still exercising the redirect-revalidation path.
	Blocked func(netip.Addr) bool

	// MaxRedirects caps redirect hops before the fetch is abandoned.
	MaxRedirects int

	// ConnectTimeout bounds establishing one TCP connection.
	ConnectTimeout time.Duration

	// ResponseHeaderTimeout bounds waiting for the response headers after the
	// request is written — the time to first byte.
	ResponseHeaderTimeout time.Duration

	// StallTimeout abandons the download if no data arrives within it. There is
	// deliberately no total deadline: a genuine forty-gigabyte transfer over a
	// slow link is the point of the feature, and a total timeout would sever it.
	// A stall timeout catches a wedged connection without punishing a slow one.
	StallTimeout time.Duration
}

// DefaultPolicy is the production configuration: block every non-public
// address, ten redirects, and unhurried but bounded timeouts.
func DefaultPolicy() Policy {
	return Policy{
		Blocked:               blockedAddr,
		MaxRedirects:          10,
		ConnectTimeout:        15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		StallTimeout:          60 * time.Second,
	}
}

// Result is an open download. The caller must Close Body, which also stops the
// stall watchdog and releases the connection.
type Result struct {
	Body     io.ReadCloser
	Filename string // best-effort suggestion; empty if none could be derived
	Size     int64  // the full file size, or -1 if the source did not declare one

	// Resumed reports that a ranged request was honoured with a 206, so Body
	// begins at the requested offset. When false after a ranged request, the
	// source ignored the range and Body is the whole file from the start — the
	// caller must reset any partial it had.
	Resumed bool
}

// Fetcher performs SSRF-guarded downloads.
type Fetcher struct {
	policy Policy
	client *http.Client
}

// New builds a Fetcher for the given policy.
func New(p Policy) *Fetcher {
	dialer := &net.Dialer{
		Timeout: p.ConnectTimeout,
		// Control runs after DNS resolution and before connect, with the
		// concrete address the socket will use. Refusing here refuses the real
		// destination, whatever the name resolved to, and it runs for every
		// dial the transport makes — so redirects are covered by construction.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("%w: unparseable address %q", ErrBlocked, host)
			}
			if p.Blocked(ip.Unmap()) {
				return ErrBlocked
			}
			return nil
		},
	}

	transport := &http.Transport{
		DialContext: dialer.DialContext,
		// No proxy: a proxy would connect on our behalf, moving the dial — and
		// thus the address validation — out of our control.
		Proxy:                 nil,
		ResponseHeaderTimeout: p.ResponseHeaderTimeout,
		ForceAttemptHTTP2:     true,
	}

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= p.MaxRedirects {
				return ErrTooManyRedirects
			}
			// The Control hook already re-validates the address of every hop;
			// this rejects a redirect to a non-http scheme, which the transport
			// would otherwise fail with a murkier error.
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return ErrScheme
			}
			return nil
		},
	}

	return &Fetcher{policy: p, client: client}
}

// Get opens rawURL for reading, resuming from offset if it is greater than zero
// by asking for that byte range. The returned Body streams the response through
// a stall watchdog; the caller owns it and must Close it.
//
// When offset > 0, inspect Result.Resumed: true means the source honoured the
// range and Body starts at offset; false means it sent the whole file and the
// caller must discard whatever it had staged.
func (f *Fetcher) Get(ctx context.Context, rawURL string, offset int64) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrScheme
	}

	// A cancellable context distinct from the caller's: the stall watchdog
	// cancels it, and Close cancels it, without disturbing the caller's ctx.
	reqCtx, cancel := context.WithCancel(ctx)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("fetch: %w", err)
	}
	req.Header.Set("User-Agent", "zefile")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := f.client.Do(req)
	if err != nil {
		cancel()
		return nil, unwrapRedirectError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		cancel()
		return nil, &StatusError{Status: resp.Status}
	}

	resumed := resp.StatusCode == http.StatusPartialContent
	body := newStallReader(resp.Body, f.policy.StallTimeout, cancel)
	return &Result{
		Body:     body,
		Filename: filenameFrom(u, resp.Header.Get("Content-Disposition")),
		Size:     totalSize(resp, resumed),
		Resumed:  resumed,
	}, nil
}

// totalSize reports the full file size regardless of ranging. For a 206 the
// body's Content-Length is only the remainder, so the total comes from the
// Content-Range header's final field; for a plain 200 it is the Content-Length.
func totalSize(resp *http.Response, resumed bool) int64 {
	if resumed {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 && i+1 < len(cr) {
				if total, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					return total
				}
			}
		}
		return -1 // ranged but no parseable total: size unknown
	}
	return resp.ContentLength
}

// unwrapRedirectError surfaces our own redirect-time errors, which the client
// wraps in a *url.Error, so callers can compare them with errors.Is.
func unwrapRedirectError(err error) error {
	if errors.Is(err, ErrBlocked) || errors.Is(err, ErrScheme) || errors.Is(err, ErrTooManyRedirects) {
		return errors.Unwrap(err) // the *url.Error wrapper adds noise; the cause is what matters
	}
	return fmt.Errorf("fetch: %w", err)
}

// filenameFrom derives a download name: the Content-Disposition filename if the
// source offered one, otherwise the last path segment of the URL. Either may be
// empty or unsafe; the storage layer validates it before it becomes a path.
func filenameFrom(u *url.URL, disposition string) string {
	if disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			if name := params["filename"]; name != "" {
				return path.Base(name)
			}
		}
	}
	if base := path.Base(u.Path); base != "." && base != "/" && base != "" {
		return base
	}
	return ""
}

// blockedAddr is the production gate: allow only public, globally-routable
// unicast. Everything an SSRF payload wants to reach — loopback, RFC 1918 and
// ULA private ranges, link-local (including the 169.254.169.254 metadata
// endpoint), carrier-grade NAT, multicast and the unspecified address — is
// refused. The address is unmapped first so an IPv4-in-IPv6 form cannot smuggle
// a blocked v4 address past the v4 checks.
func blockedAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() {
		return true
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range reservedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// reservedPrefixes are ranges not covered by the netip predicates above but
// still not a safe public destination.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT
	netip.MustParsePrefix("::ffff:0:0/96"),  // IPv4-mapped, in case an unmap was skipped upstream
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use IPv4/IPv6 translation
}
