package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/aaalzk/Simpleprobe/internal/agent"
)

// APIHandler holds dependencies for the HTTP API.
type APIHandler struct {
	store    *Store
	alerter  *Alerter
	token    string
}

// NewAPIHandler creates a new API handler.
func NewAPIHandler(store *Store, alerter *Alerter, token string) *APIHandler {
	return &APIHandler{store: store, alerter: alerter, token: token}
}

// RegisterRoutes sets up HTTP routes on the given mux.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/report", h.handleReport)
	mux.HandleFunc("/api/servers", h.handleServers)
	mux.HandleFunc("/api/history/", h.handleHistory)
	mux.HandleFunc("/api/alerts", h.handleAlerts)
}

// auth validates the Bearer token from the request.
func (h *APIHandler) auth(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	return parts[1] == h.token
}

// handleReport accepts metrics reports from agents.
func (h *APIHandler) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.auth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	h.alerter.CheckServer(report.Name, m.CPUPercent, m.NetRxRate, m.NetTxRate)

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