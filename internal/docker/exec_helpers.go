package docker

import (
	"context"
	"fmt"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/dockhand/dockhand/internal/logging"
)

// runPreUpdateHook executes a pre-update command inside the container if provided
func (s *sdkClient) runPreUpdateHook(ctx context.Context, ctn Container, timeout time.Duration) error {
	if cmdStr, ok := ctn.Labels["dockhand.pre-update"]; ok && cmdStr != "" {
		logging.Get().Info().Str("container", ctn.ID).Msg("running pre-update hook inside container")
		execResp, err := s.cli.ContainerExecCreate(ctx, ctn.ID, containertypes.ExecOptions{Cmd: []string{"sh", "-c", cmdStr}})
		if err != nil {
			logging.Get().Error().Err(err).Msg("pre-update exec create failed; aborting update")
			return fmt.Errorf("pre-update exec create: %w", err)
		}
		_ = s.cli.ContainerExecStart(ctx, execResp.ID, containertypes.ExecStartOptions{})
		// Use default timeout if not provided
		if timeout <= 0 {
			timeout = defaultExecTimeout
		}
		return s.waitForExecCompletion(ctx, execResp.ID, timeout, ctn.ID)
	}
	return nil
}

// waitForExecCompletion waits for an exec to complete with timeout and cancellation support
func (s *sdkClient) waitForExecCompletion(ctx context.Context, execID string, timeout time.Duration, containerID string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return fmt.Errorf("pre-update exec canceled: %w", ctx.Err())
		}
		execInspect, err := s.cli.ContainerExecInspect(ctx, execID)
		if err != nil {
			logging.Get().Error().Err(err).Msg("pre-update exec inspect error")
			break
		}
		if execInspect.ExitCode == 0 {
			logging.Get().Info().Str("container", containerID).Msg("pre-update exec succeeded")
			return nil
		}
		if execInspect.Running {
			// allow early exit if context canceled
			select {
			case <-ctx.Done():
				return fmt.Errorf("pre-update exec canceled: %w", ctx.Err())
			case <-time.After(execPollInterval):
			}
			continue
		}
		// exec finished with non-zero exit
		logging.Get().Warn().Int("exit_code", execInspect.ExitCode).Msg("pre-update exec failed; aborting update")
		return fmt.Errorf("pre-update exec failed: exit %d", execInspect.ExitCode)
	}
	logging.Get().Warn().Str("container", containerID).Msg("pre-update exec did not finish before timeout; aborting update")
	return fmt.Errorf("pre-update exec timed out after %s", timeout)
}
