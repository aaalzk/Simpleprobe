package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aaalzk/Simpleprobe/internal/config"
)

// TestChallengedSiteIsNotReportedAsDown covers a probe that was answered
// with a Cloudflare challenge instead of the site's own content. Such a
// result says nothing about the site's availability, so it must not raise
// an "unavailable" alert.
func TestChallengedSiteIsNotReportedAsDown(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><head><title>Just a moment...</title></head></html>"))
	}))
	defer srv.Close()

	site := config.SiteConfig{Name: "guarded", URL: srv.URL, Interval: 60, Timeout: 5}
	m := NewSiteMonitor(st, a, []config.SiteConfig{site}, a.cfg)
	m.probe(site, 5)

	state, err := st.GetSiteState("guarded")
	if err != nil {
		t.Fatalf("GetSiteState: %v", err)
	}
	if state == nil {
		t.Fatal("site state missing after the probe")
	}
	if state.Status != "challenged" {
		t.Fatalf("site status = %q, want %q", state.Status, "challenged")
	}
	if got := countAlerts(t, st, "guarded", "site_down"); got != 0 {
		t.Fatalf("site_down alerts for a challenged probe = %d, want 0", got)
	}
}

// TestRealFailureIsStillReportedAsDown makes sure the challenge detection
// does not swallow a genuine outage.
func TestRealFailureIsStillReportedAsDown(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	site := config.SiteConfig{Name: "broken", URL: srv.URL, Interval: 60, Timeout: 5}
	m := NewSiteMonitor(st, a, []config.SiteConfig{site}, a.cfg)

	// The first probe only establishes the baseline.
	m.probe(site, 5)
	for i := 0; i < a.cfg.GetTolerance("site_down"); i++ {
		m.probe(site, 5)
	}

	state, err := st.GetSiteState("broken")
	if err != nil {
		t.Fatalf("GetSiteState: %v", err)
	}
	if state == nil || state.Status != "down" {
		t.Fatalf("site status = %v, want down", state)
	}
	if got := countAlerts(t, st, "broken", "site_down"); got != 1 {
		t.Fatalf("site_down alerts for a real failure = %d, want 1", got)
	}
}
