package server

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store provides database operations for the probe server.
type Store struct {
	db *sql.DB
}

// ServerRecord represents a monitored server's current state.
type ServerRecord struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Status        string    `json:"status"` // "online" or "offline"
	LastSeen      time.Time `json:"last_seen"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemPercent    float64   `json:"mem_percent"`
	MemTotal      uint64    `json:"mem_total"`
	MemUsed       uint64    `json:"mem_used"`
	DiskPercent   float64   `json:"disk_percent"`
	DiskTotal     uint64    `json:"disk_total"`
	DiskUsed      uint64    `json:"disk_used"`
	NetRxRate     float64   `json:"net_rx_rate"`
	NetTxRate     float64   `json:"net_tx_rate"`
	NetRxBytes    uint64    `json:"net_rx_bytes"`
	NetTxBytes    uint64    `json:"net_tx_bytes"`
	Load1         float64   `json:"load_1"`
	Load5         float64   `json:"load_5"`
	Load15        float64   `json:"load_15"`
	Uptime        uint64    `json:"uptime"`
	TCPConns      uint64    `json:"tcp_conns"`
	ProcessCount  uint64    `json:"process_count"`
	OSName        string    `json:"os_name"`
	KernelVersion string    `json:"kernel_version"`
}

// ReportRecord is a historical report entry.
type ReportRecord struct {
	ID          int64     `json:"id"`
	ServerName  string    `json:"server_name"`
	Timestamp   time.Time `json:"timestamp"`
	CPUPercent  float64   `json:"cpu_percent"`
	MemPercent  float64   `json:"mem_percent"`
	DiskPercent float64   `json:"disk_percent"`
	NetRxRate   float64   `json:"net_rx_rate"`
	NetTxRate   float64   `json:"net_tx_rate"`
	Load1       float64   `json:"load_1"`
	Load5       float64   `json:"load_5"`
	Load15      float64   `json:"load_15"`
}

// AlertRecord is an alert history entry.
type AlertRecord struct {
	ID        int64     `json:"id"`
	ServerName string   `json:"server_name"`
	Type      string    `json:"type"` // "offline", "online", "cpu", "traffic"
	Message   string    `json:"message"`
	SentAt    time.Time `json:"sent_at"`
}

// NewStore opens (or creates) the SQLite database and runs migrations.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite works best with a single writer

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS servers (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT UNIQUE NOT NULL,
			status        TEXT NOT NULL DEFAULT 'online',
			last_seen     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			cpu_percent   REAL NOT NULL DEFAULT 0,
			mem_percent   REAL NOT NULL DEFAULT 0,
			mem_total     INTEGER NOT NULL DEFAULT 0,
			mem_used      INTEGER NOT NULL DEFAULT 0,
			disk_percent  REAL NOT NULL DEFAULT 0,
			disk_total    INTEGER NOT NULL DEFAULT 0,
			disk_used     INTEGER NOT NULL DEFAULT 0,
			net_rx_rate   REAL NOT NULL DEFAULT 0,
			net_tx_rate   REAL NOT NULL DEFAULT 0,
			net_rx_bytes  INTEGER NOT NULL DEFAULT 0,
			net_tx_bytes  INTEGER NOT NULL DEFAULT 0,
			load_1        REAL NOT NULL DEFAULT 0,
			load_5        REAL NOT NULL DEFAULT 0,
			load_15       REAL NOT NULL DEFAULT 0,
			uptime        INTEGER NOT NULL DEFAULT 0,
			tcp_conns     INTEGER NOT NULL DEFAULT 0,
			process_count INTEGER NOT NULL DEFAULT 0,
			os_name       TEXT NOT NULL DEFAULT '',
			kernel_version TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS reports (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			server_name  TEXT NOT NULL,
			timestamp    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			cpu_percent  REAL NOT NULL DEFAULT 0,
			mem_percent  REAL NOT NULL DEFAULT 0,
			disk_percent REAL NOT NULL DEFAULT 0,
			net_rx_rate  REAL NOT NULL DEFAULT 0,
			net_tx_rate  REAL NOT NULL DEFAULT 0,
			load_1       REAL NOT NULL DEFAULT 0,
			load_5       REAL NOT NULL DEFAULT 0,
			load_15      REAL NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reports_server_time ON reports(server_name, timestamp)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			server_name TEXT NOT NULL,
			type        TEXT NOT NULL,
			message     TEXT NOT NULL,
			sent_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_server_time ON alerts(server_name, sent_at)`,
		`CREATE TABLE IF NOT EXISTS alert_cooldowns (
			server_name TEXT NOT NULL,
			alert_type  TEXT NOT NULL,
			last_sent   DATETIME NOT NULL,
			PRIMARY KEY (server_name, alert_type)
		)`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", q, err)
		}
	}
	return nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// UpsertServer inserts or updates a server record with the latest report data.
