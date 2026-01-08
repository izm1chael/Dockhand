package docker

import (
	"context"
	"fmt"

	"github.com/dockhand/dockhand/internal/logging"
)

// performRecreate handles the heavy lifting of recreating a container: rename old, prepare config, create, start and verify.
func (s *sdkClient) performRecreate(ctx context.Context, ctn Container, newImage string, opts RecreateOptions) error {
	insp, err := s.cli.ContainerInspect(ctx, ctn.ID)
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", ctn.ID, err)
	}

	// Rename old container to keep it around while we start the new one
	origName, tmpName, err := s.renameOldContainer(ctx, insp)
	if err != nil {
		return err
	}
	_ = tmpName // suppress unused

	// Prepare config for new container
	newCfg, hostCfg, netCfg := s.prepareNewContainerConfig(insp, newImage)

	// Create new container with original name (rollback on create failure)
	resp, err := s.createNewContainerWithRollback(ctx, newCfg, hostCfg, netCfg, origName, insp)
	if err != nil {
		return err
	}

	// Start new container
	if err := s.startNewContainer(ctx, resp.ID, insp, origName); err != nil {
		return err
	}

	// Verify new container (this will remove old on success or roll back on failure)
	if err := s.verifyNewContainer(ctx, resp.ID, insp, origName, opts); err != nil {
		return err
	}

	logging.Get().Info().Str("container", ctn.ID).Str("new_image", newImage).Msg("recreate completed")
	return nil
}
