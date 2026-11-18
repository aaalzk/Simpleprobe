package server

import (
	"testing"
	"time"
)

// TestHideServerRemovesFromDashboardButKeepsRows pins down the soft-delete
// contract: the host disappears from the dashboard and from the server count,
// but its row and its history stay in the database.
func TestHideServerRemovesFromDashboardButKeepsRows(t *testing.T) {
	st := newTestStore(t)
	reportOnline(t, st, "web-01")
	reportOnline(t, st, "web-02")

	ok, err := st.HideServer("web-01")
	if err != nil {
		t.Fatalf("HideServer: %v", err)
	}
	if !ok {
		t.Fatal("HideServer reported an unknown server")
	}

	visible, err := st.GetAllServers()
	if err != nil {
		t.Fatalf("GetAllServers: %v", err)
	}
	if len(visible) != 1 || visible[0].Name != "web-02" {
		t.Fatalf("visible servers = %+v, want only web-02", visible)
	}

	if n, err := st.CountServers(); err != nil || n != 1 {
		t.Fatalf("CountServers = %d, %v; want 1", n, err)
	}

	// The row itself survives, flagged as hidden.
	rec, err := st.GetServerByName("web-01")
	if err != nil {
		t.Fatalf("GetServerByName: %v", err)
	}
	if !rec.Hidden {
		t.Fatal("web-01 should be hidden in the database")
	}

	// History survives: that is the point of a soft delete.
	if err := st.InsertReport("web-01", time.Now().UTC().Unix(), 1, 2, 3, 4, 5, 0.1, 0.2, 0.3); err != nil {
		t.Fatalf("InsertReport: %v", err)
	}
	hist, err := st.GetHistory("web-01", 24)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(hist) == 0 {
		t.Fatal("history of a hidden server must survive")
	}
}

// TestHiddenServerStaysHiddenWhenItKeepsReporting is the "force the operator
// to decide" rule: an agent that is still alive can still write its samples,
// but its reports must never bring the host back to the dashboard.
func TestHiddenServerStaysHiddenWhenItKeepsReporting(t *testing.T) {
	st := newTestStore(t)
	reportOnline(t, st, "web-01")
	if ok, err := st.HideServer("web-01"); err != nil || !ok {
		t.Fatalf("HideServer = %v, %v", ok, err)
	}

	reportOnline(t, st, "web-01") // the agent keeps reporting

	rec, err := st.GetServerByName("web-01")
	if err != nil {
		t.Fatalf("GetServerByName: %v", err)
	}
	if !rec.Hidden {
		t.Fatal("a report must not unhide a server")
	}
	if visible, err := st.GetAllServers(); err != nil || len(visible) != 0 {
		t.Fatalf("GetAllServers = %+v, %v; want none", visible, err)
	}
}

// TestHiddenServerIsIgnoredByOfflineDetection keeps hidden hosts out of the
// offline alerting path, including the link-failure ratio.
func TestHiddenServerIsIgnoredByOfflineDetection(t *testing.T) {
	st := newTestStore(t)
	reportOnline(t, st, "web-01")
	backdateLastSeen(t, st, "web-01", 10*time.Minute)
	if ok, err := st.HideServer("web-01"); err != nil || !ok {
		t.Fatalf("HideServer = %v, %v", ok, err)
	}

	newly, err := st.MarkOffline(60)
	if err != nil {
		t.Fatalf("MarkOffline: %v", err)
	}
	if len(newly) != 0 {
		t.Fatalf("MarkOffline reported a hidden server: %v", newly)
	}

	rec, err := st.GetServerByName("web-01")
	if err != nil {
		t.Fatalf("GetServerByName: %v", err)
	}
	if rec.Status != "online" {
		t.Fatalf("status = %q, want online (hidden rows are left alone)", rec.Status)
	}

	offline, err := st.GetOfflineServers()
	if err != nil {
		t.Fatalf("GetOfflineServers: %v", err)
	}
	if len(offline) != 0 {
		t.Fatalf("GetOfflineServers = %v, want none", offline)
	}
}

// TestHideClearsAlertBookkeeping checks that a restore cannot inherit a stale
// cooldown or tolerance count from before the delete.
func TestHideClearsAlertBookkeeping(t *testing.T) {
	st := newTestStore(t)
	reportOnline(t, st, "web-01")
	if err := st.SetAlertCooldown("web-01", "offline"); err != nil {
		t.Fatalf("SetAlertCooldown: %v", err)
	}
	if _, err := st.IncrementAlertTolerance("web-01", "offline"); err != nil {
		t.Fatalf("IncrementAlertTolerance: %v", err)
	}

	if ok, err := st.HideServer("web-01"); err != nil || !ok {
		t.Fatalf("HideServer = %v, %v", ok, err)
	}

	if ok := st.CheckAlertCooldown("web-01", "offline", 3600); ok {
		t.Fatal("cooldown of a hidden server must be cleared")
	}
	if n := st.GetAlertToleranceCount("web-01", "offline"); n != 0 {
		t.Fatalf("tolerance count = %d, want 0", n)
	}
}

// TestUnhideServerRestores makes sure a delete can be undone.
func TestUnhideServerRestores(t *testing.T) {
	st := newTestStore(t)
	reportOnline(t, st, "web-01")
	if ok, err := st.HideServer("web-01"); err != nil || !ok {
		t.Fatalf("HideServer = %v, %v", ok, err)
	}

	ok, err := st.UnhideServer("web-01")
	if err != nil {
		t.Fatalf("UnhideServer: %v", err)
	}
	if !ok {
		t.Fatal("UnhideServer reported an unknown server")
	}

	visible, err := st.GetAllServers()
	if err != nil {
		t.Fatalf("GetAllServers: %v", err)
	}
	if len(visible) != 1 || visible[0].Name != "web-01" {
		t.Fatalf("visible servers = %+v, want web-01", visible)
	}
	rec, err := st.GetServerByName("web-01")
	if err != nil {
		t.Fatalf("GetServerByName: %v", err)
	}
	if rec.Hidden {
		t.Fatal("web-01 should no longer be hidden")
	}
}

// TestHideUnknownServer rejects a name that was never reported.
func TestHideUnknownServer(t *testing.T) {
	st := newTestStore(t)

	ok, err := st.HideServer("nope")
	if err != nil {
		t.Fatalf("HideServer: %v", err)
	}
	if ok {
		t.Fatal("HideServer accepted an unknown name")
	}

	ok, err = st.UnhideServer("nope")
	if err != nil {
		t.Fatalf("UnhideServer: %v", err)
	}
	if ok {
		t.Fatal("UnhideServer accepted an unknown name")
	}
}

// TestListAllServersIncludesHidden backs the `host list` command: the operator
// has to be able to see what is currently deleted.
func TestListAllServersIncludesHidden(t *testing.T) {
	st := newTestStore(t)
	reportOnline(t, st, "web-01")
	reportOnline(t, st, "web-02")
	if ok, err := st.HideServer("web-01"); err != nil || !ok {
		t.Fatalf("HideServer = %v, %v", ok, err)
	}

	all, err := st.ListAllServers()
	if err != nil {
		t.Fatalf("ListAllServers: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAllServers returned %d rows, want 2", len(all))
	}

	foundHidden := false
	for _, s := range all {
		if s.Name == "web-01" && s.Hidden {
			foundHidden = true
		}
	}
	if !foundHidden {
		t.Fatalf("web-01 is missing from ListAllServers or not flagged hidden: %+v", all)
	}
}
