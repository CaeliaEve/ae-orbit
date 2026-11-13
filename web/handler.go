// Package web embeds the same-origin browser client.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var files embed.FS

func Handler() http.Handler {
	root, _ := fs.Sub(files, "static")
	server := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "style.css" && name != "compact.css" &&
			name != "assets/nei/manifest.json" &&
			name != "assets/brand/ae-orbit-mark.png" &&
			name != "assets/brand/ae-orbit-mark-dark.png" &&
			name != "assets/brand/favicon.png" &&
			name != "assets/brand/ae-orbit-mark.svg" &&
			!(strings.HasPrefix(name, "assets/nei/") && strings.HasSuffix(name, ".png")) {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(root, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-cache")
		if strings.HasSuffix(name, ".png") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		server.ServeHTTP(w, r)
	})
}
