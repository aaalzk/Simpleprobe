package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
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

	// Local maintenance subcommands work on the database directly and never
	// start the HTTP server, so they run before the token check: an operator
	// deleting a host does not need the API token.
	if args := flag.Args(); len(args) > 0 {
		runHostCommand(cfg, args)
		return
	}

	// Token validation
	if cfg.Token == "change-me" {
		log.Fatalf("FATAL: default token 'change-me' is not allowed — please set a secure token in server.yml")
	}
	if len(cfg.Token) < 16 {
		log.Fatalf("FATAL: token is too short (%d chars) — must be at least 16 characters", len(cfg.Token))
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
			if err := store.CleanupOldSiteChecks(cfg.HistoryRetentionHrs); err != nil {
				log.Printf("ERROR: cleanup old site checks: %v", err)
			}
		}
	}()

	// Start rate limiter cleanup
	rateLimiter := server.NewRateLimiter(10, 60*time.Second, 300*time.Second)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rateLimiter.Cleanup()
		}
	}()

	// Start alerter
	alerter := server.NewAlerter(store, cfg.Alerts, cfg.Gotify)
	alerter.Start()

	// Start site availability monitor
	siteMonitor := server.NewSiteMonitor(store, alerter, cfg.Sites, cfg.Alerts)
	siteMonitor.Start()

	// Set up rate limiter alert callback
	rateLimiter.OnAlert(func(ip string, count int) {
		alerter.SendSecurityAlert("brute_force",
			fmt.Sprintf("检测到暴力破解尝试 — IP: %s 短时间内 %d 次认证失败，已被临时封禁", ip, count))
	})

	// Set up HTTP routes
	mux := http.NewServeMux()

	// Start gaze tracker
	gaze := server.NewGazeTracker(cfg.Gaze.Enabled, time.Duration(cfg.Gaze.Timeout)*time.Second)
	gaze.Start()

	// API routes
	apiHandler := server.NewAPIHandler(store, alerter, cfg.Token, rateLimiter, gaze, cfg.Sites)
	apiHandler.RegisterRoutes(mux)

	// Static files (dashboard)
	mux.Handle("/", web.Handler(cfg.Token))

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

// runHostCommand implements the local `host` subcommands. They talk to the
// same SQLite file the server uses (WAL mode, so a running server is fine)
// and provide the only way to soft-delete a host: the dashboard stays as
// simple as it is, deleting is an explicit operator action.
func runHostCommand(cfg config.ServerConfig, args []string) {
	if args[0] != "host" {
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", args[0])
		hostUsage()
		os.Exit(2)
	}
	if len(args) < 2 {
		hostUsage()
		os.Exit(2)
	}

	store, err := server.NewStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database %s: %v", cfg.DBPath, err)
	}
	defer store.Close()

	switch args[1] {
	case "list":
		hostList(store)
	case "delete", "hide":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: simpleprobe-server -c <server.yml> host delete <name>")
			os.Exit(2)
		}
		hostDelete(store, args[2])
	case "restore", "unhide":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: simpleprobe-server -c <server.yml> host restore <name>")
			os.Exit(2)
		}
		hostRestore(store, args[2])
	default:
		hostUsage()
		os.Exit(2)
	}
}

// hostList prints every known host, hidden ones included.
func hostList(store *server.Store) {
	servers, err := store.ListAllServers()
	if err != nil {
		log.Fatalf("Failed to list hosts: %v", err)
	}
	if len(servers) == 0 {
		fmt.Println("暂无主机。")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tLAST_SEEN\tHIDDEN")
	for _, s := range servers {
		hidden := "no"
		if s.Hidden {
			hidden = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			s.Name, s.Status, s.LastSeen.Local().Format("2006-01-02 15:04:05"), hidden)
	}
	w.Flush()
}

// hostDelete soft-deletes a host.
func hostDelete(store *server.Store, name string) {
	ok, err := store.HideServer(name)
	if err != nil {
		log.Fatalf("Failed to hide host %q: %v", name, err)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "主机 %q 不存在。\n", name)
		os.Exit(1)
	}

	fmt.Printf("已隐藏主机 %s（软删除）。\n", name)
	fmt.Println("历史数据和告警记录都保留，看板与告警中不再出现这台主机。")
	fmt.Println("如果该机器的 agent 仍在运行，它不会重新出现在面板上。恢复请用：")
	fmt.Printf("  simpleprobe-server -c <server.yml> host restore %s\n", name)
}

// hostRestore brings a hidden host back.
func hostRestore(store *server.Store, name string) {
	ok, err := store.UnhideServer(name)
	if err != nil {
		log.Fatalf("Failed to restore host %q: %v", name, err)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "主机 %q 不存在。\n", name)
		os.Exit(1)
	}

	fmt.Printf("已恢复主机 %s，它会重新出现在看板中。\n", name)
}

// hostUsage prints the subcommand help.
func hostUsage() {
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  simpleprobe-server -c <server.yml> host list             列出全部主机（含已隐藏）")
	fmt.Fprintln(os.Stderr, "  simpleprobe-server -c <server.yml> host delete <name>    软删除主机（隐藏，保留历史）")
	fmt.Fprintln(os.Stderr, "  simpleprobe-server -c <server.yml> host restore <name>   恢复已隐藏的主机")
}
