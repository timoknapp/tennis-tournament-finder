package main

import (
	"context"
	"errors"
	"expvar"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/metrics"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/resultcache"
	"github.com/timoknapp/tennis-tournament-finder/pkg/scheduler"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

// Global variables for components that can be reloaded
var (
	globalScheduler   *scheduler.Scheduler
	globalSchedulerMu sync.Mutex
	reloadMu          sync.Mutex
	globalResultCache *resultcache.Cache
)

// newServer builds an HTTP server with defensive timeouts so slow or
// malicious clients cannot hold connections open indefinitely.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
}

// initResultCache wires up the tournament result cache.
//
// A failure to open the persistent store is not fatal: caching falls back to
// memory, which still spares the federations most of the load and only costs
// the cache contents on restart.
func initResultCache() {
	if !resultcache.Enabled() {
		logger.Info("Result cache disabled (TTF_RESULT_CACHE=false)")
		return
	}

	path := os.Getenv("TTF_RESULT_CACHE_PATH")
	if path == "" {
		path = "./data/results.bolt"
	}

	var store resultcache.Store
	boltStore, err := resultcache.NewBoltStore(path)
	if err != nil {
		logger.Error("Failed to open result cache at %s, falling back to memory: %v", path, err)
		store = resultcache.NewMemoryStore()
	} else {
		store = boltStore
	}

	opts := resultcache.OptionsFromEnv()
	cache := resultcache.New(store, opts)
	tournament.SetResultCache(cache)
	globalResultCache = cache

	// Expose cache contents through /stats so freshness is observable.
	metrics.SetResultCacheProvider(func() any {
		stats, err := cache.Stats()
		if err != nil {
			return map[string]string{"error": err.Error()}
		}
		return stats
	})

	logger.Info("Result cache enabled (path=%s, ttl=%s, stale=%s)", path, opts.TTL, opts.StaleTTL)
}

func main() {
	logger.Info("Starting Tennis Tournament Finder backend server...")

	openstreetmap.InitCache()
	logger.Info("OpenStreetMap cache initialized")

	initResultCache()

	// Lightweight metrics (no Prometheus required)
	metrics.Init()
	metrics.SetReloadCallback(ReloadComponents)

	// Start a localhost-only diagnostics server for /stats and /debug/vars
	diagMux := http.NewServeMux()
	diagMux.Handle(metrics.StatsPath, http.HandlerFunc(metrics.StatsHandler))
	diagMux.Handle(metrics.DebugVarsPath, expvar.Handler())
	diagMux.Handle(metrics.EnvPath, http.HandlerFunc(metrics.EnvHandler))
	// Clubs whose location could not be determined, with paste-ready overrides
	// for club-locations.json. Wrong pins are otherwise invisible: a tournament
	// sitting in the middle of a state looks like a working map.
	diagMux.Handle(metrics.UnresolvedClubsPath, http.HandlerFunc(metrics.UnresolvedClubsHandler))
	diagAddr := "127.0.0.1:9090"
	diagServer := newServer(diagAddr, diagMux)

	go func() {
		logger.Info("Starting diagnostics server on http://%s%s and http://%s%s", diagAddr, metrics.StatsPath, diagAddr, metrics.DebugVarsPath)
		if err := diagServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Diagnostics server failed to start on %s: %v", diagAddr, err)
			logger.Error("Diagnostics endpoints will NOT be available at http://%s%s, http://%s%s, or http://%s%s for this process lifetime", diagAddr, metrics.StatsPath, diagAddr, metrics.DebugVarsPath, diagAddr, metrics.EnvPath)
		}
	}()

	// Public API with instrumentation (served on :8080)
	apiMux := http.NewServeMux()
	apiMux.Handle("/", metrics.Instrument(http.HandlerFunc(tournament.GetTournaments)))
	apiServer := newServer(":8080", apiMux)

	// In-process scheduler (fully optional; enable with env var)
	cfg := scheduler.FromEnv()
	if cfg.Enabled {
		s, err := scheduler.New(cfg)
		if err != nil {
			logger.Error("Failed to start scheduler: %v", err)
		} else {
			s.Start()
			globalSchedulerMu.Lock()
			globalScheduler = s
			globalSchedulerMu.Unlock()
			logger.Info("Scheduler enabled")
		}
	} else {
		logger.Info("Scheduler disabled (set TTF_SCHEDULER_ENABLED=true to enable)")
	}

	// Set up graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Shutting down gracefully...")

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := apiServer.Shutdown(ctx); err != nil {
			logger.Error("API server shutdown error: %v", err)
		}
		if err := diagServer.Shutdown(ctx); err != nil {
			logger.Error("Diagnostics server shutdown error: %v", err)
		}

		globalSchedulerMu.Lock()
		if globalScheduler != nil {
			globalScheduler.Stop()
		}
		globalSchedulerMu.Unlock()

		if globalResultCache != nil {
			if err := globalResultCache.Close(); err != nil {
				logger.Error("Result cache shutdown error: %v", err)
			}
		}

		openstreetmap.CloseCache()
		os.Exit(0)
	}()

	logger.Info("Starting HTTP server on port 8080...")
	if err := apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("Server failed to start: %v", err)
	}
}

// ReloadComponents reloads application components after environment variable changes
func ReloadComponents() error {
	// Serialize reload operations to prevent concurrent execution
	reloadMu.Lock()
	defer reloadMu.Unlock()

	logger.Info("Reloading application components...")

	// Reload log level
	if logLevel := os.Getenv("TTF_LOG_LEVEL"); logLevel != "" {
		level := logger.ParseLogLevel(logLevel)
		logger.SetLogLevel(level)
		logger.Info("Log level updated to: %s", logLevel)
	}

	// Reload scheduler configuration
	globalSchedulerMu.Lock()
	cfg := scheduler.FromEnv()
	currentScheduler := globalScheduler
	globalSchedulerMu.Unlock()

	if cfg.Enabled {
		if currentScheduler == nil {
			// Scheduler was disabled, now enable it
			s, err := scheduler.New(cfg)
			if err != nil {
				logger.Error("Failed to start scheduler during reload: %v", err)
				return err
			}
			s.Start()
			globalSchedulerMu.Lock()
			globalScheduler = s
			globalSchedulerMu.Unlock()
			logger.Info("Scheduler enabled during configuration reload")
		} else {
			// Scheduler exists, reload its configuration
			if err := currentScheduler.Reload(); err != nil {
				logger.Error("Failed to reload scheduler configuration: %v", err)
				return err
			}
		}
	} else {
		// Scheduler should be disabled
		if currentScheduler != nil {
			currentScheduler.Stop()
			globalSchedulerMu.Lock()
			globalScheduler = nil
			globalSchedulerMu.Unlock()
			logger.Info("Scheduler disabled during configuration reload")
		}
	}

	logger.Info("Component reload completed successfully")
	return nil
}
