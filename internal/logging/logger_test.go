package logging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

func TestInit(t *testing.T) {
	tests := []struct {
		name      string
		logFile   string
		level     string
		wantLevel zerolog.Level
	}{
		{"default level", "", "", zerolog.InfoLevel},
		{"debug level", "", "debug", zerolog.DebugLevel},
		{"info level", "", "info", zerolog.InfoLevel},
		{"warn level", "", "warn", zerolog.WarnLevel},
		{"error level", "", "error", zerolog.ErrorLevel},
		{"case insensitive", "", "DEBUG", zerolog.DebugLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup, err := Init(tt.logFile, tt.level)
			if err != nil {
				t.Fatalf("Init() failed: %v", err)
			}
			defer cleanup()

			if zerolog.GlobalLevel() != tt.wantLevel {
				t.Errorf("expected level %v, got %v", tt.wantLevel, zerolog.GlobalLevel())
			}
		})
	}
}

func TestInitWithFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	cleanup, err := Init(logPath, "info")
	if err != nil {
		t.Fatalf("Init() with file failed: %v", err)
	}
	defer cleanup()

	// Write a test log entry
	Get().Info().Msg("test message")

	// Verify file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("log file was not created at %s", logPath)
	}

	// Cleanup should close the file
	cleanup()
}

func TestInitWithInvalidFile(t *testing.T) {
	// Ensure Init will create missing directories when given a nested path under a writable temp dir
	tmpDir := t.TempDir()
	nested := filepath.Join(tmpDir, "nonexistent", "logs")
	logPath := filepath.Join(nested, "test.log")

	cleanup, err := Init(logPath, "info")
	if err != nil {
		t.Fatalf("Init() failed to create nested log dir: %v", err)
	}
	defer cleanup()

	// Verify file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("log file was not created at %s", logPath)
	}
}

func TestGet(t *testing.T) {
	cleanup, err := Init("", "info")
	if err != nil {
		t.Fatalf("Init() failed: %v", err)
	}
	defer cleanup()

	logger := Get()
	if logger == nil {
		t.Error("Get() returned nil logger")
	}

	// Verify we can use the logger
	logger.Info().Msg("test")
}
