package web

import (
	"embed"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed static/*
var staticFS embed.FS

// mimeTypes maps file extensions to Content-Type values.
// Explicitly set to avoid relying on the OS mime database.
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent directory listing
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, "/") {
			r.URL.Path = "/index.html"
		}

		path := "static" + r.URL.Path
		data, err := staticFS.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		ext := filepath.Ext(r.URL.Path)
		contentType := mimeTypes[ext]
		if contentType == "" {
			contentType = mime.TypeByExtension(ext)
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	})
}