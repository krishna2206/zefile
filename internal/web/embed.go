// Package web serves the built single-page interface from inside the binary.
//
// The assets are compiled into the executable, so deploying Zefile stays a
// matter of copying one file. Nothing here is generated at run time: the whole
// interface is produced by the frontend build and embedded as it stands.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist holds the built interface.
//
// The all: prefix is required: without it embed skips files whose names begin
// with a dot, and the placeholder that keeps this directory in version control
// is exactly such a file. Its presence is what lets a contributor working on
// the Go side build without installing Node.
//
//go:embed all:dist
var dist embed.FS

// Assets returns the built interface, or false when the binary was compiled
// without one.
func Assets() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		// A backend-only build. The API still works; only the interface is
		// absent, and saying so plainly beats serving a blank page.
		return nil, false
	}
	return sub, true
}

// Handler serves the interface, falling back to index.html for unknown paths.
//
// That fallback is what makes client-side routing work: a browser asked to
// open /jeux/steam directly must receive the application, which then reads the
// path itself. It applies only to paths the API does not claim, since the API
// is routed first.
func Handler() (http.Handler, bool) {
	assets, ok := Assets()
	if !ok {
		return nil, false
	}
	files := http.FileServerFS(assets)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}

		if _, err := fs.Stat(assets, name); err != nil {
			// Hashed asset names are content-addressed and safe to cache
			// forever; index.html never is, or an upgrade would leave browsers
			// running the previous interface against the new API.
			w.Header().Set("Cache-Control", "no-cache")
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			files.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	return handler, true
}
