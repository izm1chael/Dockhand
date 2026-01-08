package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
)

// Init initializes the global logger. If logFilePath is non-empty, logs are
// written to both stdout and the file. level can be "debug", "info", "warn", "error".
func Init(logFilePath, level string) (func(), error) {
	// determine level
	l := zerolog.InfoLevel
	switch strings.ToLower(level) {
	case "debug":
		l = zerolog.DebugLevel
	case "info":
		l = zerolog.InfoLevel
	case "warn":
		l = zerolog.WarnLevel
	case "error":
		l = zerolog.ErrorLevel
	}
	zerolog.SetGlobalLevel(l)

	var writers []io.Writer
	writers = append(writers, os.Stdout)
	var f *os.File
	if logFilePath != "" {
		// Ensure the directory exists before attempting to open the file
		if err := os.MkdirAll(filepath.Dir(logFilePath), 0o700); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}
		var err error
		// Use 0640 for log file to avoid world-readable logs while allowing group read if needed
		f, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return nil, err
		}
		writers = append(writers, f)
	}
	multi := io.MultiWriter(writers...)
	Log = zerolog.New(multi).With().Timestamp().Logger()
	// return cleanup func
	return func() {
		if f != nil {
			_ = f.Close()
		}
	}, nil
}

// Log is the package-global logger configured by Init
var Log zerolog.Logger

// Get returns a pointer to the package-global logger
func Get() *zerolog.Logger {
	return &Log
}