func (s *Store) UpsertServer(name string, cpuPct, memPct float64, memTotal, memUsed uint64,
	diskPct float64, diskTotal, diskUsed uint64,
	netRxRate, netTxRate float64, netRxBytes, netTxBytes uint64,
	load1, load5, load15 float64, uptime, tcpConns, procCount uint64,
	osName, kernelVer string) error {

	query := `INSERT INTO servers (name, status, last_seen, cpu_percent, mem_percent, mem_total, mem_used,
		disk_percent, disk_total, disk_used, net_rx_rate, net_tx_rate, net_rx_bytes, net_tx_bytes,
		load_1, load_5, load_15, uptime, tcp_conns, process_count, os_name, kernel_version)
		VALUES (?, 'online', CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			status        = 'online',
			last_seen     = CURRENT_TIMESTAMP,
			cpu_percent   = excluded.cpu_percent,
			mem_percent   = excluded.mem_percent,
			mem_total     = excluded.mem_total,
			mem_used      = excluded.mem_used,
			disk_percent  = excluded.disk_percent,
			disk_total    = excluded.disk_total,
			disk_used     = excluded.disk_used,
			net_rx_rate   = excluded.net_rx_rate,
			net_tx_rate   = excluded.net_tx_rate,
			net_rx_bytes  = excluded.net_rx_bytes,
			net_tx_bytes  = excluded.net_tx_bytes,
			load_1        = excluded.load_1,
			load_5        = excluded.load_5,
			load_15       = excluded.load_15,
			uptime        = excluded.uptime,
			tcp_conns     = excluded.tcp_conns,
			process_count = excluded.process_count,
			os_name       = excluded.os_name,
			kernel_version = excluded.kernel_version`

	_, err := s.db.Exec(query, name, cpuPct, memPct, memTotal, memUsed,
		diskPct, diskTotal, diskUsed, netRxRate, netTxRate, netRxBytes, netTxBytes,
		load1, load5, load15, uptime, tcpConns, procCount, osName, kernelVer)
	return err
}

// InsertReport stores a historical report entry.
func (s *Store) InsertReport(name string, cpuPct, memPct, diskPct, netRxRate, netTxRate float64,
	load1, load5, load15 float64) error {

	query := `INSERT INTO reports (server_name, timestamp, cpu_percent, mem_percent, disk_percent,
		net_rx_rate, net_tx_rate, load_1, load_5, load_15)
		VALUES (?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query, name, cpuPct, memPct, diskPct, netRxRate, netTxRate, load1, load5, load15)
	return err
}

// MarkOffline marks servers that haven't reported within the specified duration as offline.
// Returns the names of servers that were just marked offline.
func (s *Store) MarkOffline(timeoutSeconds int) ([]string, error) {
	cutoff := time.Now().Add(-time.Duration(timeoutSeconds) * time.Second)

	// First, find servers that are online but past the cutoff
	rows, err := s.db.Query(`SELECT name FROM servers WHERE status = 'online' AND last_seen < ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Then update them to offline
	if len(names) > 0 {
		if _, err := s.db.Exec(`UPDATE servers SET status = 'offline' WHERE status = 'online' AND last_seen < ?`, cutoff); err != nil {
			return nil, err
		}
	}

	return names, nil
}

// GetAllServers returns all server records.
func (s *Store) GetAllServers() ([]ServerRecord, error) {
	query := `SELECT id, name, status, last_seen, cpu_percent, mem_percent, mem_total, mem_used,
		disk_percent, disk_total, disk_used, net_rx_rate, net_tx_rate, net_rx_bytes, net_tx_bytes,
		load_1, load_5, load_15, uptime, tcp_conns, process_count, os_name, kernel_version
		FROM servers ORDER BY name`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []ServerRecord
	for rows.Next() {
		var sr ServerRecord
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Status, &sr.LastSeen,
			&sr.CPUPercent, &sr.MemPercent, &sr.MemTotal, &sr.MemUsed,
			&sr.DiskPercent, &sr.DiskTotal, &sr.DiskUsed,
			&sr.NetRxRate, &sr.NetTxRate, &sr.NetRxBytes, &sr.NetTxBytes,
			&sr.Load1, &sr.Load5, &sr.Load15, &sr.Uptime, &sr.TCPConns,
			&sr.ProcessCount, &sr.OSName, &sr.KernelVersion); err != nil {
			return nil, err
		}
		servers = append(servers, sr)
	}
	return servers, rows.Err()
}

