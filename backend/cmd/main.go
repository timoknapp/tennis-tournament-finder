package main

import (
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/scheduler"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

func main() {
	logger.Info("Starting Tennis Tournament Finder backend server...")

	openstreetmap.InitCache()
	logger.Info("OpenStreetMap cache initialized")

	// Set up graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Shutting down gracefully...")
		openstreetmap.CloseCache()
		os.Exit(0)
	}()

	// Public API
	http.HandleFunc("/", tournament.GetTournaments)

	// In-process scheduler (fully optional; enable with env var)
	cfg := scheduler.FromEnv()
	if cfg.Enabled {
		s, err := scheduler.New(cfg)
		if err != nil {
			logger.Error("Failed to start scheduler: %v", err)
		} else {
			s.Start()
			logger.Info("Scheduler enabled")
		}
	} else {
		logger.Info("Scheduler disabled (set TTF_SCHEDULER_ENABLED=true to enable)")
	}

	logger.Info("Starting HTTP server on port 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Error("Server failed to start: %v", err)
	}
}
