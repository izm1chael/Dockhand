package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds runtime configuration for Dockhand
type Config struct {
	PollInterval     time.Duration `json:"poll_interval" yaml:"poll_interval"`
	PatchWindow      string        `json:"patch_window" yaml:"patch_window"`
	ManageLatestOnly bool          `json:"manage_latest_only" yaml:"manage_latest_only"`
	OptOut           bool          `json:"opt_out" yaml:"opt_out"`
	VerifyTimeout    time.Duration `json:"verify_timeout" yaml:"verify_timeout"`
	VerifyInterval   time.Duration `json:"verify_interval" yaml:"verify_interval"`
	VerifyCommand    []string      `json:"verify_command" yaml:"verify_command"`
	// Timeout for any container-level pre-update hook (duration string, e.g. "30s" or "5m")
	HookTimeout time.Duration `json:"hook_timeout" yaml:"hook_timeout"`
	// Circuit breaker triggers to avoid notification storms (number of failures within cooldown)
	CircuitBreakerThreshold int           `json:"circuit_breaker_threshold" yaml:"circuit_breaker_threshold"`
	CircuitBreakerCooldown  time.Duration `json:"circuit_breaker_cooldown" yaml:"circuit_breaker_cooldown"`
	// Should Dockhand sanitize container names (strip disallowed chars and lowercase)
	SanitizeNames bool `json:"sanitize_names" yaml:"sanitize_names"`

	// Notification configuration
	NotificationLevel string `json:"notification_level" yaml:"notification_level"` // "all", "failure", "none"

	// Metrics
	MetricsEnabled bool `json:"metrics_enabled" yaml:"metrics_enabled"`
	MetricsPort    int  `json:"metrics_port" yaml:"metrics_port"`

	// InfluxDB (push)
	InfluxURL      string        `json:"influx_url" yaml:"influx_url"`
	InfluxToken    string        `json:"influx_token" yaml:"influx_token"`
	InfluxOrg      string        `json:"influx_org" yaml:"influx_org"`
	InfluxBucket   string        `json:"influx_bucket" yaml:"influx_bucket"`
	InfluxInterval time.Duration `json:"influx_interval" yaml:"influx_interval"`
	DiscordWebhook string        `json:"discord_webhook" yaml:"discord_webhook"`
	SlackWebhook   string        `json:"slack_webhook" yaml:"slack_webhook"`
	TeamsWebhook   string        `json:"teams_webhook" yaml:"teams_webhook"`

	// Dry-run: check what would be updated without making changes
	DryRun bool `json:"dry_run" yaml:"dry_run"`

	// Private registry credentials (simple auth support)
	RegistryUser string `json:"registry_user" yaml:"registry_user"`
	RegistryPass string `json:"registry_pass" yaml:"registry_pass"`

	TelegramToken  string `json:"telegram_token" yaml:"telegram_token"`
	TelegramChatID string `json:"telegram_chat_id" yaml:"telegram_chat_id"`

	MastodonServer string `json:"mastodon_server" yaml:"mastodon_server"`
	MastodonToken  string `json:"mastodon_token" yaml:"mastodon_token"`

	EmailHost string   `json:"email_host" yaml:"email_host"`
	EmailPort int      `json:"email_port" yaml:"email_port"`
	EmailUser string   `json:"email_user" yaml:"email_user"`
	EmailPass string   `json:"email_pass" yaml:"email_pass"`
	EmailTo   []string `json:"email_to" yaml:"email_to"`

	// Additional notifiers
	GenericWebhookURL string `json:"generic_webhook_url" yaml:"generic_webhook_url"`
	GotifyURL         string `json:"gotify_url" yaml:"gotify_url"`
	GotifyToken       string `json:"gotify_token" yaml:"gotify_token"`
	PushoverUser      string `json:"pushover_user" yaml:"pushover_user"`
	PushoverToken     string `json:"pushover_token" yaml:"pushover_token"`
	// Apprise gateway URL (optional)
	AppriseURL string `json:"apprise_url" yaml:"apprise_url"`

	// HostSocketPath is the path to the Docker socket on the host machine.
	// Used by the self-update worker to mount the socket correctly.
	HostSocketPath string `json:"host_socket_path" yaml:"host_socket_path"`

	// PinDigests ensures the container is updated to a specific sha256 digest
	// rather than a mutable tag.
	PinDigests bool `json:"pin_digests" yaml:"pin_digests"`

	// MaxConcurrentUpdates limits how many containers are processed in parallel.
	// Default: 1 (sequential). Set to 5-10 for faster updates in large environments.
	MaxConcurrentUpdates int `json:"max_concurrent_updates" yaml:"max_concurrent_updates"`
}

