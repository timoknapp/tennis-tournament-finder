package scheduler

import (
	"os"

	"github.com/robfig/cron/v3"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

type Config struct {
	Enabled     bool
	CronSpec    string // e.g. "0 2 * * *" (server local time)
	CompType    string // optional, e.g. "Herren+Einzel"
	Federations string // optional, comma-separated IDs, empty = all
}

type Scheduler struct {
	c      *cron.Cron
	config Config
}

func FromEnv() Config {
	return Config{
		Enabled:     os.Getenv("TTF_SCHEDULER_ENABLED") == "true" || os.Getenv("TTF_SCHEDULER_ENABLED") == "1",
		CronSpec:    firstNonEmpty(os.Getenv("TTF_SCHEDULER_CRON"), "0 2 * * *"),
		CompType:    os.Getenv("TTF_SCHEDULER_COMP_TYPE"),
		Federations: os.Getenv("TTF_SCHEDULER_FEDERATIONS"),
	}
}

func New(cfg Config) (*Scheduler, error) {
	s := &Scheduler{
		c:      cron.New(), // standard 5-field spec, runs in server local time
		config: cfg,
	}
	_, err := s.c.AddFunc(cfg.CronSpec, func() {
		logger.Info("Scheduler tick: running warmup job")
		total := tournament.Warmup("", "", cfg.CompType, cfg.Federations)
		logger.Info("Scheduler warmup done, tournaments fetched: %d", total)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Scheduler) Start() {
	logger.Info("Starting scheduler (cron=%s, compType=%s, federations=%s)",
		s.config.CronSpec, s.config.CompType, s.config.Federations)
	s.c.Start()
}

func (s *Scheduler) Stop() {
	s.c.Stop()
}

// Reload updates the scheduler configuration from environment variables and restarts if necessary
func (s *Scheduler) Reload() error {
	newConfig := FromEnv()
	
	// If configuration hasn't changed, no need to restart
	if s.config.CronSpec == newConfig.CronSpec &&
		s.config.CompType == newConfig.CompType &&
		s.config.Federations == newConfig.Federations &&
		s.config.Enabled == newConfig.Enabled {
		logger.Info("Scheduler configuration unchanged, no restart needed")
		return nil
	}
	
	// Stop current scheduler
	s.c.Stop()
	logger.Info("Stopped scheduler for configuration reload")
	
	// Update configuration
	s.config = newConfig
	
	// Create new cron scheduler with updated config
	s.c = cron.New()
	if newConfig.Enabled {
		_, err := s.c.AddFunc(newConfig.CronSpec, func() {
			logger.Info("Scheduler tick: running warmup job")
			total := tournament.Warmup("", "", newConfig.CompType, newConfig.Federations)
			logger.Info("Scheduler warmup done, tournaments fetched: %d", total)
		})
		if err != nil {
			return err
		}
		s.c.Start()
		logger.Info("Scheduler restarted with new configuration (cron=%s, compType=%s, federations=%s)",
			newConfig.CronSpec, newConfig.CompType, newConfig.Federations)
	} else {
		logger.Info("Scheduler disabled via configuration reload")
	}
	
	return nil
}

// GetConfig returns the current scheduler configuration
func (s *Scheduler) GetConfig() Config {
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