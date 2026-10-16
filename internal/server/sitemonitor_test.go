package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aaalzk/Simpleprobe/internal/config"
)

// Site alerts used to be counted only on up<->down transitions, so with the
// default tolerance of 3 they could never reach the threshold. They must now
// count every probe.
func TestSiteAlertsCountEveryProbe(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)

	var failing atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	site := config.SiteConfig{Name: "Example", URL: srv.URL, Interval: 60, Timeout: 5}
	m := NewSiteMonitor(st, a, []config.SiteConfig{site}, a.cfg)

	// Baseline probe: up, but only establishes the state.
	m.probe(site, 5)
	if got := countAlerts(t, st, "Example", "site_down"); got != 0 {
		t.Fatalf("site_down alerts on the baseline probe = %d, want 0", got)
	}

	tolerance := a.cfg.GetTolerance("site_down")
	if tolerance < 2 {
		t.Fatalf("test expects tolerance >= 2, got %d", tolerance)
	}

	failing.Store(true)
	for i := 0; i < tolerance; i++ {
		m.probe(site, 5)
	}
	if got := countAlerts(t, st, "Example", "site_down"); got != 1 {
		t.Fatalf("site_down alerts after %d failing probes = %d, want 1", tolerance, got)
	}

	// Staying down must not spam further alerts.
	for i := 0; i < 5; i++ {
		m.probe(site, 5)
	}
	if got := countAlerts(t, st, "Example", "site_down"); got != 1 {
		t.Fatalf("site_down alerts while the outage continues = %d, want 1", got)
	}

	failing.Store(false)
	for i := 0; i < tolerance; i++ {
		m.probe(site, 5)
	}
	if got := countAlerts(t, st, "Example", "site_up"); got != 1 {
		t.Fatalf("site_up alerts after %d healthy probes = %d, want 1", tolerance, got)
	}
}

// newFlappingSite returns a monitor for a site whose probe result is driven by
// the returned flag: false = healthy, true = failing.
func newFlappingSite(t *testing.T, st *Store, a *Alerter) (*SiteMonitor, config.SiteConfig, *atomic.Bool) {
	t.Helper()

	var failing atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	site := config.SiteConfig{Name: "Example", URL: srv.URL, Interval: 60, Timeout: 5}
	return NewSiteMonitor(st, a, []config.SiteConfig{site}, a.cfg), site, &failing
}

// A site that dips below the down tolerance and comes back must not announce a
// recovery: nothing was ever announced as unavailable.
func TestSiteRecoverySkippedWhenDownWasNeverAnnounced(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)
	m, site, failing := newFlappingSite(t, st, a)
	upTolerance := a.cfg.GetTolerance("site_up")

	m.probe(site, 5) // baseline: up

	// A single failing probe: not enough for a site_down alert.
	failing.Store(true)
	m.probe(site, 5)
	if got := countAlerts(t, st, "Example", "site_down"); got != 0 {
		t.Fatalf("site_down alerts after one failing probe = %d, want 0", got)
	}

	// Healthy again, for long enough to confirm a recovery.
	failing.Store(false)
	for i := 0; i < upTolerance+2; i++ {
		m.probe(site, 5)
	}
	if got := countAlerts(t, st, "Example", "site_up"); got != 0 {
		t.Fatalf("site_up alerts for an unannounced outage = %d, want 0", got)
	}
}

// A recovery is announced once per announced outage, and a later dip that never
// reaches the down tolerance stays silent even though the site is healthy again.
func TestSiteRecoveryAnnouncedOncePerAnnouncedOutage(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)
	m, site, failing := newFlappingSite(t, st, a)
	downTolerance := a.cfg.GetTolerance("site_down")
	upTolerance := a.cfg.GetTolerance("site_up")

	m.probe(site, 5) // baseline: up

	// Announced outage, then a confirmed recovery.
	failing.Store(true)
	for i := 0; i < downTolerance; i++ {
		m.probe(site, 5)
	}
	if got := countAlerts(t, st, "Example", "site_down"); got != 1 {
		t.Fatalf("site_down alerts after %d failing probes = %d, want 1", downTolerance, got)
	}
	failing.Store(false)
	for i := 0; i < upTolerance; i++ {
		m.probe(site, 5)
	}
	if got := countAlerts(t, st, "Example", "site_up"); got != 1 {
		t.Fatalf("site_up alerts after %d healthy probes = %d, want 1", upTolerance, got)
	}

	// A later single-failure dip: no site_down alert, so no site_up either.
	failing.Store(true)
	m.probe(site, 5)
	failing.Store(false)
	for i := 0; i < upTolerance+2; i++ {
		m.probe(site, 5)
	}
	if got := countAlerts(t, st, "Example", "site_up"); got != 1 {
		t.Fatalf("site_up alerts after a silent dip = %d, want 1", got)
	}
}
