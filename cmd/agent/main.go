package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aaalzk/Simpleprobe/internal/agent"
	"github.com/aaalzk/Simpleprobe/internal/config"
)

var version = "1.0.0"

func main() {
	cfgPath := flag.String("c", "agent.yml", "path to agent config file")
	showVersion := flag.Bool("v", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("simpleprobe-agent", version)
		os.Exit(0)
	}

	cfg, err := config.LoadAgentConfig(*cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Token validation
	if cfg.Token == "change-me" {
		log.Fatalf("FATAL: default token 'change-me' is not allowed — please set a secure token in agent.yml")
	}
	if len(cfg.Token) < 16 {
		log.Fatalf("FATAL: token is too short (%d chars) — must be at least 16 characters", len(cfg.Token))
	}

	log.Printf("simpleprobe-agent %s starting", version)
	log.Printf("  Server: %s", cfg.ServerURL)
	log.Printf("  Name:   %s", cfg.Name)
	log.Printf("  Interval: %ds", cfg.Interval)

	collector := agent.NewCollector()
	pusher := agent.NewPusher(cfg.ServerURL, cfg.Token, version)

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Dynamic push loop: interval changes based on gaze mode
	normalInterval := time.Duration(cfg.Interval) * time.Second
	gazeInterval := 10 * time.Second

	for {
		reportAndLog(collector, pusher, cfg.Name)

		// Adjust interval based on server's gaze response
		if pusher.LastGaze() {
			normalInterval = gazeInterval
		} else {
			normalInterval = time.Duration(cfg.Interval) * time.Second
		}

		select {
		case <-time.After(normalInterval):
		case sig := <-sigCh:
			log.Printf("Received %v, shutting down", sig)
			return
		}
	}
}

func reportAndLog(c *agent.Collector, p *agent.Pusher, name string) {
	report := c.Collect(name)
	if err := p.Push(report); err != nil {
		log.Printf("ERROR: report failed: %v", err)
		return
	}
	log.Printf("Report sent: CPU %.1f%%, Mem %.1f%%, NetRx %.1f KB/s, NetTx %.1f KB/s",
		report.Metrics.CPUPercent,
		report.Metrics.MemPercent,
		report.Metrics.NetRxRate/1024,
		report.Metrics.NetTxRate/1024,
	)
}