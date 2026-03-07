package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kingyoung/bbsit/internal/config"
	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/scheduler"
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

	// Create deployer and scheduler
	dep := deployer.New(database, logger)
	sched := scheduler.New(database, dep, logger)

	// Start scheduler
	sched.Start()
	defer sched.Stop()

	// Create web server
	srv := web.NewServer(database, dep, sched, logger, cfg.StackRoot)

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

	// Wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	logger.Info("shutting down", "signal", s)
}
