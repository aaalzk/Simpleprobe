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
		// Read a small amount so the connection can be reused. The bytes are
		// kept because a Cloudflare challenge is recognised by its body.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		statusCode = resp.StatusCode
		switch {
		case statusCode >= 200 && statusCode < 400:
			status = "up"
		case isCloudflareChallenge(resp, body):
			// The site answered with a challenge page instead of its own
			// content. That says nothing about the site's availability, so
			// it must not be reported as "down".
			status = "challenged"
			errMsg = "被 Cloudflare 挑战"
			log.Printf("SiteMonitor: %s (%s) CHALLENGED: HTTP %d", site.Name, site.URL, statusCode)
		default:
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
//
// site_up is additionally gated on the matching site_down alert having really
// been delivered (the in-memory active-alert set in Alerter), so a site that
// flaps below the down tolerance recovers without a notification.
func (m *SiteMonitor) alertOnProbe(name, url string, prev *SiteRecord, status string, statusCode int, latencyMs float64, errMsg string) {
	if status == "challenged" {
		// A challenge says nothing about the site's availability: it
		// neither confirms an outage nor confirms a recovery, so it must
		// not touch any counter.
		return
	}
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

	// Only an announced outage may be announced as over. A dip that never
	// reached the down tolerance never sent a "不可用" notification, so the
	// site coming back stays silent instead of announcing a recovery for an
	// outage nobody was told about.
	if status == "up" && !m.alerter.AlertActive(name, "site_down") {
		return
	}

	if m.store.CheckAlertCooldown(name, alertType, m.alertCfg.CooldownSeconds) {
		return
	}

	if status == "up" {
		// The outage is closed, so a later dip has to raise its own alert
		// before it can be resolved.
		m.alerter.ResolveAlert(name, "site_down")
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
