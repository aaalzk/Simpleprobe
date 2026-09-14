package server

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/aaalzk/Simpleprobe/internal/config"
)

// SiteMonitor periodically probes configured sites over HTTP(S) and records
// reachability history. It is the equivalent of:
//
//	curl -L -o /dev/null -s -w "%{http_code} %{redirect_url}\n" https://example.com
//
// A site is considered "up" when the final response (after redirects) has an
// HTTP status code in [200, 400). Network errors and timeouts count as "down".
type SiteMonitor struct {
	store     *Store
	alerter   *Alerter
	sites     []config.SiteConfig
	alertCfg  config.AlertConfig
	transport *http.Transport
	stopCh    chan struct{}
}

// NewSiteMonitor creates a site monitor. alertCfg is the full alert config
// (provides both cooldown and per-type tolerance).
func NewSiteMonitor(store *Store, alerter *Alerter, sites []config.SiteConfig, alertCfg config.AlertConfig) *SiteMonitor {
	return &SiteMonitor{
		store:    store,
		alerter:  alerter,
		sites:    sites,
		alertCfg: alertCfg,
		transport: &http.Transport{
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		},
		stopCh: make(chan struct{}),
	}
}

// Start launches one goroutine per configured site. Each site probes on its
// own cadence (default 60s). It returns immediately.
func (m *SiteMonitor) Start() {
	if len(m.sites) == 0 {
		log.Printf("SiteMonitor: no sites configured, disabled")
		return
	}
	for _, site := range m.sites {
		go m.loop(site)
	}
	log.Printf("SiteMonitor: monitoring %d site(s)", len(m.sites))
}

// Stop shuts down all probe loops.
func (m *SiteMonitor) Stop() {
	close(m.stopCh)
}

// loop runs the probe ticker for a single site.
func (m *SiteMonitor) loop(site config.SiteConfig) {
	interval := site.Interval
	if interval <= 0 {
		interval = 60
	}
	timeout := site.Timeout
	if timeout <= 0 {
		timeout = 10
	}

	// Probe immediately, then on the fixed cadence.
	m.probe(site, timeout)

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.probe(site, timeout)
		case <-m.stopCh:
			return
		}
	}
}

// probe performs one HTTP check for the site, stores the result, and raises
// alerts on up<->down transitions (respecting the alert cooldown).
func (m *SiteMonitor) probe(site config.SiteConfig, timeout int) {
	// Read previous state BEFORE probing/writing so transitions can be detected.
	prev, err := m.store.GetSiteState(site.Name)
	if err != nil {
		log.Printf("ERROR: get site state %s: %v", site.Name, err)
	}

	client := &http.Client{
		Timeout:   time.Duration(timeout) * time.Second,
		Transport: m.transport,
		// Default redirect policy: follow up to 10 redirects, record final code.
	}

	start := time.Now()
	resp, err := client.Get(site.URL)
	latencyMs := time.Since(start).Seconds() * 1000

	status := "down"
	statusCode := 0
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		log.Printf("SiteMonitor: %s (%s) DOWN: %v", site.Name, site.URL, err)
	} else {
		defer resp.Body.Close()
		// Drain a small amount so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		statusCode = resp.StatusCode
		if statusCode >= 200 && statusCode < 400 {
			status = "up"
		} else {
			log.Printf("SiteMonitor: %s (%s) DOWN: HTTP %d", site.Name, site.URL, statusCode)
		}
	}

	// Persist state + history (best-effort; probe failures hit the log above).
	if err := m.store.UpsertSiteState(site.Name, site.URL, status, statusCode, latencyMs, errMsg); err != nil {
		log.Printf("ERROR: upsert site state %s: %v", site.Name, err)
	}
	if err := m.store.InsertSiteCheck(site.Name, site.URL, status, statusCode, latencyMs, errMsg); err != nil {
		log.Printf("ERROR: insert site check %s: %v", site.Name, err)
	}

	m.alertOnProbe(site.Name, site.URL, prev, status, statusCode, latencyMs, errMsg)
}

// alertOnProbe evaluates one probe result against the per-type tolerance and
// cooldown, and raises site_down / site_up alerts. Every probe counts, so a
// tolerance of 3 means "3 consecutive probes with the same conclusion"; a probe
// that contradicts a counter restarts it.
func (m *SiteMonitor) alertOnProbe(name, url string, prev *SiteRecord, status string, statusCode int, latencyMs float64, errMsg string) {
	alertType := "site_down"
	opposite := "site_up"
	if status == "up" {
		alertType = "site_up"
		opposite = "site_down"
	}

	if prev == nil {
		// First probe ever only establishes the baseline; never alert on it.
		if err := m.store.ResetAlertTolerance(name, alertType); err != nil {
			log.Printf("ERROR: reset tolerance %s %s: %v", alertType, name, err)
		}
		if err := m.store.ResetAlertTolerance(name, opposite); err != nil {
			log.Printf("ERROR: reset tolerance %s %s: %v", opposite, name, err)
		}
		return
	}

	count, err := m.store.IncrementAlertTolerance(name, alertType)
	if err != nil {
		log.Printf("ERROR: increment tolerance %s %s: %v", alertType, name, err)
		return
	}
	// This probe contradicts the opposite conclusion: restart its counter.
	if prev.Status != status {
		if err := m.store.ResetAlertTolerance(name, opposite); err != nil {
			log.Printf("ERROR: reset tolerance %s %s: %v", opposite, name, err)
		}
	}

	// Alert exactly once per confirmed state, i.e. when the counter first
	// reaches the tolerance; it keeps rising while the state holds.
	if count != m.alertCfg.GetTolerance(alertType) {
		return
	}
	if m.store.CheckAlertCooldown(name, alertType, m.alertCfg.CooldownSeconds) {
		return
	}

	statusText := BuildStatusText(status, statusCode, latencyMs, errMsg)
	var msg string
	if status == "down" {
		msg = fmt.Sprintf("站点 %s 不可用 — %s", name, statusText)
	} else {
		msg = fmt.Sprintf("站点 %s 已恢复 — %s", name, statusText)
	}
	m.alerter.sendAlert(name, alertType, msg, fmt.Sprintf("%s\nURL: %s", msg, url))
}