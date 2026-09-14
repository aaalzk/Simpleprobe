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
