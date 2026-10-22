package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aaalzk/Simpleprobe/internal/agent"
	"github.com/aaalzk/Simpleprobe/internal/config"
)

var version = "1.0.0"

const (
	// maxBackoff caps the delay between two consecutive failed reports.
	maxBackoff = 5 * time.Minute
	// backoffJitter is the relative jitter applied to the delay, so a fleet
	// of agents that failed at the same moment does not retry in lockstep.
	backoffJitter = 0.2
)

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
	pusher := agent.NewPusher(cfg.ServerURL, cfg.Token, version, cfg.Contact)

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Dynamic push loop: interval changes based on gaze mode.
	// A gaze interval of 0 disables the speed-up, so the agent always
	// reports at the configured interval.
	normalInterval := time.Duration(cfg.Interval) * time.Second
	gazeInterval := time.Duration(cfg.GazeInterval) * time.Second
	consecFail := 0

	for {
		if err := reportAndLog(collector, pusher, cfg.Name); err != nil {
			consecFail++
		} else {
			consecFail = 0
		}

		// Adjust interval based on server's gaze response
		base := normalInterval
		if gazeInterval > 0 && pusher.LastGaze() {
			base = gazeInterval
		}
		delay := backoffDelay(base, consecFail)
		if consecFail > 0 {
			log.Printf("Backing off: %d consecutive failure(s), next report in %s", consecFail, delay.Round(time.Second))
		}

		select {
		case <-time.After(delay):
		case sig := <-sigCh:
			log.Printf("Received %v, shutting down", sig)
			return
		}
	}
}

// reportAndLog collects metrics and pushes them to the server. It returns
// the push error (if any) so the caller can apply a backoff.
func reportAndLog(c *agent.Collector, p *agent.Pusher, name string) error {
	report := c.Collect(name)
	if _, err := p.Push(report); err != nil {
		log.Printf("ERROR: report failed: %v", err)
		return err
	}
	log.Printf("Report sent: CPU %.1f%%, Mem %.1f%%, NetRx %.1f KB/s, NetTx %.1f KB/s",
		report.Metrics.CPUPercent,
		report.Metrics.MemPercent,
		report.Metrics.NetRxRate/1024,
		report.Metrics.NetTxRate/1024,
	)
	return nil
}

// backoffDelay returns how long to wait before the next report.
//
// After consecutive failures the delay grows exponentially (base, 2×base,
// 4×base, … capped at maxBackoff) and is jittered by ±20%. A fixed, machine
// like retry cadence is exactly what Cloudflare's bot rules and rate
// limiting flag, and it also keeps a broken link from hammering the origin.
func backoffDelay(base time.Duration, consecFail int) time.Duration {
	d := base
	for i := 1; i < consecFail; i++ {
		d *= 2
		if d >= maxBackoff {
			d = maxBackoff
			break
		}
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	// ±backoffJitter relative jitter.
	jitter := time.Duration(float64(d) * backoffJitter * (rand.Float64()*2 - 1))
	d += jitter
	if d < time.Second {
		d = time.Second
	}
	return d
}
