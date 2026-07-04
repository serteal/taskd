//go:build webui

// Package webui embeds the built web frontend into taskd so one binary
// serves UI and API from the same origin (no CORS, no second server).
// Build the bundle first (`make web`), then compile with -tags webui.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the bundle with an SPA fallback: unknown paths get
// index.html so client-side view URLs survive a reload.
func Handler() http.Handler {
	root, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // embed shape is fixed at compile time
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(root, p); err != nil {
				r.URL.Path = "/" // SPA fallback
			}
		}
		files.ServeHTTP(w, r)
	})
}
