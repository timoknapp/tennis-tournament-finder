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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}