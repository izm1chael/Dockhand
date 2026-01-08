package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const invalidEnvFmt = "invalid %s: %w"

// ApplyEnvOverrides reads configuration values from environment variables and
// overrides fields in the provided Config. Returns an error if parsing fails.
//
// Environment variables supported:
// - DOCKHAND_POLL_INTERVAL (duration, e.g. "10m")
// - DOCKHAND_PATCH_WINDOW (string, e.g. "00:00-02:00")
// - DOCKHAND_MANAGE_LATEST_ONLY (bool, e.g. "true")
// - DOCKHAND_OPT_OUT (bool, e.g. "true")
// - DOCKHAND_METRICS_ENABLED (bool, "true"/"false")
// - DOCKHAND_METRICS_PORT (int, e.g. 9090)
// - DOCKHAND_INFLUX_URL (string, e.g. http://localhost:8086)
// - DOCKHAND_INFLUX_TOKEN (string)
// - DOCKHAND_INFLUX_ORG (string)
// - DOCKHAND_INFLUX_BUCKET (string)
// - DOCKHAND_INFLUX_INTERVAL (duration, e.g. "1m")
func ApplyEnvOverrides(cfg *Config) error {
	// Basic fields and booleans
	if err := applyBasicEnv(cfg); err != nil {
		return err
	}

	// Notifications
	if err := applyNotificationEnv(cfg); err != nil {
		return err
	}

	// Email
	if err := applyEmailEnv(cfg); err != nil {
		return err
	}

	// Metrics
	if err := applyMetricsEnv(cfg); err != nil {
		return err
	}

	// Influx
	if err := applyInfluxEnv(cfg); err != nil {
		return err
	}

	// Misc (dry-run, registry, gotify, pushover, notification level)
	if err := applyMiscEnv(cfg); err != nil {
		return err
	}

	return nil
}

// applyBasicEnv consolidates basic fields and boolean env parsing
func applyBasicEnv(cfg *Config) error {
	if err := setDurationEnv("DOCKHAND_POLL_INTERVAL", func(d time.Duration) { cfg.PollInterval = d }); err != nil {
		return err
	}
	setStringEnv("DOCKHAND_PATCH_WINDOW", func(s string) { cfg.PatchWindow = s })

	if err := setBoolEnv("DOCKHAND_MANAGE_LATEST_ONLY", func(b bool) { cfg.ManageLatestOnly = b }); err != nil {
		return err
	}
	if err := setBoolEnv("DOCKHAND_OPT_OUT", func(b bool) { cfg.OptOut = b }); err != nil {
		return err
	}
	if err := setBoolEnv("DOCKHAND_SANITIZE_NAMES", func(b bool) { cfg.SanitizeNames = b }); err != nil {
		return err
	}
	if v := os.Getenv("DOCKHAND_DOCKER_HOSTS"); v != "" {
		parts := strings.Split(v, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		cfg.DockerHosts = parts
	}
	return nil
}

// applyMiscEnv handles dry-run, registry auth, gotify, pushover, and notification level
func applyMiscEnv(cfg *Config) error {
	if err := applyDryRunAndRegistry(cfg); err != nil {
		return err
	}
	if err := applyPushNotifications(cfg); err != nil {
		return err
	}
	if err := applyRuntimeFlags(cfg); err != nil {
		return err
	}
	return nil
}

func applyDryRunAndRegistry(cfg *Config) error {
	if v := os.Getenv("DOCKHAND_DRY_RUN"); v == "true" {
		cfg.DryRun = true
	}
	if v := os.Getenv("DOCKHAND_REGISTRY_USER"); v != "" {
		cfg.RegistryUser = v
	}
	if v := os.Getenv("DOCKHAND_REGISTRY_PASS"); v != "" {
		cfg.RegistryPass = v
	}
	return nil
}

func applyPushNotifications(cfg *Config) error {
	if v := os.Getenv("DOCKHAND_GOTIFY_URL"); v != "" {
		cfg.GotifyURL = v
	}
	if v := os.Getenv("DOCKHAND_GOTIFY_TOKEN"); v != "" {
		cfg.GotifyToken = v
	}
	if v := os.Getenv("DOCKHAND_PUSHOVER_USER"); v != "" {
		cfg.PushoverUser = v
	}
	if v := os.Getenv("DOCKHAND_PUSHOVER_TOKEN"); v != "" {
		cfg.PushoverToken = v
	}
	return nil
}

func applyRuntimeFlags(cfg *Config) error {
	setStringEnv("DOCKHAND_NOTIFICATION_LEVEL", func(s string) { cfg.NotificationLevel = s })
	if err := setIntEnv("DOCKHAND_CIRCUIT_BREAKER_THRESHOLD", func(n int) { cfg.CircuitBreakerThreshold = n }); err != nil {
		return err
	}
	if err := setDurationEnv("DOCKHAND_CIRCUIT_BREAKER_COOLDOWN", func(d time.Duration) { cfg.CircuitBreakerCooldown = d }); err != nil {
		return err
	}
	setStringEnv("DOCKHAND_HOST_SOCKET_PATH", func(s string) { cfg.HostSocketPath = s })
	if err := setBoolEnv("DOCKHAND_PIN_DIGESTS", func(b bool) { cfg.PinDigests = b }); err != nil {
		return err
	}
	if err := setIntEnv("DOCKHAND_MAX_CONCURRENT_UPDATES", func(n int) {
		if n > 0 {
			cfg.MaxConcurrentUpdates = n
		}
	}); err != nil {
		return err
	}
	return nil
}

// setBoolEnv is a small helper to parse boolean environment variables
func setBoolEnv(env string, setter func(bool)) error {
	if v := os.Getenv(env); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf(invalidEnvFmt, env, err)
		}
		setter(b)
	}
	return nil
}

