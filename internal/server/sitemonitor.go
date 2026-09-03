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
	cooldown  int // alert cooldown seconds, from alert config
	transport *http.Transport
	stopCh    chan struct{}
}

// NewSiteMonitor creates a site monitor. cooldown is the alert cooldown in
// seconds (reuse the alert.cooldown_seconds setting).
func NewSiteMonitor(store *Store, alerter *Alerter, sites []config.SiteConfig, cooldown int) *SiteMonitor {
	return &SiteMonitor{
		store:    store,
		alerter:  alerter,
		sites:    sites,
		cooldown: cooldown,
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

	m.alertOnTransition(site.Name, site.URL, prev, status, statusCode, latencyMs, errMsg)
}

// alertOnTransition sends a Gotify alert when a site flips between up and down,
// respecting the shared alert cooldown.
func (m *SiteMonitor) alertOnTransition(name, url string, prev *SiteRecord, status string, statusCode int, latencyMs float64, errMsg string) {
	if prev == nil || prev.Status == status {
		return // first probe or no transition
	}

	alertType := "site_down"
	var msg string
	if status == "down" {
		if statusCode > 0 {
			msg = fmt.Sprintf("站点 %s 不可用 — HTTP %d，延迟 %.0fms", name, statusCode, latencyMs)
		} else {
			msg = fmt.Sprintf("站点 %s 不可用 — %s", name, errMsg)
		}
	} else {
		alertType = "site_up"
		msg = fmt.Sprintf("站点 %s 已恢复可用 — HTTP %d，延迟 %.0fms", name, statusCode, latencyMs)
	}

	if m.store.CheckAlertCooldown(name, alertType, m.cooldown) {
		return
	}
	m.alerter.sendAlert(name, alertType, msg, fmt.Sprintf("%s\nURL: %s", msg, url))
}