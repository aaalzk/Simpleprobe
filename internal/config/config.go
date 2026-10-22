package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds the server configuration.
type ServerConfig struct {
	Listen              string       `yaml:"listen"`
	DBPath              string       `yaml:"db_path"`
	Token               string       `yaml:"token"`
	Gotify              GotifyConfig `yaml:"gotify"`
	Alerts              AlertConfig  `yaml:"alerts"`
	Gaze                GazeConfig   `yaml:"gaze"`
	HistoryRetentionHrs int          `yaml:"history_retention_hours"`
	Sites               []SiteConfig `yaml:"sites"`
}

// SiteConfig holds a single site availability monitor target.
type SiteConfig struct {
	Name     string `yaml:"name"`     // display name, also used for history lookup
	URL      string `yaml:"url"`      // full URL to probe (http:// or https://)
	Interval int    `yaml:"interval"` // probe interval in seconds, default 60
	Timeout  int    `yaml:"timeout"`  // request timeout in seconds, default 10
}

// GotifyConfig holds Gotify push notification settings.
type GotifyConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

// AlertTolerance holds per-type tolerance (consecutive triggers before alerting).
type AlertTolerance struct {
	Offline   int `yaml:"offline"`
	Online    int `yaml:"online"`
	CPU       int `yaml:"cpu"`
	TrafficRx int `yaml:"traffic_rx"`
	TrafficTx int `yaml:"traffic_tx"`
	SiteDown  int `yaml:"site_down"`
	SiteUp    int `yaml:"site_up"`
}

// AlertConfig holds alert thresholds and cooldown settings.
type AlertConfig struct {
	OfflineSeconds  int            `yaml:"offline_seconds"`
	CPUThreshold    float64        `yaml:"cpu_threshold"`
	TrafficRxMbps   float64        `yaml:"traffic_rx_mbps"`
	TrafficTxMbps   float64        `yaml:"traffic_tx_mbps"`
	CooldownSeconds int            `yaml:"cooldown_seconds"`
	Tolerance       AlertTolerance `yaml:"tolerance"`
	// LinkFailureRatio is the share of the known servers that have to go
	// silent at the same time before the outage is treated as a broken
	// reporting link (a Cloudflare outage, a dead network hop) instead of
	// N individual machines. Below that share every server keeps its own
	// offline alert. The rule only applies when at least two servers are
	// known: one silent machine is a machine, not a link.
	LinkFailureRatio float64 `yaml:"link_failure_ratio"`
}

// GazeConfig holds gaze mode settings.
type GazeConfig struct {
	Enabled       bool `yaml:"enabled"`
	AgentInterval int  `yaml:"agent_interval"` // seconds, default 10
	WebInterval   int  `yaml:"web_interval"`   // seconds, default 5
	Timeout       int  `yaml:"timeout"`        // seconds without gaze ping to exit, default 30
}

// AgentConfig holds the agent configuration.
type AgentConfig struct {
	ServerURL string `yaml:"server_url"`
	Name      string `yaml:"name"`
	Token     string `yaml:"token"`
	Interval  int    `yaml:"interval"`
	// GazeInterval is the report interval used while somebody is watching
	// the dashboard. 0 disables the speed-up, so the agent always reports
	// at Interval. A shorter interval means fresher charts but also more
	// traffic through Cloudflare.
	GazeInterval int `yaml:"gaze_interval"`
	// Contact is an optional contact URL or e-mail that is appended to the
	// User-Agent, e.g. "https://example.com/agent". Bot-management rules
	// treat an identifiable, contactable client far more leniently than an
	// anonymous one, and the contact address is what a Cloudflare
	// administrator looks for when deciding whether to allow the traffic.
	Contact string `yaml:"contact"`
}

// GetTolerance returns the tolerance for the given alert type.
func (c *AlertConfig) GetTolerance(alertType string) int {
	switch alertType {
	case "offline":
		if c.Tolerance.Offline > 0 {
			return c.Tolerance.Offline
		}
	case "online":
		if c.Tolerance.Online > 0 {
			return c.Tolerance.Online
		}
	case "cpu":
		if c.Tolerance.CPU > 0 {
			return c.Tolerance.CPU
		}
	case "traffic_rx":
		if c.Tolerance.TrafficRx > 0 {
			return c.Tolerance.TrafficRx
		}
	case "traffic_tx":
		if c.Tolerance.TrafficTx > 0 {
			return c.Tolerance.TrafficTx
		}
	case "site_down":
		if c.Tolerance.SiteDown > 0 {
			return c.Tolerance.SiteDown
		}
	case "site_up":
		if c.Tolerance.SiteUp > 0 {
			return c.Tolerance.SiteUp
		}
	}
	return 3 // fallback default
}

// DefaultServerConfig returns a ServerConfig with sensible defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Listen:              ":8080",
		DBPath:              "./probe.db",
		Token:               "change-me",
		HistoryRetentionHrs: 72,
		Alerts: AlertConfig{
			OfflineSeconds:  90,
			CPUThreshold:    90,
			TrafficRxMbps:   800,
			TrafficTxMbps:   800,
			CooldownSeconds: 300,
			Tolerance: AlertTolerance{
				Offline:   3,
				Online:    3,
				CPU:       3,
				TrafficRx: 3,
				TrafficTx: 3,
				SiteDown:  3,
				SiteUp:    3,
			},
			LinkFailureRatio: 0.8,
		},
		Gaze: GazeConfig{
			Enabled:       true,
			AgentInterval: 10,
			WebInterval:   5,
			Timeout:       30,
		},
	}
}

// DefaultAgentConfig returns an AgentConfig with sensible defaults.
func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		ServerURL:    "https://probe.example.com",
		Name:         "my-server",
		Token:        "change-me",
		Interval:     30,
		GazeInterval: 10,
	}
}

// LoadServerConfig reads server configuration from a YAML file,
// applying defaults for any missing fields.
func LoadServerConfig(path string) (ServerConfig, error) {
	cfg := DefaultServerConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadAgentConfig reads agent configuration from a YAML file,
// applying defaults for any missing fields.
func LoadAgentConfig(path string) (AgentConfig, error) {
	cfg := DefaultAgentConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
