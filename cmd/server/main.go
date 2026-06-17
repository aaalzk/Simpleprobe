package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aaalzk/Simpleprobe/internal/config"
	"github.com/aaalzk/Simpleprobe/internal/server"
	"github.com/aaalzk/Simpleprobe/web"
)

var version = "1.0.0"

func main() {
	cfgPath := flag.String("c", "server.yml", "path to server config file")
	showVersion := flag.Bool("v", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("simpleprobe-server", version)
		os.Exit(0)
	}

	cfg, err := config.LoadServerConfig(*cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Token == "change-me" {
		log.Println("WARNING: using default token 'change-me' — please set a secure token in server.yml")
	}

	// Open database
	store, err := server.NewStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	// Start periodic cleanup
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := store.CleanupOldReports(cfg.HistoryRetentionHrs); err != nil {
				log.Printf("ERROR: cleanup old reports: %v", err)
			}
		}
	}()

	// Start alerter
	alerter := server.NewAlerter(store, cfg.Alerts, cfg.Gotify)
	alerter.Start()

	// Set up HTTP routes
	mux := http.NewServeMux()

	// API routes
	apiHandler := server.NewAPIHandler(store, alerter, cfg.Token)
	apiHandler.RegisterRoutes(mux)

	// Static files (dashboard)
	mux.Handle("/", web.Handler())

	// Start HTTP server
	log.Printf("simpleprobe-server %s starting", version)
	log.Printf("  Listen: %s", cfg.Listen)
	log.Printf("  DB:     %s", cfg.DBPath)
	log.Printf("  Gotify: %s (configured: %v)", cfg.Gotify.URL, cfg.Gotify.URL != "")

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %v, shutting down", sig)
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("Server stopped")
}