package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kingyoung/bbsit/internal/backup"
	"github.com/kingyoung/bbsit/internal/config"
	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	bbruntime "github.com/kingyoung/bbsit/internal/runtime"
	"github.com/kingyoung/bbsit/internal/scheduler"
	"github.com/kingyoung/bbsit/internal/tunnel"
	"github.com/kingyoung/bbsit/internal/web"
)

func main() {
	configPath := flag.String("config", "/opt/bbsit/config.yaml", "path to bbsit config file")
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bbsit config: %v\n", err)
		os.Exit(1)
	}

	// Setup logger
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Clear stale deploying states from any previous crash/restart
	if err := database.ResetStaleStates(); err != nil {
		logger.Error("reset stale states", "error", err)
		os.Exit(1)
	}
	if err := database.ResetStaleBackups(); err != nil {
		logger.Error("reset stale backups", "error", err)
		os.Exit(1)
	}

	// Resolve container runtime (docker or podman)
	runtime, err := cfg.ResolvedRuntime()
	if err != nil {
		logger.Error("container runtime", "error", err)
		os.Exit(1)
	}
	logger.Info("using container runtime", "runtime", runtime)

	// Create deployer and scheduler
	dep := deployer.New(database, logger, runtime)
	sched := scheduler.New(database, dep, logger, runtime)

	// Tunnel manager — owns cloudflared system projects + ingress reconciliation
	tm := tunnel.NewManager(database, dep, logger, cfg.StackRoot, runtime)

	// Backup service — orchestrates application-aware backup/restore via compose exec.
	bk := backup.New(database, bbruntime.New(runtime), logger)

	// Start scheduler
	sched.Start()
	defer sched.Stop()

	// Reconcile any existing tunnels at startup so cloudflared system projects come up
	go func() {
		if err := tm.ReconcileAll(); err != nil {
			logger.Error("startup tunnel reconcile", "error", err)
		}
	}()

	// Create web server
	srv := web.NewServer(database, dep, sched, tm, bk, logger, cfg.StackRoot)

	// Start HTTP server
	httpServer := &http.Server{
		Addr:    cfg.Listen,
		Handler: srv.Handler(),
	}

	go func() {
		logger.Info("web UI listening", "addr", cfg.Listen)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("http server", "error", err)
			os.Exit(1)
		}
	}()

	// Admin Unix socket for bbsit-ctl
	socketCtx, cancelSocket := context.WithCancel(context.Background())
	defer cancelSocket()
	if cfg.AdminSocket != "" {
		if err := srv.ServeAdminSocket(socketCtx, cfg.AdminSocket, cfg.AdminGroup, 0); err != nil {
			logger.Error("admin socket", "error", err)
			os.Exit(1)
		}
	}

	// Wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	logger.Info("shutting down", "signal", s)
	cancelSocket()
}