// GetServerByName returns a single server record.
func (s *Store) GetServerByName(name string) (*ServerRecord, error) {
	query := `SELECT id, name, status, last_seen, cpu_percent, mem_percent, mem_total, mem_used,
		disk_percent, disk_total, disk_used, net_rx_rate, net_tx_rate, net_rx_bytes, net_tx_bytes,
		load_1, load_5, load_15, uptime, tcp_conns, process_count, os_name, kernel_version
		FROM servers WHERE name = ?`

	var sr ServerRecord
	err := s.db.QueryRow(query, name).Scan(&sr.ID, &sr.Name, &sr.Status, &sr.LastSeen,
		&sr.CPUPercent, &sr.MemPercent, &sr.MemTotal, &sr.MemUsed,
		&sr.DiskPercent, &sr.DiskTotal, &sr.DiskUsed,
		&sr.NetRxRate, &sr.NetTxRate, &sr.NetRxBytes, &sr.NetTxBytes,
		&sr.Load1, &sr.Load5, &sr.Load15, &sr.Uptime, &sr.TCPConns,
		&sr.ProcessCount, &sr.OSName, &sr.KernelVersion)
	if err != nil {
		return nil, err
	}
	return &sr, nil
}

// GetHistory returns historical reports for a server within the given time window.
func (s *Store) GetHistory(name string, hours int) ([]ReportRecord, error) {
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
	query := `SELECT id, server_name, timestamp, cpu_percent, mem_percent, disk_percent,
		net_rx_rate, net_tx_rate, load_1, load_5, load_15
		FROM reports WHERE server_name = ? AND timestamp >= ?
		ORDER BY timestamp ASC`

	rows, err := s.db.Query(query, name, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []ReportRecord
	for rows.Next() {
		var r ReportRecord
		if err := rows.Scan(&r.ID, &r.ServerName, &r.Timestamp,
			&r.CPUPercent, &r.MemPercent, &r.DiskPercent,
			&r.NetRxRate, &r.NetTxRate, &r.Load1, &r.Load5, &r.Load15); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

// InsertAlert records an alert event.
func (s *Store) InsertAlert(serverName, alertType, message string) error {
	query := `INSERT INTO alerts (server_name, type, message, sent_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	_, err := s.db.Exec(query, serverName, alertType, message)
	return err
}

// GetRecentAlerts returns the most recent N alerts.
func (s *Store) GetRecentAlerts(limit int) ([]AlertRecord, error) {
	query := `SELECT id, server_name, type, message, sent_at FROM alerts ORDER BY sent_at DESC LIMIT ?`
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []AlertRecord
	for rows.Next() {
		var a AlertRecord
		if err := rows.Scan(&a.ID, &a.ServerName, &a.Type, &a.Message, &a.SentAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// CheckAlertCooldown returns true if an alert of the given type for the given server
// is still in cooldown.
func (s *Store) CheckAlertCooldown(serverName, alertType string, cooldownSec int) bool {
	var lastSent time.Time
	query := `SELECT last_sent FROM alert_cooldowns WHERE server_name = ? AND alert_type = ?`
	err := s.db.QueryRow(query, serverName, alertType).Scan(&lastSent)
	if err != nil {
		return false // no record, no cooldown
	}
	return time.Since(lastSent) < time.Duration(cooldownSec)*time.Second
}

// SetAlertCooldown records (or updates) the last sent time for an alert type.
func (s *Store) SetAlertCooldown(serverName, alertType string) error {
	query := `INSERT INTO alert_cooldowns (server_name, alert_type, last_sent)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(server_name, alert_type) DO UPDATE SET last_sent = CURRENT_TIMESTAMP`
	_, err := s.db.Exec(query, serverName, alertType)
	return err
}

// CleanupOldReports removes reports older than the specified number of hours.
func (s *Store) CleanupOldReports(retentionHours int) error {
	cutoff := time.Now().Add(-time.Duration(retentionHours) * time.Hour)
	_, err := s.db.Exec(`DELETE FROM reports WHERE timestamp < ?`, cutoff)
	return err
}