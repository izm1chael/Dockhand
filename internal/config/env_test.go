package config

import (
	"os"
	"testing"
	"time"
)

func TestApplyEnvOverrides(t *testing.T) {
	cleanup := applyEnvSetup(t)
	defer cleanup()

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	validateAppliedEnvOverrides(t, cfg)
}

func applyEnvSetup(t *testing.T) func() {
	t.Helper()
	os.Setenv("DOCKHAND_POLL_INTERVAL", "2m")
	os.Setenv("DOCKHAND_PATCH_WINDOW", "01:00-03:00")
	os.Setenv("DOCKHAND_MANAGE_LATEST_ONLY", "false")
	os.Setenv("DOCKHAND_OPT_OUT", "false")
	os.Setenv("DOCKHAND_METRICS_ENABLED", "true")
	os.Setenv("DOCKHAND_METRICS_PORT", "9100")
	os.Setenv("DOCKHAND_INFLUX_URL", "http://influx:8086")
	os.Setenv("DOCKHAND_INFLUX_BUCKET", "b")
	os.Setenv("DOCKHAND_INFLUX_ORG", "o")
	os.Setenv("DOCKHAND_INFLUX_TOKEN", "t")
	os.Setenv("DOCKHAND_INFLUX_INTERVAL", "30s")
	// apprise
	os.Setenv("DOCKHAND_APPRISE_URL", "https://apprise.example/send")
	return func() {
		os.Unsetenv("DOCKHAND_POLL_INTERVAL")
		os.Unsetenv("DOCKHAND_PATCH_WINDOW")
		os.Unsetenv("DOCKHAND_MANAGE_LATEST_ONLY")
		os.Unsetenv("DOCKHAND_OPT_OUT")
		os.Unsetenv("DOCKHAND_METRICS_ENABLED")
		os.Unsetenv("DOCKHAND_METRICS_PORT")
		os.Unsetenv("DOCKHAND_INFLUX_URL")
		os.Unsetenv("DOCKHAND_INFLUX_BUCKET")
		os.Unsetenv("DOCKHAND_INFLUX_ORG")
		os.Unsetenv("DOCKHAND_INFLUX_TOKEN")
		os.Unsetenv("DOCKHAND_INFLUX_INTERVAL")
		os.Unsetenv("DOCKHAND_APPRISE_URL")
	}
}

func validateAppliedEnvOverrides(t *testing.T, cfg *Config) {
	t.Helper()
	if cfg.PollInterval != 2*time.Minute {
		t.Fatalf("expected poll 2m, got %v", cfg.PollInterval)
	}
	if cfg.PatchWindow != "01:00-03:00" {
		t.Fatalf("unexpected patch window: %s", cfg.PatchWindow)
	}
	if cfg.ManageLatestOnly != false {
		t.Fatalf("unexpected ManageLatestOnly: %v", cfg.ManageLatestOnly)
	}
	if cfg.OptOut != false {
		t.Fatalf("unexpected OptOut: %v", cfg.OptOut)
	}
	if !cfg.MetricsEnabled {
		t.Fatalf("expected metrics enabled")
	}
	if cfg.MetricsPort != 9100 {
		t.Fatalf("expected metrics port 9100, got %d", cfg.MetricsPort)
	}
	if cfg.InfluxURL != "http://influx:8086" {
		t.Fatalf("unexpected influx url: %s", cfg.InfluxURL)
	}
	if cfg.InfluxBucket != "b" || cfg.InfluxOrg != "o" || cfg.InfluxToken != "t" {
		t.Fatalf("unexpected influx config: %v", cfg)
	}
	if cfg.InfluxInterval != 30*time.Second {
		t.Fatalf("unexpected influx interval: %v", cfg.InfluxInterval)
	}
	if cfg.AppriseURL != "https://apprise.example/send" {
		t.Fatalf("unexpected apprise url: %v", cfg.AppriseURL)
	}
}
