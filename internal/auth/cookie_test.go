package auth

import (
	"net/http"
	"testing"
	"time"
)

// TestCookieAttributes pins the properties a static analyser cannot prove,
// because Secure is set from configuration rather than written as a literal.
func TestCookieAttributes(t *testing.T) {
	t.Parallel()

	for _, secure := range []bool{true, false} {
		c := SessionCookie("zefile_sess_abc", time.Now().Add(time.Hour), secure)

		if !c.HttpOnly {
			t.Error("HttpOnly is not set; a script on the page could read the session")
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("SameSite = %v, want Lax", c.SameSite)
		}
		if c.Secure != secure {
			t.Errorf("Secure = %v, want %v", c.Secure, secure)
		}
		if c.Path != "/" {
			t.Errorf("Path = %q, want %q", c.Path, "/")
		}
		if c.Name != SessionCookieName {
			t.Errorf("Name = %q, want %q", c.Name, SessionCookieName)
		}
	}
}

// TestClearCookieMatchesSetCookie guards a classic bug: a browser treats a
// cookie with different attributes as a different cookie, so a mismatched
// clearing cookie leaves the original in place.
func TestClearCookieMatchesSetCookie(t *testing.T) {
	t.Parallel()

	for _, secure := range []bool{true, false} {
		set := SessionCookie("zefile_sess_abc", time.Now().Add(time.Hour), secure)
		clear := ClearSessionCookie(secure)

		if clear.Name != set.Name {
			t.Errorf("Name %q does not match %q", clear.Name, set.Name)
		}
		if clear.Path != set.Path {
			t.Errorf("Path %q does not match %q", clear.Path, set.Path)
		}
		if clear.Secure != set.Secure || clear.HttpOnly != set.HttpOnly || clear.SameSite != set.SameSite {
			t.Error("attributes differ between setting and clearing")
		}
		if clear.Value != "" {
			t.Errorf("Value = %q, want empty", clear.Value)
		}
		if clear.MaxAge >= 0 {
			t.Errorf("MaxAge = %d, want negative so the cookie is deleted", clear.MaxAge)
		}
	}
}
