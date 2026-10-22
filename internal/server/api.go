package server

import (
	"bytes"
	"compress/gzip"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/aaalzk/Simpleprobe/internal/agent"
	"github.com/aaalzk/Simpleprobe/internal/config"
)

// APIHandler holds dependencies for the HTTP API.
type APIHandler struct {
	store       *Store
	alerter     *Alerter
	token       string
	rateLimiter *RateLimiter
	gaze        *GazeTracker
	sites       []config.SiteConfig
	hub         *Hub
}

// NewAPIHandler creates a new API handler.
func NewAPIHandler(store *Store, alerter *Alerter, token string, rl *RateLimiter, gaze *GazeTracker, sites []config.SiteConfig) *APIHandler {
	return &APIHandler{store: store, alerter: alerter, token: token, rateLimiter: rl, gaze: gaze, sites: sites, hub: NewHub()}
}

// RegisterRoutes sets up HTTP routes on the given mux. All API routes
// require Bearer token authentication.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/report", h.authMiddleware(h.handleReport))
	mux.HandleFunc("/api/servers", h.authMiddleware(h.handleServers))
	mux.HandleFunc("/api/history/", h.authMiddleware(h.handleHistory))
	mux.HandleFunc("/api/alerts", h.authMiddleware(h.handleAlerts))
	mux.HandleFunc("/api/sites", h.authMiddleware(h.handleSites))
	mux.HandleFunc("/api/sites/", h.authMiddleware(h.handleSiteHistory))
	mux.HandleFunc("/api/stream", h.authMiddleware(h.HandleStream))
}

