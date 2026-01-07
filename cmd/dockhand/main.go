package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dockhand/dockhand/internal/config"
	"github.com/dockhand/dockhand/internal/daemon"
	"github.com/dockhand/dockhand/internal/docker"
	"github.com/dockhand/dockhand/internal/logging"
	"github.com/dockhand/dockhand/internal/metrics"
)

func main() {
	// 1. Define ALL flags at the top
	cfgFile := flag.String("config", "", "Path to config file")
	poll := flag.Duration("poll-interval", 5*time.Minute, "Poll interval")
	// CLI run-once / dry-run flags
	runOnce := flag.Bool("run-once", false, "run one reconciliation pass and exit")
	dryRun := flag.Bool("dry-run", false, "perform a dry run (detect updates but do not apply)")

	// Internal / Worker flags
	selfUpdateTarget := flag.String("self-update-worker", "", "INTERNAL: Target container ID to update (worker mode)")
	selfUpdateImage := flag.String("self-update-image", "", "INTERNAL: New image to apply when running as worker")

	// 2. Parse ONCE
	flag.Parse()

	// 3. Check for Worker Mode immediately
	// If this is a worker, we don't need to load the full daemon config or metrics
	if *selfUpdateTarget != "" {
		// Initialize minimal logger for worker
		// (You can still load basic env vars if needed, but keep it simple)
		if err := runWorker(*selfUpdateTarget, *selfUpdateImage); err != nil {
			log.Fatalf("worker failed: %v", err)
		}
		return
	}

	cfg := config.DefaultConfig()
	// load from file if provided (overrides defaults)
	if *cfgFile != "" {
		c, err := config.LoadConfigFromFile(*cfgFile)
		if err != nil {
			log.Fatalf("failed loading config: %v", err)
		}
		cfg = c
	}

	// apply env var overrides (overrides file/defaults)
	if err := config.ApplyEnvOverrides(cfg); err != nil {
		log.Fatalf("invalid environment configuration: %v", err)
	}

	// CLI flags should have highest precedence (override env/file/defaults)
	cfg.PollInterval = *poll

	// CLI run-once / dry-run flags were parsed above
	if *dryRun {
		cfg.DryRun = true
	}

	// initialize logging
	cleanup := initLogging()
	defer cleanup()

	// start metrics & influx if configured
	initMetricsAndInflux(cfg)

	// Verify Docker socket is accessible (common pitfall when running in containers)
	ensureDockerSocketAccessible()

	// create docker client (with optional registry auth), then start daemon
	ctx := context.Background()
	dCli := createDockerClientOrFatal(cfg)
	startDaemonAndWait(ctx, cfg, dCli, *runOnce)
}

// Helper to keep main clean
func runWorker(target, image string) error {
	// Simple config loading for worker just to get registry auth if needed
	// Or just use env vars directly since the worker inherits env from the parent
	user := os.Getenv("DOCKHAND_REGISTRY_USER")
	pass := os.Getenv("DOCKHAND_REGISTRY_PASS")

	// Initialize logger (basic)
	_, _ = logging.Init("", "info")

	logging.Get().Info().Str("target", target).Msg("starting self-update worker")

	// Respect the sanitize-names setting inherited via env. Default to true.
	sanitize := true
	if v := os.Getenv("DOCKHAND_SANITIZE_NAMES"); v == "false" {
		sanitize = false
	}

	cli, err := docker.NewClientWithAuthWithSanitize(user, pass, sanitize)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	if image == "" {
		logging.Get().Warn().Msg("no --self-update-image provided; worker will use image from ReplaceContainer logic")
	}

	return performSelfUpdate(context.Background(), cli, target, image)
}

// checkDockerSocketAccess verifies the socket exists and is openable for read/write.
// Returns nil if socket is absent (allowed), nil if accessible, or an error indicating
// why it isn't accessible (permission, other IO error, etc.).
func checkDockerSocketAccess(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		_ = f.Close()
		return nil
	}
	if os.IsNotExist(err) {
		// socket missing — not fatal here; Docker might not be present
		return nil
	}
	return err
}

// initLogging initializes log subsystem from env and returns a cleanup func
func initLogging() func() {
	logLevel := os.Getenv("DOCKHAND_LOG_LEVEL")
	logFile := os.Getenv("DOCKHAND_LOG_FILE")
	cleanup, err := logging.Init(logFile, logLevel)
	if err != nil {
		log.Fatalf("failed to initialize logger: %v", err)
	}
	return cleanup
}

// initMetricsAndInflux starts optional metrics server and Influx pusher
func initMetricsAndInflux(cfg *config.Config) {
	if cfg.MetricsEnabled {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", metrics.PromHandler())
			mux.Handle("/status", metrics.JSONHandler())
			addr := fmt.Sprintf(":%d", cfg.MetricsPort)
			logging.Get().Info().Str("addr", addr).Msg("starting metrics server")
			_ = http.ListenAndServe(addr, mux)
		}()
	}
	if cfg.InfluxURL != "" {
		go metrics.StartInfluxPusher(context.Background(), cfg.InfluxURL, cfg.InfluxToken, cfg.InfluxOrg, cfg.InfluxBucket, cfg.InfluxInterval)
	}
}

// ensureDockerSocketAccessible wraps checkDockerSocketAccess and logs/fatals appropriately
func ensureDockerSocketAccessible() {
	if err := checkDockerSocketAccess("/var/run/docker.sock"); err != nil {
		if os.IsPermission(err) {
			logging.Get().Fatal().Msg("permission denied accessing /var/run/docker.sock: ensure the container user has appropriate group access (e.g., --group-add docker) or bind mount with matching GID)")
		} else {
			logging.Get().Warn().Err(err).Msg("warning: problem accessing /var/run/docker.sock; continuing but operations may fail")
		}
	}
}

// createDockerClientOrFatal creates a docker client or exits
func createDockerClientOrFatal(cfg *config.Config) docker.Client {
	cli, err := docker.NewClientWithAuthWithSanitize(cfg.RegistryUser, cfg.RegistryPass, cfg.SanitizeNames)
	if err != nil {
		logging.Get().Fatal().Err(err).Msg("failed to create docker client")
	}
	return cli
}

// startDaemonAndWait starts the daemon (or runs once) and waits for shutdown signal
func startDaemonAndWait(ctx context.Context, cfg *config.Config, dCli docker.Client, runOnce bool) {
	d := daemon.New(cfg, dCli)
	if runOnce {
		logging.Get().Info().Msg("run-once: performing a single reconciliation pass")
		d.RunOnce()
		return
	}
	go d.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	// Graceful shutdown: give up to 5 seconds for active operations to complete
	logging.Get().Info().Msg("shutdown signal received, waiting for active operations to complete")
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	d.Stop(shutdownCtx)
}
