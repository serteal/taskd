//go:build !webui

// Package webui embeds the built web frontend into taskd. This stub serves
// a pointer instead when the binary was built without the webui tag.
package webui

import (
	"fmt"
	"net/http"
)

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>taskd</title><body style="font-family:system-ui;margin:4rem auto;max-width:36rem;line-height:1.6">
<h1>taskd is running</h1>
<p>The API is live on this port. This build does not include the web UI —
build it with <code>make build-web</code> and restart, or use the
<code>task</code> CLI.</p></body>`)
	})
}
