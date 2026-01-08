package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/dockhand/dockhand/internal/logging"
)

// waitForStop polls the container until it is no longer running or context cancels.
func (s *sdkClient) waitForStop(ctx context.Context, targetID string) error {
	for i := 0; i < waitForStopRetries; i++ {
		if ctx.Err() != nil {
			return fmt.Errorf("context canceled while waiting for target to stop: %w", ctx.Err())
		}
		st, err := s.cli.ContainerInspect(ctx, targetID)
		if err != nil {
			// If inspect fails (container missing), assume stopped and continue
			break
		}
		if st.State == nil || !st.State.Running {
			break
		}
		time.Sleep(waitForStopInterval)
	}
	return nil
}

// createAndStartReplacement creates and starts the replacement container using original config.
func (s *sdkClient) createAndStartReplacement(ctx context.Context, insp types.ContainerJSON, newImage, origName string) error {
	newCfg := insp.Config
	if newCfg == nil {
		newCfg = &containertypes.Config{}
	}
	newCfg.Image = newImage
	hostCfg := insp.HostConfig
	var netCfg *network.NetworkingConfig
	if insp.NetworkSettings != nil && insp.NetworkSettings.Networks != nil {
		netCfg = &network.NetworkingConfig{EndpointsConfig: insp.NetworkSettings.Networks}
	}

	resp, err := s.cli.ContainerCreate(ctx, newCfg, hostCfg, netCfg, nil, origName)
	if err != nil {
		return fmt.Errorf("create replaced container: %w", err)
	}
	if err := s.cli.ContainerStart(ctx, resp.ID, containertypes.StartOptions{}); err != nil {
		return fmt.Errorf("start replaced container: %w", err)
	}
	logging.Get().Info().Str("new", resp.ID).Str("orig_name", origName).Msg("replacement started")
	return nil
}
