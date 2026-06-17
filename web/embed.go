package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"
)

//go:embed static
var staticFS embed.FS

// mimeTypes maps file extensions to Content-Type values.
// Explicitly set to avoid relying on OS mime database in embedded FS.
var mimeTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
}

// Handler returns an http.Handler that serves the embedded static files
// with correct Content-Type headers.
func Handler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set correct Content-Type based on file extension
		ext := filepath.Ext(r.URL.Path)
		if ct, ok := mimeTypes[ext]; ok {
			w.Header().Set("Content-Type", ct)
		}

		// Serve index.html for root path
		if r.URL.Path == "/" || r.URL.Path == "" {
			r.URL.Path = "/index.html"
			// Re-set content type for index.html
			w.Header().Set("Content-Type", mimeTypes[".html"])
		}

		fileServer.ServeHTTP(w, r)
	})
}