// IsWithinPatchWindow returns true when the provided time is inside the configured patch window.
// PatchWindow format: "HH:MM-HH:MM" in local time. Supports windows that span midnight (e.g., "23:00-02:00").
func (c *Config) IsWithinPatchWindow(now time.Time) bool {
	if c.PatchWindow == "" {
		// empty window means always allowed
		return true
	}
	var sh, sm, eh, em int
	n, err := fmt.Sscanf(c.PatchWindow, "%d:%d-%d:%d", &sh, &sm, &eh, &em)
	if err != nil || n != 4 {
		// invalid format - be conservative and return false (don't patch)
		return false
	}
	// Convert current time to minutes since midnight
	nowMinutes := now.Hour()*60 + now.Minute()
	startMinutes := sh*60 + sm
	endMinutes := eh*60 + em

	if endMinutes > startMinutes {
		// Normal window (e.g., 02:00-06:00)
		return nowMinutes >= startMinutes && nowMinutes <= endMinutes
	}
	// Window wraps midnight (e.g., 23:00-01:00)
	// Valid if now >= start OR now <= end
	return nowMinutes >= startMinutes || nowMinutes <= endMinutes
}

// DefaultConfig returns a sane default configuration
func DefaultConfig() *Config {
	return &Config{
		PollInterval:            10 * time.Minute,
		PatchWindow:             "",
		ManageLatestOnly:        true,
		OptOut:                  true, // default to opt-out (manage unless dockhand.disable=true)
		VerifyTimeout:           10 * time.Second,
		VerifyInterval:          500 * time.Millisecond,
		HookTimeout:             5 * time.Minute,
		CircuitBreakerThreshold: 3,
		CircuitBreakerCooldown:  10 * time.Minute,
		NotificationLevel:       "all",

		// Metrics defaults (opt-in)
		MetricsEnabled: false,
		MetricsPort:    9090,

		// Influx defaults
		InfluxInterval: 1 * time.Minute,
		// default: no dry-run, no registry auth
		DryRun: false,
		// sanitize names by default
		SanitizeNames: true,

		// Default assumes standard Docker installation
		HostSocketPath: "/var/run/docker.sock",

		// By default we do not pin to digests
		PinDigests: false,

		// By default run sequential updates for safety
		MaxConcurrentUpdates: 1,
	}
}

// Validate returns a list of non-fatal configuration warnings, such as
// incomplete notifier credential combinations.
func (c *Config) Validate() []string {
	var warnings []string
	checks := []struct {
		cond bool
		msg  string
	}{
		{c.GotifyURL != "" && c.GotifyToken == "", "gotify URL provided but token is missing"},
		{c.GotifyToken != "" && c.GotifyURL == "", "gotify token provided but URL is missing"},
		{c.PushoverUser != "" && c.PushoverToken == "", "pushover user provided but token is missing"},
		{c.PushoverToken != "" && c.PushoverUser == "", "pushover token provided but user is missing"},
		{c.EmailHost != "" && len(c.EmailTo) == 0, "email host provided but no recipients configured (EmailTo)"},
		{c.EmailHost == "" && len(c.EmailTo) > 0, "email recipients configured but email host is empty"},
	}
	for _, ch := range checks {
		if ch.cond {
			warnings = append(warnings, ch.msg)
		}
	}
	if pw := validatePatchWindow(c.PatchWindow); pw != "" {
		warnings = append(warnings, pw)
	}
	return warnings
}

// validatePatchWindow returns a warning string when the provided patch window is invalid, or empty when valid/empty.
func validatePatchWindow(pw string) string {
	if pw == "" {
		return ""
	}
	var sh, sm, eh, em int
	n, err := fmt.Sscanf(pw, "%d:%d-%d:%d", &sh, &sm, &eh, &em)
	if err != nil || n != 4 || sh < 0 || sh > 23 || eh < 0 || eh > 23 || sm < 0 || sm > 59 || em < 0 || em > 59 {
		return fmt.Sprintf("invalid PatchWindow format: %q (expected HH:MM-HH:MM)", pw)
	}
	return ""
}

// LoadConfigFromFile loads config from a YAML/JSON file
func LoadConfigFromFile(path string) (*Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
