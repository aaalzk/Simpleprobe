package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds the server configuration.
type ServerConfig struct {
	Listen              string        `yaml:"listen"`
	DBPath              string        `yaml:"db_path"`
	Token               string        `yaml:"token"`
	Gotify              GotifyConfig  `yaml:"gotify"`
	Alerts              AlertConfig   `yaml:"alerts"`
	HistoryRetentionHrs int           `yaml:"history_retention_hours"`
}

// GotifyConfig holds Gotify push notification settings.
type GotifyConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

// AlertConfig holds alert thresholds and cooldown settings.
type AlertConfig struct {
	OfflineSeconds  int     `yaml:"offline_seconds"`
	CPUThreshold    float64 `yaml:"cpu_threshold"`
	TrafficRxMbps   float64 `yaml:"traffic_rx_mbps"`
	TrafficTxMbps   float64 `yaml:"traffic_tx_mbps"`
	CooldownSeconds int     `yaml:"cooldown_seconds"`
}

// AgentConfig holds the agent configuration.
type AgentConfig struct {
	ServerURL string `yaml:"server_url"`
	Name      string `yaml:"name"`
	Token     string `yaml:"token"`
	Interval  int    `yaml:"interval"`
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
		},
	}
}

// DefaultAgentConfig returns an AgentConfig with sensible defaults.
func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		ServerURL: "https://probe.example.com",
		Name:      "my-server",
		Token:     "change-me",
		Interval:  30,
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