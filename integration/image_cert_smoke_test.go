package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// This integration test is skipped by default. To run it locally, set
// RUN_DOCKER_INTEGRATION=1 in your environment. It requires Docker to be
// available on the host where the test runs.
func TestImageContainsCACerts(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("skipping integration test; set RUN_DOCKER_INTEGRATION=1 to enable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Build the image
	build := exec.CommandContext(ctx, "docker", "build", "-t", "dockhand:smoke", ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("docker build failed: %v", err)
	}

	// Run the image and check that ca-certificates file exists
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "dockhand:smoke", "sh", "-c", "test -f /etc/ssl/certs/ca-certificates.crt && echo OK")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v - output: %s", err, string(out))
	}
	if string(out) != "OK\n" {
		t.Fatalf("unexpected output from container: %q", string(out))
	}
}
