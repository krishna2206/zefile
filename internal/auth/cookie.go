package auth

import (
	"net/http"
	"time"
)

// SessionCookieName is the cookie carrying the session token in a browser.
//
// The __Host- prefix is deliberately not used: it requires Secure, forbids a
// Domain attribute and pins Path to "/", which would make Zefile impossible to
// run over plain HTTP on a local network during setup. Security comes from the
// attributes below, which are set on every cookie this package issues.
const SessionCookieName = "zefile_session"

// SessionCookie builds the cookie for a freshly created session.
//
// The token never reaches JavaScript. Storing it in localStorage — as File
// Browser did — means any script that runs on the page can read it, so a single
// cross-site scripting flaw becomes full account takeover. HttpOnly removes
// that path entirely, which is also why user content is served from a separate
// origin: no cookie exists there to steal.
//
// secure is a parameter rather than a hard true because a self-hosted instance
// is often reached over plain HTTP on a local network before TLS is set up, and
// a Secure cookie would simply be discarded there — leaving an unexplained
// inability to sign in. Callers pass false only when serving without TLS.
// TestCookieAttributes asserts the properties a static analyser cannot prove.
func SessionCookie(token string, expires time.Time, secure bool) *http.Cookie {
	return &http.Cookie{ // #nosec G124 -- Secure comes from configuration; see the doc comment above
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   secure,
		// Lax rather than Strict: Strict would drop the cookie when someone
		// follows a link into Zefile from anywhere else, presenting a signed-in
		// user with a login form. Lax still withholds it from cross-site
		// form posts, which is the case that matters.
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearSessionCookie builds the cookie that removes the session from a browser.
//
// The attributes must match those used when setting it, or the browser treats
// it as a different cookie and quietly keeps the original. Clearing the browser
// copy is a courtesy in any case: the server-side row is what actually ends the
// session, and it is revoked independently.
func ClearSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{ // #nosec G124 -- Secure comes from configuration; see SessionCookie
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
