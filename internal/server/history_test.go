package server

import (
	"testing"
	"time"
)

// TestInsertReportDeduplicatesRepeatedReports pins down the idempotency of
// the report write: a retried report (same agent timestamp) must not add a
// second point to the chart.
func TestInsertReportDeduplicatesRepeatedReports(t *testing.T) {
	st := newTestStore(t)

	agentTS := time.Now().UTC().Unix()
	for i := 0; i < 3; i++ {
		if err := st.InsertReport("web-01", agentTS, 10, 20, 30, 100, 200, 0.1, 0.2, 0.3); err != nil {
			t.Fatalf("InsertReport: %v", err)
		}
	}

	var raw int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM reports WHERE server_name = 'web-01'`).Scan(&raw); err != nil {
		t.Fatalf("count raw reports: %v", err)
	}
	if raw != 1 {
		t.Fatalf("raw report rows after 3 identical reports = %d, want 1", raw)
	}

	// The one minute rollup keeps one bucket, but counts the samples.
	var buckets, samples int
	if err := st.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(samples), 0) FROM reports_1m WHERE server_name = 'web-01'`).Scan(&buckets, &samples); err != nil {
		t.Fatalf("count rollup buckets: %v", err)
	}
	if buckets != 1 {
		t.Fatalf("rollup buckets after 3 identical reports = %d, want 1", buckets)
	}
	if samples != 3 {
		t.Fatalf("rollup sample counter = %d, want 3", samples)
	}
}

// TestHistoryResolutionFollowsWindow checks that a short window is answered
// with the full resolution samples while a long window is answered from the
// one minute rollup. That is what keeps a 24 hour query small.
func TestHistoryResolutionFollowsWindow(t *testing.T) {
	st := newTestStore(t)

	for i := 0; i < 5; i++ {
		if err := st.InsertReport("web-01", time.Now().UTC().Unix()+int64(i), 10, 20, 30, 100, 200, 0.1, 0.2, 0.3); err != nil {
			t.Fatalf("InsertReport: %v", err)
		}
	}

	raw, err := st.GetHistory("web-01", 1)
	if err != nil {
		t.Fatalf("GetHistory(1): %v", err)
	}
	if len(raw) != 5 {
		t.Fatalf("short window returned %d points, want 5 (full resolution)", len(raw))
	}

	rolled, err := st.GetHistory("web-01", 24)
	if err != nil {
		t.Fatalf("GetHistory(24): %v", err)
	}
	if len(rolled) != 1 {
		t.Fatalf("24h window returned %d points, want 1 (one minute rollup)", len(rolled))
	}
	if rolled[0].ServerName != "web-01" {
		t.Fatalf("rollup point server name = %q, want %q", rolled[0].ServerName, "web-01")
	}
}

// TestCleanupKeepsRollupButDropsOldRawSamples makes sure the raw samples are
// pruned early while the rollup still carries the long range.
func TestCleanupKeepsRollupButDropsOldRawSamples(t *testing.T) {
	st := newTestStore(t)

	if err := st.InsertReport("web-01", time.Now().UTC().Unix(), 10, 20, 30, 100, 200, 0.1, 0.2, 0.3); err != nil {
		t.Fatalf("InsertReport: %v", err)
	}

	// Pretend the raw sample is three hours old.
	if _, err := st.db.Exec(`UPDATE reports SET timestamp = ? WHERE server_name = 'web-01'`,
		time.Now().UTC().Add(-3*time.Hour)); err != nil {
		t.Fatalf("backdate reports: %v", err)
	}

	if err := st.CleanupOldReports(72); err != nil {
		t.Fatalf("CleanupOldReports: %v", err)
	}

	var raw int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM reports`).Scan(&raw); err != nil {
		t.Fatalf("count raw reports: %v", err)
	}
	if raw != 0 {
		t.Fatalf("raw rows after cleanup = %d, want 0 (raw samples are pruned after %d hours)", raw, rawHistoryHours)
	}

	var rollup int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM reports_1m`).Scan(&rollup); err != nil {
		t.Fatalf("count rollup rows: %v", err)
	}
	if rollup != 1 {
		t.Fatalf("rollup rows after cleanup = %d, want 1 (the rollup carries the long range)", rollup)
	}
}

// TestFleetSilenceRaisesLinkAlertInsteadOfPerServerAlerts covers the case
// where the reporting path is broken: when almost every server goes silent
// at the same time, one link alert replaces the per-server offline alerts.
func TestFleetSilenceRaisesLinkAlertInsteadOfPerServerAlerts(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)

	reportOnline(t, st, "A")
	reportOnline(t, st, "B")
	backdateLastSeen(t, st, "A", 5*time.Minute)
	backdateLastSeen(t, st, "B", 5*time.Minute)

	for i := 0; i < a.cfg.GetTolerance("offline"); i++ {
		a.scanOffline()
	}

	if got := countAlerts(t, st, "A", "offline"); got != 0 {
		t.Fatalf("per-server offline alerts during a fleet outage = %d, want 0", got)
	}
	if got := countAlerts(t, st, "__system__", "link_failure"); got != 1 {
		t.Fatalf("link_failure alerts = %d, want 1", got)
	}
}

// TestLinkAlertClosesWhenServersReportAgain makes sure the link alert is
// resolved once the fleet talks again.
func TestLinkAlertClosesWhenServersReportAgain(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)

	reportOnline(t, st, "A")
	reportOnline(t, st, "B")
	backdateLastSeen(t, st, "A", 5*time.Minute)
	backdateLastSeen(t, st, "B", 5*time.Minute)

	for i := 0; i < a.cfg.GetTolerance("offline"); i++ {
		a.scanOffline()
	}
	if got := countAlerts(t, st, "__system__", "link_failure"); got != 1 {
		t.Fatalf("link_failure alerts = %d, want 1", got)
	}

	// The link recovers: both agents report again.
	reportOnline(t, st, "A")
	reportOnline(t, st, "B")
	a.scanOffline()

	if got := countAlerts(t, st, "__system__", "link_recovery"); got != 1 {
		t.Fatalf("link_recovery alerts = %d, want 1", got)
	}
}

// TestSingleServerOutageStillAlertsPerServer makes sure the link rule never
// swallows a real single machine outage.
func TestSingleServerOutageStillAlertsPerServer(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)

	reportOnline(t, st, "Only")
	backdateLastSeen(t, st, "Only", 5*time.Minute)

	for i := 0; i < a.cfg.GetTolerance("offline"); i++ {
		a.scanOffline()
	}

	if got := countAlerts(t, st, "Only", "offline"); got != 1 {
		t.Fatalf("offline alerts for a single machine = %d, want 1", got)
	}
	if got := countAlerts(t, st, "__system__", "link_failure"); got != 0 {
		t.Fatalf("link_failure alerts for a single machine = %d, want 0", got)
	}
}
