package scheduler

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

type Config struct {
	Enabled     bool
	CronSpec    string // e.g. "0 2 * * *" (server local time)
	CompType    string // optional, e.g. "Herren+Einzel"
	Federations string // optional, comma-separated IDs, empty = all
	// WarmupDays is how far ahead the scheduled run pre-fetches. It should
	// cover the range users actually browse, otherwise their requests miss the
	// cache and scrape live anyway.
	WarmupDays int
}

type Scheduler struct {
	mu     sync.RWMutex
	c      *cron.Cron
	config Config
}

func FromEnv() Config {
	return Config{
		Enabled:     os.Getenv("TTF_SCHEDULER_ENABLED") == "true" || os.Getenv("TTF_SCHEDULER_ENABLED") == "1",
		CronSpec:    firstNonEmpty(os.Getenv("TTF_SCHEDULER_CRON"), "0 2 * * *"),
		CompType:    os.Getenv("TTF_SCHEDULER_COMP_TYPE"),
		Federations: os.Getenv("TTF_SCHEDULER_FEDERATIONS"),
		WarmupDays:  warmupDaysFromEnv(),
	}
}

// defaultWarmupDays covers the typical browsing window.
const defaultWarmupDays = 30

func warmupDaysFromEnv() int {
	raw := os.Getenv("TTF_SCHEDULER_WARMUP_DAYS")
	if raw == "" {
		return defaultWarmupDays
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		return defaultWarmupDays
	}
	return days
}

// runWarmup pre-fetches the configured window into the result cache.
func runWarmup(cfg Config) {
	days := cfg.WarmupDays
	if days <= 0 {
		days = defaultWarmupDays
	}

	now := time.Now()
	dateFrom := now.Format("02.01.2006")
	dateTo := now.AddDate(0, 0, days).Format("02.01.2006")

	logger.Info("Scheduler tick: warming cache for %s..%s", dateFrom, dateTo)
	total := tournament.Warmup(dateFrom, dateTo, cfg.CompType, cfg.Federations)
	logger.Info("Scheduler warmup done, tournaments fetched: %d", total)
}

func New(cfg Config) (*Scheduler, error) {
	s := &Scheduler{
		c:      cron.New(), // standard 5-field spec, runs in server local time
		config: cfg,
	}
	_, err := s.c.AddFunc(cfg.CronSpec, func() {
		runWarmup(cfg)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Scheduler) Start() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	logger.Info("Starting scheduler (cron=%s, compType=%s, federations=%s)",
		s.config.CronSpec, s.config.CompType, s.config.Federations)
	s.c.Start()
}

func (s *Scheduler) Stop() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.c.Stop()
}

// Reload updates the scheduler configuration from environment variables and restarts if necessary
func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	newConfig := FromEnv()

	// If configuration hasn't changed, no need to restart
	if s.config.CronSpec == newConfig.CronSpec &&
		s.config.CompType == newConfig.CompType &&
		s.config.Federations == newConfig.Federations &&
		s.config.WarmupDays == newConfig.WarmupDays &&
		s.config.Enabled == newConfig.Enabled {
		logger.Info("Scheduler configuration unchanged, no restart needed")
		return nil
	}

	// Keep backup of old cron and config in case reload fails
	oldCron := s.c
	oldConfig := s.config

	// Stop current scheduler
	oldCron.Stop()
	logger.Info("Stopped scheduler for configuration reload")

	// Create new cron scheduler with updated config only if enabled
	if newConfig.Enabled {
		newCron := cron.New()
		_, err := newCron.AddFunc(newConfig.CronSpec, func() {
			runWarmup(newConfig)
		})
		if err != nil {
			// Restore old scheduler on error
			s.c = oldCron
			s.config = oldConfig
			oldCron.Start()
			logger.Error("Failed to reload scheduler, restored previous configuration: %v", err)
			return err
		}
		newCron.Start()
		logger.Info("Scheduler restarted with new configuration (cron=%s, compType=%s, federations=%s)",
			newConfig.CronSpec, newConfig.CompType, newConfig.Federations)

		// Update to new configuration
		s.c = newCron
		s.config = newConfig
	} else {
		// Create a new stopped cron instance to keep state clean
		s.c = cron.New()
		s.config = newConfig
		logger.Info("Scheduler disabled via configuration reload")
	}

	return nil
}

// GetConfig returns the current scheduler configuration
func (s *Scheduler) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