// applyNotificationEnv consolidates notification-related env parsing
func applyNotificationEnv(cfg *Config) error {
	setStringEnv("DOCKHAND_DISCORD_WEBHOOK", func(s string) { cfg.DiscordWebhook = s })
	setStringEnv("DOCKHAND_SLACK_WEBHOOK", func(s string) { cfg.SlackWebhook = s })
	setStringEnv("DOCKHAND_TEAMS_WEBHOOK", func(s string) { cfg.TeamsWebhook = s })
	setStringEnv("DOCKHAND_TELEGRAM_TOKEN", func(s string) { cfg.TelegramToken = s })
	setStringEnv("DOCKHAND_TELEGRAM_CHAT_ID", func(s string) { cfg.TelegramChatID = s })
	setStringEnv("DOCKHAND_MASTODON_SERVER", func(s string) { cfg.MastodonServer = s })
	setStringEnv("DOCKHAND_MASTODON_TOKEN", func(s string) { cfg.MastodonToken = s })
	setStringEnv("DOCKHAND_GENERIC_WEBHOOK_URL", func(s string) { cfg.GenericWebhookURL = s })
	setStringEnv("DOCKHAND_APPRISE_URL", func(s string) { cfg.AppriseURL = s })
	if err := setDurationEnv("DOCKHAND_HOOK_TIMEOUT", func(d time.Duration) { cfg.HookTimeout = d }); err != nil {
		return err
	}
	return nil
}

// setStringEnv assigns an environment variable string to a setter if non-empty
func setStringEnv(env string, setter func(string)) {
	if v := os.Getenv(env); v != "" {
		setter(v)
	}
}

// setDurationEnv parses a duration env var and assigns it via setter if present
func setDurationEnv(env string, setter func(time.Duration)) error {
	if v := os.Getenv(env); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf(invalidEnvFmt, env, err)
		}
		setter(d)
	}
	return nil
}

// setIntEnv parses an int env var and assigns it via setter if present
func setIntEnv(env string, setter func(int)) error {
	if v := os.Getenv(env); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf(invalidEnvFmt, env, err)
		}
		setter(n)
	}
	return nil
}

// applyEmailEnv consolidates email-related env parsing
func applyEmailEnv(cfg *Config) error {
	if v := os.Getenv("DOCKHAND_EMAIL_HOST"); v != "" {
		cfg.EmailHost = v
	}
	if v := os.Getenv("DOCKHAND_EMAIL_USER"); v != "" {
		cfg.EmailUser = v
	}
	if v := os.Getenv("DOCKHAND_EMAIL_PASS"); v != "" {
		cfg.EmailPass = v
	}
	if v := os.Getenv("DOCKHAND_EMAIL_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DOCKHAND_EMAIL_PORT: %w", err)
		}
		cfg.EmailPort = p
	}
	if v := os.Getenv("DOCKHAND_EMAIL_TO"); v != "" {
		parts := strings.Split(v, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		cfg.EmailTo = parts
	}
	return nil
}

// applyMetricsEnv consolidates metrics-related env parsing
func applyMetricsEnv(cfg *Config) error {
	if v := os.Getenv("DOCKHAND_METRICS_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "true":
			cfg.MetricsEnabled = true
		case "false":
			cfg.MetricsEnabled = false
		}
	}
	if v := os.Getenv("DOCKHAND_METRICS_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DOCKHAND_METRICS_PORT: %w", err)
		}
		cfg.MetricsPort = p
	}
	return nil
}

// applyInfluxEnv consolidates Influx-related env parsing
func applyInfluxEnv(cfg *Config) error {
	if v := os.Getenv("DOCKHAND_INFLUX_URL"); v != "" {
		cfg.InfluxURL = v
	}
	if v := os.Getenv("DOCKHAND_INFLUX_TOKEN"); v != "" {
		cfg.InfluxToken = v
	}
	if v := os.Getenv("DOCKHAND_INFLUX_ORG"); v != "" {
		cfg.InfluxOrg = v
	}
	if v := os.Getenv("DOCKHAND_INFLUX_BUCKET"); v != "" {
		cfg.InfluxBucket = v
	}
	if v := os.Getenv("DOCKHAND_INFLUX_INTERVAL"); v != "" {
		dur, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid DOCKHAND_INFLUX_INTERVAL: %w", err)
		}
		cfg.InfluxInterval = dur
	}
	return nil
}
