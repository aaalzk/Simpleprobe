package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aaalzk/Simpleprobe/internal/agent"
	"github.com/aaalzk/Simpleprobe/internal/config"
)

// Alerter monitors server states and sends alerts via Gotify.
type Alerter struct {
	store  *Store
	cfg    config.AlertConfig
	gotify config.GotifyConfig
	client *http.Client
}

// NewAlerter creates a new Alerter.
func NewAlerter(store *Store, cfg config.AlertConfig, gotify config.GotifyConfig) *Alerter {
	return &Alerter{
		store:  store,
		cfg:    cfg,
		gotify: gotify,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Start begins the periodic alert scanning loop. Runs in a goroutine.
func (a *Alerter) Start() {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			a.scanOffline()
		}
	}()
}

// scanOffline checks for servers that have gone offline.
func (a *Alerter) scanOffline() {
	offlineNames, err := a.store.MarkOffline(a.cfg.OfflineSeconds)
	if err != nil {
		log.Printf("ERROR: mark offline: %v", err)
		return
	}
	for _, name := range offlineNames {
		if a.store.CheckAlertCooldown(name, "offline", a.cfg.CooldownSeconds) {
			continue
		}
		msg := fmt.Sprintf("服务器 %s 已离线", name)
		a.sendAlert(name, "offline", msg)
	}
}

// SendRecoveryAlert sends a recovery notification when a previously offline
// server comes back online. Called by the API handler before upserting.
func (a *Alerter) SendRecoveryAlert(name string) {
	// Check cooldown to avoid flooding recovery alerts
	if a.store.CheckAlertCooldown(name, "online", a.cfg.CooldownSeconds) {
		return
	}
	msg := fmt.Sprintf("服务器 %s 已恢复上线", name)
	a.sendAlert(name, "online", msg)
}

// CheckServer checks a single server's metrics for alert conditions.
// Called after each report is received.
func (a *Alerter) CheckServer(name string, cpuPct, netRxRate, netTxRate float64, topCPUProcs []agent.ProcInfo) {
	// Check CPU threshold
	if cpuPct > a.cfg.CPUThreshold {
		if !a.store.CheckAlertCooldown(name, "cpu", a.cfg.CooldownSeconds) {
			msg := fmt.Sprintf("服务器 %s CPU 使用率过高: %.1f%% (阈值: %.0f%%)", name, cpuPct, a.cfg.CPUThreshold)
			if len(topCPUProcs) > 0 {
				msg += "\nCPU 占用 Top 进程:"
				for i, p := range topCPUProcs {
					msg += fmt.Sprintf("\n  %d. %s (PID %d) — %.1f%%", i+1, p.Name, p.PID, p.CPUPercent)
				}
			}
			a.sendAlert(name, "cpu", msg)
		}
	}

	// Check traffic thresholds (bytes/s to Mbps)
	rxMbps := (netRxRate * 8) / 1_000_000
	txMbps := (netTxRate * 8) / 1_000_000

	if rxMbps > a.cfg.TrafficRxMbps {
		if !a.store.CheckAlertCooldown(name, "traffic_rx", a.cfg.CooldownSeconds) {
			msg := fmt.Sprintf("服务器 %s 入站流量异常: %.1f Mbps (阈值: %.0f Mbps)", name, rxMbps, a.cfg.TrafficRxMbps)
			a.sendAlert(name, "traffic_rx", msg)
		}
	}

	if txMbps > a.cfg.TrafficTxMbps {
		if !a.store.CheckAlertCooldown(name, "traffic_tx", a.cfg.CooldownSeconds) {
			msg := fmt.Sprintf("服务器 %s 出站流量异常: %.1f Mbps (阈值: %.0f Mbps)", name, txMbps, a.cfg.TrafficTxMbps)
			a.sendAlert(name, "traffic_tx", msg)
		}
	}
}

// SendSecurityAlert sends a system-level security alert (e.g. brute force attempts).
func (a *Alerter) SendSecurityAlert(alertType, message string) {
	log.Printf("SECURITY ALERT [%s]: %s", alertType, message)

	// Record in database under a special system name
	if err := a.store.InsertAlert("__system__", alertType, message); err != nil {
		log.Printf("ERROR: insert security alert: %v", err)
	}

	// Send to Gotify if configured
	if a.gotify.URL == "" || a.gotify.Token == "" {
		return
	}

	payload := map[string]interface{}{
		"title":    fmt.Sprintf("[SECURITY] %s", alertType),
		"message":  message,
		"priority": 8, // higher priority for security alerts
		"extras":   map[string]interface{}{},
	}
	body, _ := json.Marshal(payload)

	url := strings.TrimRight(a.gotify.URL, "/") + "/message"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("ERROR: gotify security alert request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", a.gotify.Token)

	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("ERROR: gotify security alert push: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Printf("ERROR: gotify security alert returned %d, body: %s", resp.StatusCode, string(respBody))
	}
}

// sendAlert sends an alert via Gotify and records it in the database.
func (a *Alerter) sendAlert(serverName, alertType, message string) {
	log.Printf("ALERT [%s] %s: %s", alertType, serverName, message)

	// Record in database
	if err := a.store.InsertAlert(serverName, alertType, message); err != nil {
		log.Printf("ERROR: insert alert: %v", err)
	}
	if err := a.store.SetAlertCooldown(serverName, alertType); err != nil {
		log.Printf("ERROR: set cooldown: %v", err)
	}

	// Send to Gotify if configured
	if a.gotify.URL == "" || a.gotify.Token == "" {
		return
	}

	payload := map[string]interface{}{
		"title":    fmt.Sprintf("[%s] %s", alertType, serverName),
		"message":  message,
		"priority": 5,
		"extras":   map[string]interface{}{},
	}
	body, _ := json.Marshal(payload)

	url := strings.TrimRight(a.gotify.URL, "/") + "/message"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("ERROR: gotify request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", a.gotify.Token)

	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("ERROR: gotify push: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Printf("ERROR: gotify returned %d, body: %s", resp.StatusCode, string(respBody))
	}
}