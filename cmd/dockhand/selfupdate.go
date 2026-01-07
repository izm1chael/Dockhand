package main

import (
	"context"

	"github.com/dockhand/dockhand/internal/docker"
	"github.com/dockhand/dockhand/internal/logging"
)

// performSelfUpdate runs on the worker: waits for target to stop and replaces it
func performSelfUpdate(ctx context.Context, cli docker.Client, targetID, image string) error {
	logging.Get().Info().Str("target", targetID).Msg("performing self-update")
	// If image is empty, try to proceed; ReplaceContainer will use the provided image
	// and expect caller to have specified it.
	// Attempt replace directly; ReplaceContainer will inspect and recreate
	// using the provided image.
	if image == "" {
		// No image provided; log but proceed (ReplaceContainer will still use newImage)
		logging.Get().Warn().Msg("no image provided to worker; proceeding with provided image value (may fail)")
	}
	// call ReplaceContainer which waits and recreates
	return cli.ReplaceContainer(ctx, targetID, image)
}
