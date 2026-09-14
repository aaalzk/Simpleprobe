package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aaalzk/Simpleprobe/internal/config"
)

// newTestStore opens a throwaway SQLite database for a single test.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := NewStore(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newTestAlerter returns an alerter whose cooldown is disabled, so tests
// exercise the tolerance counters instead of the cooldown window.
func newTestAlerter(t *testing.T, st *Store) *Alerter {
	t.Helper()
	cfg := config.DefaultServerConfig().Alerts
	cfg.CooldownSeconds = 0
	return NewAlerter(st, cfg, config.GotifyConfig{})
}

// reportOnline simulates one agent report, so the server row exists and is online.
func reportOnline(t *testing.T, st *Store, name string) {
	t.Helper()
	if err := st.UpsertServer(name,
		10, 20, 100, 20,
		30, 100, 30,
		1, 2, 3, 4,
		0.1, 0.2, 0.3,
		100, 5, 50,
		"Linux", "6.1"); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
}

// backdateLastSeen makes the server look silent for d, so MarkOffline picks it up.
func backdateLastSeen(t *testing.T, st *Store, name string, d time.Duration) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE servers SET last_seen = ? WHERE name = ?`,
		time.Now().UTC().Add(-d), name); err != nil {
		t.Fatalf("backdate last_seen: %v", err)
	}
}

func countAlerts(t *testing.T, st *Store, name, alertType string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM alerts WHERE server_name = ? AND type = ?`,
		name, alertType).Scan(&n); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	return n
}

// The offline scanner runs every 15s. After `tolerance` consecutive scans that
// still find the server offline, an offline alert must be raised.
func TestOfflineAlertFiresAfterConsecutiveScans(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)
	reportOnline(t, st, "Storage")
	backdateLastSeen(t, st, "Storage", 5*time.Minute)

	tolerance := a.cfg.GetTolerance("offline")
	if tolerance < 2 {
		t.Fatalf("test expects tolerance >= 2, got %d", tolerance)
	}

	for i := 0; i < tolerance; i++ {
		a.scanOffline()
	}

	if got := countAlerts(t, st, "Storage", "offline"); got != 1 {
		t.Fatalf("offline alerts after %d consecutive offline scans = %d, want 1", tolerance, got)
	}
}

// One outage must produce exactly one offline alert, no matter how long the
// server stays down or how often the scanner runs.
func TestOfflineAlertFiresOnlyOncePerOutage(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)
	reportOnline(t, st, "Storage")
	backdateLastSeen(t, st, "Storage", 5*time.Minute)

	for i := 0; i < 10; i++ {
		a.scanOffline()
	}

	if got := countAlerts(t, st, "Storage", "offline"); got != 1 {
		t.Fatalf("offline alerts for a single outage = %d, want 1", got)
	}
}

// After a recovery the offline counter must start over, so the next outage has
// to be confirmed by `tolerance` fresh scans before alerting again.
func TestOfflineAlertFiresAgainAfterRecovery(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)
	reportOnline(t, st, "Storage")
	backdateLastSeen(t, st, "Storage", 5*time.Minute)

	tolerance := a.cfg.GetTolerance("offline")
	for i := 0; i < tolerance; i++ {
		a.scanOffline()
	}

	// Server comes back and reports again.
	reportOnline(t, st, "Storage")
	a.CheckOnline("Storage", true)

	// Goes down again.
	backdateLastSeen(t, st, "Storage", 5*time.Minute)
	for i := 0; i < tolerance; i++ {
		a.scanOffline()
	}

	if got := countAlerts(t, st, "Storage", "offline"); got != 2 {
		t.Fatalf("offline alerts across two outages = %d, want 2", got)
	}
}

// The recovery alert must wait for `online` consecutive reports, and its
// counter must restart afterwards instead of accumulating for the lifetime of
// the process (which reported ever-growing "连续 N 次检测" numbers).
func TestOnlineAlertNeedsConsecutiveReportsAndResets(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)
	reportOnline(t, st, "Storage")

	tolerance := a.cfg.GetTolerance("online")
	if tolerance < 2 {
		t.Fatalf("test expects tolerance >= 2, got %d", tolerance)
	}

	// Reports 1..tolerance-1: still confirming, nothing announced yet.
	for i := 1; i < tolerance; i++ {
		a.CheckOnline("Storage", i == 1)
	}
	if got := countAlerts(t, st, "Storage", "online"); got != 0 {
		t.Fatalf("online alerts after %d of %d confirming reports = %d, want 0", tolerance-1, tolerance, got)
	}

	// The confirming report.
	a.CheckOnline("Storage", false)
	if got := countAlerts(t, st, "Storage", "online"); got != 1 {
		t.Fatalf("online alerts after %d confirming reports = %d, want 1", tolerance, got)
	}
	if n := st.GetAlertToleranceCount("Storage", "online"); n != 0 {
		t.Fatalf("online counter after a confirmed recovery = %d, want 0", n)
	}

	// Steady state must not keep counting.
	for i := 0; i < 10; i++ {
		a.CheckOnline("Storage", false)
	}
	if got := countAlerts(t, st, "Storage", "online"); got != 1 {
		t.Fatalf("online alerts during steady state = %d, want 1", got)
	}
}

// A recovery interrupted by a fresh outage must not announce anything: the
// confirmation has to start over.
func TestOnlineAlertSkippedWhenOutageRepeats(t *testing.T) {
	st := newTestStore(t)
	a := newTestAlerter(t, st)

	// Outage #1, then the server reports once — confirmation starts.
	reportOnline(t, st, "Storage")
	backdateLastSeen(t, st, "Storage", 5*time.Minute)
	a.scanOffline()
	reportOnline(t, st, "Storage")
	a.CheckOnline("Storage", true)

	// It goes silent again before the confirmation completes.
	backdateLastSeen(t, st, "Storage", 5*time.Minute)
	a.scanOffline()

	if got := countAlerts(t, st, "Storage", "online"); got != 0 {
		t.Fatalf("online alert announced for an unconfirmed recovery = %d, want 0", got)
	}
	if n := st.GetAlertToleranceCount("Storage", "online"); n != 0 {
		t.Fatalf("online counter after an interrupted recovery = %d, want 0", n)
	}

	// Outage #2: the recovery must be re-confirmed from scratch.
	tolerance := a.cfg.GetTolerance("online")
	reportOnline(t, st, "Storage")
	for i := 1; i <= tolerance; i++ {
		a.CheckOnline("Storage", i == 1)
	}
	if got := countAlerts(t, st, "Storage", "online"); got != 1 {
		t.Fatalf("online alerts after a full re-confirmation = %d, want 1", got)
	}
}
