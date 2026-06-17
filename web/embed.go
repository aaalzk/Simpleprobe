package web

import (
	"embed"
	"net/http"
)

//go:embed static/index.html
var staticFS embed.FS

// Handler returns an http.Handler that serves the single-page dashboard.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := staticFS.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}