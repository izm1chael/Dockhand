package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"

	"github.com/dockhand/dockhand/internal/logging"
	"github.com/dockhand/dockhand/internal/metrics"
	"github.com/dockhand/dockhand/internal/state"
)

// rollbackOnInspectFailure handles inspect failures by cleaning up and returning an error
func (s *sdkClient) rollbackOnInspectFailure(ctx context.Context, newID string, insp types.ContainerJSON, origName string, inspectErr error) error {
	logging.Get().Error().Err(inspectErr).Str("new", newID).Msg("inspect new container failed; rolling back")
	_ = s.cli.ContainerRemove(ctx, newID, containertypes.RemoveOptions{Force: true})
	_ = s.cli.ContainerRename(ctx, insp.ID, origName)
	_ = s.cli.ContainerStart(ctx, insp.ID, containertypes.StartOptions{})
	metrics.IncRollback()
	return fmt.Errorf("inspect new container: %w", inspectErr)
}

// runHealthcheckExec runs an exec-based healthcheck inside the new container and performs rollback on failure
func (s *sdkClient) runHealthcheckExec(ctx context.Context, newID string, cmd []string, deadline time.Time, insp types.ContainerJSON, origName string, interval time.Duration) error {
	execResp, err := s.cli.ContainerExecCreate(ctx, newID, containertypes.ExecOptions{Cmd: cmd})
	if err != nil {
		logging.Get().Error().Err(err).Msg("exec create failed; rolling back")
		return s.rollbackRestore(ctx, newID, insp, origName)
	}
	logging.Get().Info().Str("exec", execResp.ID).Msg("starting exec")
	_ = s.cli.ContainerExecStart(ctx, execResp.ID, containertypes.ExecStartOptions{})

	ok, err := s.waitForExecSuccess(ctx, execResp.ID, deadline, interval)
	if err != nil {
		logging.Get().Error().Err(err).Str("new", newID).Msg("exec healthcheck error; rolling back")
		return s.rollbackRestore(ctx, newID, insp, origName)
	}
	if ok {
		logging.Get().Info().Str("new", newID).Msg("healthcheck exec succeeded")
		if err := s.cli.ContainerRemove(ctx, insp.ID, containertypes.RemoveOptions{Force: true}); err != nil {
			logging.Get().Warn().Err(err).Str("old", insp.ID).Msg("failed removing old container after healthcheck success")
		}
		return nil
	}
	logging.Get().Warn().Str("new", newID).Msg("healthcheck did not succeed within timeout; rolling back")
	return s.rollbackRestore(ctx, newID, insp, origName)
}

// waitForExecSuccess polls exec inspect until success, failure or deadline.
func (s *sdkClient) waitForExecSuccess(ctx context.Context, execID string, deadline time.Time, interval time.Duration) (bool, error) {
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false, fmt.Errorf("healthcheck canceled: %w", ctx.Err())
		}
		execInspect, err := s.cli.ContainerExecInspect(ctx, execID)
		if err != nil {
			logging.Get().Error().Err(err).Msg("exec inspect error")
			return false, err
		}
		if execInspect.ExitCode == 0 {
			return true, nil
		}
		if execInspect.Running {
			select {
			case <-ctx.Done():
				return false, fmt.Errorf("healthcheck canceled: %w", ctx.Err())
			case <-time.After(interval):
			}
			continue
		}
		logging.Get().Warn().Int("exit_code", execInspect.ExitCode).Msg("healthcheck exec failed")
		return false, nil
	}
	return false, nil
}

// removeOldAndSuccess removes the old container and returns success
func (s *sdkClient) removeOldAndSuccess(ctx context.Context, oldID string) error {
	if err := s.cli.ContainerRemove(ctx, oldID, containertypes.RemoveOptions{Force: true}); err != nil {
		// Removing the old container failed; log and treat as a non-fatal cleanup warning.
		logging.Get().Warn().Err(err).Str("old", oldID).Msg("failed removing old container after update; marking cleanup as failed")
		// Record cleanup failure for metrics and continue — the new container is assumed healthy.
		metrics.IncCleanupFailed()
		return nil
	}
	// Clean up persistent rename record if present
	if err := state.RemoveRenameRecordByContainerID(oldID); err != nil {
		// non-fatal: log and continue
		logging.Get().Warn().Err(err).Str("container", oldID).Msg("failed to remove rename record after successful cleanup")
	}
	return nil
}

// rollbackRestore performs rollback by removing new container and restoring the old
func (s *sdkClient) rollbackRestore(ctx context.Context, newID string, insp types.ContainerJSON, origName string) error {
	logging.Get().Warn().Str("new", newID).Msg("verification failed; removing new and restoring old")
	_ = s.cli.ContainerRemove(ctx, newID, containertypes.RemoveOptions{Force: true})
	if err := s.cli.ContainerRename(ctx, insp.ID, origName); err != nil {
		logging.Get().Warn().Err(err).Str("container", insp.ID).Msg("failed to restore old name during rollback")
	} else {
		// removal or restoration succeeded – ensure any persistent rename record is cleaned up
		if err := state.RemoveRenameRecordByContainerID(insp.ID); err != nil {
			logging.Get().Warn().Err(err).Str("container", insp.ID).Msg("failed to remove rename record during rollback")
		}
	}
	_ = s.cli.ContainerStart(ctx, insp.ID, containertypes.StartOptions{})
	metrics.IncRollback()
	return fmt.Errorf("new container failed verification; rolled back to old container")
}
