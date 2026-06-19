package server

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/aaalzk/Simpleprobe/internal/agent"
)

// APIHandler holds dependencies for the HTTP API.
type APIHandler struct {
	store       *Store
	alerter     *Alerter
	token       string
	rateLimiter *RateLimiter
}

// NewAPIHandler creates a new API handler.
func NewAPIHandler(store *Store, alerter *Alerter, token string, rl *RateLimiter) *APIHandler {
	return &APIHandler{store: store, alerter: alerter, token: token, rateLimiter: rl}
}

// RegisterRoutes sets up HTTP routes on the given mux. All API routes
// require Bearer token authentication.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/report", h.authMiddleware(h.handleReport))
	mux.HandleFunc("/api/servers", h.authMiddleware(h.handleServers))
	mux.HandleFunc("/api/history/", h.authMiddleware(h.handleHistory))
	mux.HandleFunc("/api/alerts", h.authMiddleware(h.handleAlerts))
}

// extractIP extracts the client IP from the request, respecting X-Forwarded-For.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip := strings.SplitN(xff, ",", 2)[0]
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
		m.CPUPercent, m.MemPercent, m.DiskPercent,
		m.NetRxRate, m.NetTxRate,
		m.Load1, m.Load5, m.Load15,
	); err != nil {
		log.Printf("ERROR: insert report for %s: %v", report.Name, err)
		// Non-fatal: we already updated the server state
	}

	// Check recovery alert
	if wasOffline {
		h.alerter.SendRecoveryAlert(report.Name)
	}

	// Check alerts immediately
	h.alerter.CheckServer(report.Name, m.CPUPercent, m.NetRxRate, m.NetTxRate, m.TopCPUProcs)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleServers returns all servers' current state.
func (h *APIHandler) handleServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	servers, err := h.store.GetAllServers()
	if err != nil {
		log.Printf("ERROR: get servers: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if servers == nil {
		servers = []ServerRecord{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(servers)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reports)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alerts)
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