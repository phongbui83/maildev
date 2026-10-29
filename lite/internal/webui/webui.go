// Package webui serves the statically exported Next.js frontend embedded in
// the binary (web/out copied to internal/webui/dist at build time).
package webui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the UI. Resolution order for a request path p:
// an exact file, p + ".html", /projects/<anything>/<rest> mapped onto the
// pre-rendered "default" project shell (pages read the real slug from the
// URL at runtime), /guide -> guide/index.html, and finally 404.html.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	exists := func(name string) bool {
		f, err := sub.Open(name)
		if err != nil {
			return false
		}
		st, err := f.Stat()
		f.Close()
		return err == nil && !st.IsDir()
	}
	resolve := func(p string) (string, bool) {
		p = strings.TrimPrefix(path.Clean("/"+p), "/")
		if p == "" {
			p = "index.html"
		}
		if exists(p) {
			return p, true
		}
		if exists(p + ".html") {
			return p + ".html", true
		}
		if p == "guide" {
			return "guide/index.html", exists("guide/index.html")
		}
		if strings.HasPrefix(p, "projects/") {
			rest := strings.TrimPrefix(p, "projects/")
			if i := strings.Index(rest, "/"); i >= 0 {
				rest = rest[i:]
			} else {
				rest = ""
			}
			cand := "projects/default" + rest + ".html"
			if exists(cand) {
				return cand, true
			}
		}
		return "", false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name, ok := resolve(r.URL.Path)
		status := http.StatusOK
		if !ok {
			if !exists("404.html") {
				http.NotFound(w, r)
				return
			}
			name, status = "404.html", http.StatusNotFound
		}
		f, err := sub.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		rs, ok := f.(io.ReadSeeker)
		if !ok {
			http.Error(w, "unreadable asset", http.StatusInternalServerError)
			return
		}
		if strings.HasPrefix(name, "_next/static/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			io.Copy(w, rs)
			return
		}
		// ServeContent (not FileServer: that redirects index.html to "/") picks the
		// content type from the extension and handles ranges and conditionals.
		http.ServeContent(w, r, name, st.ModTime(), rs)
	})
}