// extractIP determines the client IP used for rate limiting and security
// alerts.
//
// Trust order, most trustworthy first:
//
//  1. CF-Connecting-IP — written by Cloudflare's edge and holding the real
//     client IP. Cloudflare overwrites it on every proxied request.
//  2. X-Forwarded-For — the LAST entry is the one appended by the closest
//     trusted proxy (our own Nginx), so it is the address that proxy saw.
//     The first entry is controlled by the client and must never be
//     trusted: taking it lets an attacker spoof an IP and walk straight
//     through the brute-force rate limiter.
//  3. RemoteAddr — the direct peer address.
//
// Headers 1 and 2 are only trustworthy while the origin is reachable
// exclusively through Cloudflare (firewall the origin to Cloudflare's IP
// ranges, or use Cloudflare Tunnel); otherwise a client can forge them.
func extractIP(r *http.Request) string {
	if ip := normalizeIP(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := normalizeIP(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// normalizeIP trims and validates an IP address literal. It returns an
// empty string when the value is not a valid address.
func normalizeIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return ""
}

// auth validates the Bearer token using constant-time comparison.
func (h *APIHandler) auth(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(h.token)) == 1
}

// authMiddleware wraps a handler with authentication, rate limiting, and
// brute-force detection logging.
func (h *APIHandler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// API responses are live state; they must never be stored by a
		// browser or by an intermediary cache.
		w.Header().Set("Cache-Control", "no-store")

		ip := extractIP(r)

		// Check if this IP is currently blocked
		if !h.rateLimiter.Allow(ip) {
			log.Printf("AUTH BLOCKED: ip=%s — still in cooldown", ip)
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}

		if !h.auth(r) {
			// Record the failure; check if threshold was just crossed
			justBlocked := h.rateLimiter.RecordFailure(ip)
			log.Printf("AUTH FAILED: ip=%s user-agent=%s", ip, r.UserAgent())

			if justBlocked {
				log.Printf("AUTH BRUTE-FORCE: ip=%s exceeded failure threshold", ip)
				h.alerter.SendSecurityAlert("brute_force",
					"检测到暴力破解尝试 — IP: "+ip+" 短时间内多次认证失败，已被临时封禁")
			}

			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

// gzipThreshold is the minimum response size (in bytes) that is worth
// compressing. Below it the gzip header and trailer cost more than they
// save.
const gzipThreshold = 1024

// writeJSON writes v as a JSON response.
//
// Two things every API response needs when it sits behind a CDN:
//
//   - Cache-Control: no-store. The payload is live state; should a cache
//     rule ever cache it, the dashboard would show stale data (Cloudflare's
//     default edge TTL for a 200 response is 120 minutes).
//   - gzip for larger payloads. A 24 hour history query easily reaches
//     megabytes of JSON; compressing it at the origin keeps the
//     origin→edge transfer small.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	if len(body) >= gzipThreshold && acceptsGzip(r) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write(body); err == nil {
			if err := gz.Close(); err == nil {
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Vary", "Accept-Encoding")
				w.WriteHeader(status)
				_, _ = w.Write(buf.Bytes())
				return
			}
		}
		// Compression failed: fall through and serve the plain body.
	}

	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// acceptsGzip reports whether the client advertised gzip support.
func acceptsGzip(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip")
}

// handleReport accepts metrics reports from agents.
func (h *APIHandler) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var report agent.Report
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if report.Name == "" {
		http.Error(w, "server name is required", http.StatusBadRequest)
		return
	}

	m := report.Metrics

	// Check if server was previously offline (recovery detection)
	prevServer, _ := h.store.GetServerByName(report.Name)
	wasOffline := prevServer != nil && prevServer.Status == "offline"

	// Upsert server state
	if err := h.store.UpsertServer(
		report.Name,
		m.CPUPercent, m.MemPercent, m.MemTotal, m.MemUsed,
		m.DiskPercent, m.DiskTotal, m.DiskUsed,
		m.NetRxRate, m.NetTxRate, m.NetRxBytes, m.NetTxBytes,
		m.Load1, m.Load5, m.Load15,
		m.Uptime, m.TCPConns, m.ProcessCount,
		m.OSName, m.KernelVersion,
	); err != nil {
		log.Printf("ERROR: upsert server %s: %v", report.Name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Insert historical report
	if err := h.store.InsertReport(
		report.Name,
		report.Timestamp,
		m.CPUPercent, m.MemPercent, m.DiskPercent,
		m.NetRxRate, m.NetTxRate,
		m.Load1, m.Load5, m.Load15,
	); err != nil {
		log.Printf("ERROR: insert report for %s: %v", report.Name, err)
		// Non-fatal: we already updated the server state
	}

	// Push the new state to every connected dashboard (SSE). The hub
	// coalesces bursts, so a batch of reports results in one update.
	h.hub.Notify()

	// Check recovery alert (runs on every report; a recovery is only announced
	// once the server has reported online for `online` consecutive reports).
	h.alerter.CheckOnline(report.Name, wasOffline)

	// Check alerts immediately
	h.alerter.CheckServer(report.Name, m.CPUPercent, m.NetRxRate, m.NetTxRate, m.TopCPUProcs)

	writeJSON(w, r, http.StatusOK, struct {
		Status string `json:"status"`
		Gaze   bool   `json:"gaze"`
	}{
		Status: "ok",
		Gaze:   h.gaze.IsActive(),
	})
}

// handleServers returns all servers' current state.
func (h *APIHandler) handleServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Record gaze signal — someone is actively viewing the dashboard
	h.gaze.Ping()

	servers, err := h.store.GetAllServers()
	if err != nil {
		log.Printf("ERROR: get servers: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if servers == nil {
		servers = []ServerRecord{}
	}

	resp := struct {
		Servers []ServerRecord `json:"servers"`
		Gaze    bool           `json:"gaze"`
	}{
		Servers: servers,
		Gaze:    h.gaze.IsActive(),
	}

	writeJSON(w, r, http.StatusOK, resp)
}

// handleHistory returns historical report data for a server.
// URL: /api/history/{name}?hours=24
func (h *APIHandler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/history/")
	if name == "" {
		http.Error(w, "server name required", http.StatusBadRequest)
		return
	}

	hours := 24
	if hStr := r.URL.Query().Get("hours"); hStr != "" {
		if h, err := parseInt(hStr); err == nil && h > 0 && h <= 168 {
			hours = h
		}
	}

	reports, err := h.store.GetHistory(name, hours)
	if err != nil {
		log.Printf("ERROR: get history for %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if reports == nil {
		reports = []ReportRecord{}
	}

	writeJSON(w, r, http.StatusOK, reports)
}

// handleAlerts returns recent alerts.
func (h *APIHandler) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	alerts, err := h.store.GetRecentAlerts(50)
	if err != nil {
		log.Printf("ERROR: get alerts: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if alerts == nil {
		alerts = []AlertRecord{}
	}

	writeJSON(w, r, http.StatusOK, alerts)
}

// handleSites returns the current status of every configured site together
// with 24-hour uptime statistics. Sites that are configured but never probed
// yet are included with status "unknown".
func (h *APIHandler) handleSites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.gaze.Ping()

	states, err := h.store.GetAllSiteStates()
	if err != nil {
		log.Printf("ERROR: get site states: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	byName := make(map[string]SiteRecord, len(states))
	for _, st := range states {
		byName[st.Name] = st
	}

	sites := make([]SiteRecord, 0, len(h.sites))
	for _, cfg := range h.sites {
		if st, ok := byName[cfg.Name]; ok {
			sites = append(sites, st)
			continue
		}
		// Configured but not yet probed.
		sites = append(sites, SiteRecord{
			Name:       cfg.Name,
			URL:        cfg.URL,
			Status:     "unknown",
			StatusCode: 0,
			LatencyMs:  -1,
			Uptime24h:  -1,
		})
	}

	resp := struct {
		Sites []SiteRecord `json:"sites"`
		Gaze  bool         `json:"gaze"`
	}{Sites: sites, Gaze: h.gaze.IsActive()}

	writeJSON(w, r, http.StatusOK, resp)
}

// handleSiteHistory returns probe history for one configured site.
// URL: /api/sites/{name}/history?hours=24
func (h *APIHandler) handleSiteHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/sites/")
	name = strings.TrimSuffix(name, "/history")
	if name == "" || strings.Contains(name, "/") {
		http.Error(w, "site name required", http.StatusBadRequest)
		return
	}

	hours := 24
	if hStr := r.URL.Query().Get("hours"); hStr != "" {
		if h, err := parseInt(hStr); err == nil && h > 0 && h <= 168 {
			hours = h
		}
	}

	checks, err := h.store.GetSiteHistory(name, hours)
	if err != nil {
		log.Printf("ERROR: get site history %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if checks == nil {
		checks = []SiteCheckRecord{}
	}

	writeJSON(w, r, http.StatusOK, checks)
}

func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
