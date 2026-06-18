package web

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed static/index.html
var staticFS embed.FS

// Handler returns an http.Handler that serves the dashboard, injecting
// the API token so the browser can authenticate its requests.
func Handler(token string) http.Handler {
	html, _ := staticFS.ReadFile("static/index.html")
	htmlStr := strings.Replace(string(html), "window.API_TOKEN=\"\"", "window.API_TOKEN=\""+token+"\"", 1)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlStr))
	})
}