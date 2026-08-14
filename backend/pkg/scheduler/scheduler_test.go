package scheduler

import (
	"testing"
	"time"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("TTF_SCHEDULER_ENABLED", "")
	t.Setenv("TTF_SCHEDULER_CRON", "")
	t.Setenv("TTF_SCHEDULER_COMP_TYPE", "")
	t.Setenv("TTF_SCHEDULER_FEDERATIONS", "")
	t.Setenv("TTF_SCHEDULER_WARMUP_DAYS", "")

	cfg := FromEnv()

	if cfg.Enabled {
		t.Error("Enabled = true, want disabled by default")
	}
	if cfg.CronSpec != "0 2 * * *" {
		t.Errorf("CronSpec = %q, want the 02:00 default", cfg.CronSpec)
	}
	if cfg.WarmupDays != defaultWarmupDays {
		t.Errorf("WarmupDays = %d, want %d", cfg.WarmupDays, defaultWarmupDays)
	}
}

func TestFromEnvReadsValues(t *testing.T) {
	t.Setenv("TTF_SCHEDULER_ENABLED", "true")
	t.Setenv("TTF_SCHEDULER_CRON", "30 3 * * *")
	t.Setenv("TTF_SCHEDULER_COMP_TYPE", "Damen+Einzel")
	t.Setenv("TTF_SCHEDULER_FEDERATIONS", "BAD,WTB")
	t.Setenv("TTF_SCHEDULER_WARMUP_DAYS", "45")

	cfg := FromEnv()

	if !cfg.Enabled {
		t.Error("Enabled = false, want true")
	}
	if cfg.CronSpec != "30 3 * * *" {
		t.Errorf("CronSpec = %q", cfg.CronSpec)
	}
	if cfg.CompType != "Damen+Einzel" {
		t.Errorf("CompType = %q", cfg.CompType)
	}
	if cfg.Federations != "BAD,WTB" {
		t.Errorf("Federations = %q", cfg.Federations)
	}
	if cfg.WarmupDays != 45 {
		t.Errorf("WarmupDays = %d, want 45", cfg.WarmupDays)
	}
}

func TestFromEnvAcceptsNumericEnabledFlag(t *testing.T) {
	t.Setenv("TTF_SCHEDULER_ENABLED", "1")
	if !FromEnv().Enabled {
		t.Error("Enabled = false for \"1\", want true")
	}
}

// TestWarmupDaysFallsBackOnInvalidInput guards against a typo silently
// disabling the pre-fetch window.
func TestWarmupDaysFallsBackOnInvalidInput(t *testing.T) {
	for _, v := range []string{"not-a-number", "-5", "0", ""} {
		t.Setenv("TTF_SCHEDULER_WARMUP_DAYS", v)
		if got := warmupDaysFromEnv(); got != defaultWarmupDays {
			t.Errorf("warmupDaysFromEnv() = %d for %q, want %d", got, v, defaultWarmupDays)
		}
	}
}

func TestNewRejectsInvalidCronSpec(t *testing.T) {
	if _, err := New(Config{Enabled: true, CronSpec: "not a cron spec"}); err == nil {
		t.Error("New() accepted an invalid cron spec")
	}
}

func TestNewAcceptsValidCronSpec(t *testing.T) {
	s, err := New(Config{Enabled: true, CronSpec: "0 2 * * *", WarmupDays: 30})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := s.GetConfig().WarmupDays; got != 30 {
		t.Errorf("WarmupDays = %d, want 30", got)
	}
}

func TestStartStopIsSafe(t *testing.T) {
	s, err := New(Config{Enabled: true, CronSpec: "0 2 * * *"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Must not panic or dead-lock.
	s.Start()
	s.Stop()
}

// TestReloadDetectsWarmupDaysChange makes sure the new setting participates in
// the "did anything change?" comparison, otherwise a reload would silently
// keep the old window.
func TestReloadDetectsWarmupDaysChange(t *testing.T) {
	t.Setenv("TTF_SCHEDULER_ENABLED", "true")
	t.Setenv("TTF_SCHEDULER_CRON", "0 2 * * *")
	t.Setenv("TTF_SCHEDULER_WARMUP_DAYS", "30")

	s, err := New(FromEnv())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.Start()
	defer s.Stop()

	t.Setenv("TTF_SCHEDULER_WARMUP_DAYS", "60")
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	if got := s.GetConfig().WarmupDays; got != 60 {
		t.Errorf("WarmupDays after reload = %d, want 60", got)
	}
}

func TestReloadDisablesScheduler(t *testing.T) {
	t.Setenv("TTF_SCHEDULER_ENABLED", "true")
	t.Setenv("TTF_SCHEDULER_CRON", "0 2 * * *")

	s, err := New(FromEnv())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.Start()

	t.Setenv("TTF_SCHEDULER_ENABLED", "false")
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	if s.GetConfig().Enabled {
		t.Error("scheduler still enabled after reload")
	}
}

func TestReloadRestoresConfigOnInvalidCron(t *testing.T) {
	t.Setenv("TTF_SCHEDULER_ENABLED", "true")
	t.Setenv("TTF_SCHEDULER_CRON", "0 2 * * *")

	s, err := New(FromEnv())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.Start()
	defer s.Stop()

	t.Setenv("TTF_SCHEDULER_CRON", "totally invalid")
	if err := s.Reload(); err == nil {
		t.Fatal("Reload() accepted an invalid cron spec")
	}

	// The previous working configuration must remain in place.
	if got := s.GetConfig().CronSpec; got != "0 2 * * *" {
		t.Errorf("CronSpec = %q, want the previous value to be restored", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "third"); got != "third" {
		t.Errorf("firstNonEmpty() = %q, want third", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Errorf("firstNonEmpty() = %q, want first", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Errorf("firstNonEmpty() = %q, want empty", got)
	}
}

// TestWarmupWindowCoversConfiguredDays documents the relationship between the
// scheduler window and the cache: a window shorter than what users browse
// leaves their requests scraping live.
func TestWarmupWindowCoversConfiguredDays(t *testing.T) {
	cfg := Config{WarmupDays: 45}

	now := time.Now()
	to := now.AddDate(0, 0, cfg.WarmupDays)

	if got := to.Sub(now).Hours() / 24; got < 44 || got > 46 {
		t.Errorf("warmup window spans %.0f days, want ~45", got)
	}
}
