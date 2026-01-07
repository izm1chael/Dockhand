package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

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
	if v := os.Getenv("DOCKHAND_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid DOCKHAND_POLL_INTERVAL: %w", err)
		}
		cfg.PollInterval = d
	}
	if v := os.Getenv("DOCKHAND_PATCH_WINDOW"); v != "" {
		cfg.PatchWindow = v
	}

	if err := setBoolEnv("DOCKHAND_MANAGE_LATEST_ONLY", func(b bool) { cfg.ManageLatestOnly = b }); err != nil {
		return err
	}
	if err := setBoolEnv("DOCKHAND_OPT_OUT", func(b bool) { cfg.OptOut = b }); err != nil {
		return err
	}
	if err := setBoolEnv("DOCKHAND_SANITIZE_NAMES", func(b bool) { cfg.SanitizeNames = b }); err != nil {
		return err
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
	if v := os.Getenv("DOCKHAND_NOTIFICATION_LEVEL"); v != "" {
		cfg.NotificationLevel = v
	}
	if v := os.Getenv("DOCKHAND_CIRCUIT_BREAKER_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DOCKHAND_CIRCUIT_BREAKER_THRESHOLD: %w", err)
		}
		cfg.CircuitBreakerThreshold = n
	}
	if v := os.Getenv("DOCKHAND_CIRCUIT_BREAKER_COOLDOWN"); v != "" {
		dur, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid DOCKHAND_CIRCUIT_BREAKER_COOLDOWN: %w", err)
		}
		cfg.CircuitBreakerCooldown = dur
	}
	if v := os.Getenv("DOCKHAND_HOST_SOCKET_PATH"); v != "" {
		cfg.HostSocketPath = v
	}
	if err := setBoolEnv("DOCKHAND_PIN_DIGESTS", func(b bool) { cfg.PinDigests = b }); err != nil {
		return err
	}
	return nil
}

// setBoolEnv is a small helper to parse boolean environment variables
func setBoolEnv(env string, setter func(bool)) error {
	if v := os.Getenv(env); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", env, err)
		}
		setter(b)
	}
	return nil
}

// applyNotificationEnv consolidates notification-related env parsing
func applyNotificationEnv(cfg *Config) error {
	if v := os.Getenv("DOCKHAND_DISCORD_WEBHOOK"); v != "" {
		cfg.DiscordWebhook = v
	}
	if v := os.Getenv("DOCKHAND_SLACK_WEBHOOK"); v != "" {
		cfg.SlackWebhook = v
	}
	if v := os.Getenv("DOCKHAND_TEAMS_WEBHOOK"); v != "" {
		cfg.TeamsWebhook = v
	}
	if v := os.Getenv("DOCKHAND_TELEGRAM_TOKEN"); v != "" {
		cfg.TelegramToken = v
	}
	if v := os.Getenv("DOCKHAND_TELEGRAM_CHAT_ID"); v != "" {
		cfg.TelegramChatID = v
	}
	if v := os.Getenv("DOCKHAND_MASTODON_SERVER"); v != "" {
		cfg.MastodonServer = v
	}
	if v := os.Getenv("DOCKHAND_MASTODON_TOKEN"); v != "" {
		cfg.MastodonToken = v
	}
	if v := os.Getenv("DOCKHAND_GENERIC_WEBHOOK_URL"); v != "" {
		cfg.GenericWebhookURL = v
	}
	if v := os.Getenv("DOCKHAND_APPRISE_URL"); v != "" {
		cfg.AppriseURL = v
	}
	if v := os.Getenv("DOCKHAND_HOOK_TIMEOUT"); v != "" {
		dur, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid DOCKHAND_HOOK_TIMEOUT: %w", err)
		}
		cfg.HookTimeout = dur
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
