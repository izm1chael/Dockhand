package config_test

import (
	"testing"
	"time"

	"github.com/dockhand/dockhand/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	c := config.DefaultConfig()
	if c.PollInterval == 0 {
		t.Fatal("expected default poll interval to be >0")
	}
	if c.PatchWindow != "" {
		t.Fatalf("expected default patch window to be empty, got %q", c.PatchWindow)
	}
	if c.PollInterval < time.Minute {
		t.Fatalf("unrealistic poll interval: %v", c.PollInterval)
	}
}

func TestValidateWarnings(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GotifyURL = "https://gotify"
	// missing token
	w := cfg.Validate()
	if len(w) == 0 {
		t.Fatalf("expected warnings, got none")
	}
	// set token but no URL
	cfg2 := config.DefaultConfig()
	cfg2.GotifyToken = "tok"
	w2 := cfg2.Validate()
	if len(w2) == 0 {
		t.Fatalf("expected warnings for missing URL when token set, got none")
	}
	// pushover mismatches
	cfg3 := config.DefaultConfig()
	cfg3.PushoverUser = "u"
	w3 := cfg3.Validate()
	if len(w3) == 0 {
		t.Fatalf("expected pushover warnings, got none")
	}
	// email host without recipients
	cfg4 := config.DefaultConfig()
	cfg4.EmailHost = "mail"
	w4 := cfg4.Validate()
	if len(w4) == 0 {
		t.Fatalf("expected email warnings, got none")
	}
}
