package docker

import (
	"context"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/dockhand/dockhand/internal/logging"
)

// evaluateNewContainerState inspects the container and returns (done, error).
// done == true indicates verification succeeded (old removed or success returned).
func (s *sdkClient) evaluateNewContainerState(ctx context.Context, newID string, insp types.ContainerJSON, origName string, opts RecreateOptions, deadline time.Time) (bool, error) {
	st, err := s.cli.ContainerInspect(ctx, newID)
	if err != nil {
		return false, s.rollbackOnInspectFailure(ctx, newID, insp, origName, err)
	}
	if st.State != nil && st.State.Running {
		return s.handleRunningState(ctx, st, newID, insp, origName, opts, deadline)
	}
	return false, nil
}

// handleRunningState processes a running container's state and returns (done, error).
func (s *sdkClient) handleRunningState(ctx context.Context, st types.ContainerJSON, newID string, insp types.ContainerJSON, origName string, opts RecreateOptions, deadline time.Time) (bool, error) {
	// explicit exec-based healthcheck
	if len(opts.HealthcheckCmd) > 0 {
		if err := s.runHealthcheckExec(ctx, newID, opts.HealthcheckCmd, deadline, insp, origName, opts.VerifyInterval); err != nil {
			return false, err
		}
		return true, nil
	}

	// Network-based healthcheck (HTTP/TCP) via labels
	if err := s.runNetworkHealthcheck(ctx, st, deadline, opts.VerifyInterval); err != nil {
		logging.Get().Warn().Err(err).Str("new", newID).Msg("network healthcheck failed; rolling back")
		return false, s.rollbackRestore(ctx, newID, insp, origName)
	}

	// Docker health status
	if st.State.Health != nil {
		if st.State.Health.Status == "healthy" {
			if err := s.removeOldAndSuccess(ctx, insp.ID); err != nil {
				return false, err
			}
			return true, nil
		}
	} else {
		// running and no healthcheck => consider success
		if err := s.removeOldAndSuccess(ctx, insp.ID); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
