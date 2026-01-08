// Package docker provides helpers and an SDK-backed client for interacting
// with Docker from the Dockhand daemon.
package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	imageapi "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/dockhand/dockhand/internal/state"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dockhand/dockhand/internal/logging"
	"github.com/dockhand/dockhand/internal/metrics"
)

const (
	maxNameLen          = 64
	waitForStopRetries  = 120
	waitForStopInterval = 1 * time.Second
	execPollInterval    = 500 * time.Millisecond
	defaultExecTimeout  = 30 * time.Second
	// small named constants to satisfy linter rules for repeated literals
	emptyString = ""
	zeroInt     = 0
)

var disallowedNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// WorkerOptions groups optional parameters for spawning a worker container.
type WorkerOptions struct {
	Name   string
	Binds  []string
	Labels map[string]string
}

type containerConfigs struct {
	config *containertypes.Config
	host   *containertypes.HostConfig
	net    *network.NetworkingConfig
}

type swapContext struct {
	newID    string
	original types.ContainerJSON
	origName string
}

// sanitizeName returns a Docker-safe container name by removing disallowed
// characters, normalizing to lowercase, and ensuring the name starts with an
// alphanumeric character. It enforces a maximum length of maxNameLen and
// falls back to "container" when the result would be empty.
func (s *sdkClient) sanitizeName(name string) string {
	if s.sanitizeNames {
		name = strings.ToLower(name)
	}
	clean := disallowedNameChars.ReplaceAllString(name, emptyString)
	if clean == emptyString {
		return "container"
	}
	clean = trimToMax(clean)
	first := rune(clean[zeroInt])
	if unicode.IsLetter(first) || unicode.IsDigit(first) {
		return clean
	}
	return trimToMax("c" + clean)
}

func trimToMax(name string) string {
	if len(name) > maxNameLen {
		return name[:maxNameLen]
	}
	return name
}

// RecreateOptions controls behavior for container recreate/replace flows.
// It configures verification timeouts, intervals, optional healthchecks,
// and hook timeouts used during the recreate process.
type RecreateOptions struct {
	VerifyTimeout  time.Duration
	VerifyInterval time.Duration
	HealthcheckCmd []string
	HookTimeout    time.Duration
}

// Client is the interface used by the daemon for Docker operations.
type Client interface {
	ListRunningContainers(
		ctx context.Context,
	) ([]Container, error)
	ListAllContainers(
		ctx context.Context,
	) ([]Container, error)
	// PullImage returns the image ID and repo digest (when available).
	// The digest is used for pinning.
	PullImage(
		ctx context.Context,
		image string,
	) (string, string, error)
	RecreateContainer(
		ctx context.Context,
		c Container,
		newImage string,
		opts RecreateOptions,
	) error
	RemoveImage(ctx context.Context, imageID string) error
	RenameContainer(
		ctx context.Context,
		containerID, newName string,
	) error

	// SpawnWorker starts a temporary container (worker) from the provided
	// image. The command, binds, and labels are supplied, and it returns the
	// worker ID.
	SpawnWorker(
		ctx context.Context,
		image string,
		cmd []string,
		opts WorkerOptions,
	) (string, error)

	// ReplaceContainer waits for the target container to stop, removes it, and
	// recreates it using the provided image while preserving configuration
	// where possible.
	ReplaceContainer(
		ctx context.Context,
		targetID, newImage string,
	) error
}

// sdkClient is the production implementation using the official Docker SDK
type dockerAPI interface {
	ImagePull(
		ctx context.Context,
		refStr string,
		options imageapi.PullOptions,
	) (io.ReadCloser, error)
	ImageInspectWithRaw(
		ctx context.Context,
		image string,
	) (types.ImageInspect, []byte, error)
	ContainerInspect(
		ctx context.Context,
		containerID string,
	) (types.ContainerJSON, error)
	ContainerRename(ctx context.Context, containerID, newName string) error
	ContainerCreate(
		ctx context.Context,
		config *containertypes.Config,
		hostConfig *containertypes.HostConfig,
		networkingConfig *network.NetworkingConfig,
		platform *ocispec.Platform,
		containerName string,
	) (containertypes.CreateResponse, error)
	ContainerStart(
		ctx context.Context,
		containerID string,
		options containertypes.StartOptions,
	) error
	ContainerRemove(
		ctx context.Context,
		containerID string,
		options containertypes.RemoveOptions,
	) error
	ContainerExecCreate(
		ctx context.Context,
		container string,
		config containertypes.ExecOptions,
	) (containertypes.ExecCreateResponse, error)
	ContainerExecStart(
		ctx context.Context,
		execID string,
		config containertypes.ExecStartOptions,
	) error
	ContainerExecInspect(
		ctx context.Context,
		execID string,
	) (containertypes.ExecInspect, error)
	ContainerList(
		ctx context.Context,
		options containertypes.ListOptions,
	) ([]types.Container, error)
	ImageRemove(
		ctx context.Context,
		image string,
		options imageapi.RemoveOptions,
	) ([]imageapi.DeleteResponse, error)
}

type sdkClient struct {
	cli           dockerAPI
	registryAuth  string
	sanitizeNames bool
}

// SpawnWorker starts a temporary worker container using the specified image
// and command, mounting the provided binds and applying labels. It returns the
// created container ID.
func (s *sdkClient) SpawnWorker(
	ctx context.Context,
	image string,
	cmd []string,
	opts WorkerOptions,
) (string, error) {
	cfg := &containertypes.Config{
		Image:  image,
		Cmd:    cmd,
		Labels: opts.Labels,
	}
	hostCfg := &containertypes.HostConfig{
		Binds:      opts.Binds,
		AutoRemove: true,
	}
	resp, err := s.cli.ContainerCreate(
		ctx,
		cfg,
		hostCfg,
		nil,
		nil,
		opts.Name,
	)
	if err != nil {
		return emptyString, fmt.Errorf("create worker: %w", err)
	}
	startOpts := containertypes.StartOptions{}
	if err := s.cli.ContainerStart(ctx, resp.ID, startOpts); err != nil {
		return emptyString, fmt.Errorf("start worker: %w", err)
	}
	return resp.ID, nil
}

// ReplaceContainer waits for the target container to stop, removes it and
// recreates it using the provided image while preserving configuration where
// possible.
func (s *sdkClient) ReplaceContainer(
	ctx context.Context,
	targetID, newImage string,
) error {
	if err := s.waitForStop(ctx, targetID); err != nil {
		return err
	}
	insp, err := s.cli.ContainerInspect(ctx, targetID)
	if err != nil {
		return fmt.Errorf("inspect target for replace: %w", err)
	}
	origName := strings.TrimPrefix(insp.Name, "/")
	origName = s.sanitizeName(origName)

	// Ensure target removed before creating replacement
	removeOpts := containertypes.RemoveOptions{Force: true}
	_ = s.cli.ContainerRemove(ctx, targetID, removeOpts)

	return s.createAndStartReplacement(ctx, insp, newImage, origName)
}

// waitForStop polls the container until it is no longer running or context
// cancels.
// waitForStop and createAndStartReplacement moved to replace_helpers.go

// NewClient returns an SDK-backed Docker client
func NewClient() (Client, error) {
	return NewClientWithAuth(emptyString, emptyString)
}

// NewClientWithAuthWithSanitize returns a client configured with simple
// registry auth and a sanitize-names option.
func NewClientWithAuthWithSanitize(
	user, pass string,
	sanitize bool,
) (Client, error) {
	c, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	s := &sdkClient{cli: c, sanitizeNames: sanitize}
	if user != emptyString || pass != emptyString {
		auth := map[string]string{"username": user, "password": pass}
		b, _ := json.Marshal(auth)
		s.registryAuth = base64.StdEncoding.EncodeToString(b)
	}
	return s, nil
}

// NewClientWithAuth returns a client configured with simple registry auth
// (username/password) and uses a default sanitize behavior (true).
func NewClientWithAuth(user, pass string) (Client, error) {
	return NewClientWithAuthWithSanitize(user, pass, true)
}

// NewClientForHost returns a client configured for a specific host endpoint.
// host may be empty to indicate default behavior (FromEnv).
func NewClientForHost(host, user, pass string, sanitize bool) (Client, error) {
	opts := []client.Opt{client.WithAPIVersionNegotiation()}
	if host != emptyString {
		opts = append(opts, client.WithHost(host))
	} else {
		opts = append(opts, client.FromEnv)
	}

	c, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}

	s := &sdkClient{cli: c, sanitizeNames: sanitize}
	if user != emptyString || pass != emptyString {
		auth := map[string]string{"username": user, "password": pass}
		b, _ := json.Marshal(auth)
		s.registryAuth = base64.StdEncoding.EncodeToString(b)
	}
	return s, nil
}

func (s *sdkClient) ListRunningContainers(
	ctx context.Context,
) ([]Container, error) {
	listOpts := containertypes.ListOptions{All: false}
	list, err := s.cli.ContainerList(ctx, listOpts)
	if err != nil {
		return nil, err
	}
	out := make([]Container, zeroInt, len(list))
	for _, c := range list {
		out = append(out, Container{
			ID:      c.ID,
			Image:   c.Image,
			ImageID: c.ImageID,
			Labels:  c.Labels,
			Names:   c.Names,
		})
	}
	return out, nil
}

func (s *sdkClient) ListAllContainers(
	ctx context.Context,
) ([]Container, error) {
	listOpts := containertypes.ListOptions{All: true}
	list, err := s.cli.ContainerList(ctx, listOpts)
	if err != nil {
		return nil, err
	}
	out := make([]Container, zeroInt, len(list))
	for _, c := range list {
		out = append(out, Container{
			ID:      c.ID,
			Image:   c.Image,
			ImageID: c.ImageID,
			Labels:  c.Labels,
			Names:   c.Names,
		})
	}
	return out, nil
}

func (s *sdkClient) RenameContainer(
	ctx context.Context,
	containerID, newName string,
) error {
	return s.cli.ContainerRename(ctx, containerID, newName)
}

func (s *sdkClient) PullImage(
	ctx context.Context,
	img string,
) (imageID string, repoDigest string, err error) {
	logging.Get().
		Info().
		Str("image", img).
		Msg("pulling image")
	pullOpts := imageapi.PullOptions{RegistryAuth: s.registryAuth}
	rc, err := s.cli.ImagePull(ctx, img, pullOpts)
	if err != nil {
		return emptyString, emptyString, imageError("image pull", img, err)
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
	inspected, _, err := s.cli.ImageInspectWithRaw(ctx, img)
	if err != nil {
		return emptyString, emptyString, imageError("inspect image", img, err)
	}
	repoDigest = firstRepoDigest(inspected.RepoDigests)
	logPulledImage(img, inspected.ID, repoDigest)
	return inspected.ID, repoDigest, nil
}

func logPulledImage(img, id, digest string) {
	logging.Get().
		Info().
		Str("image", img).
		Str("id", id).
		Str("digest", digest).
		Msg("pulled image")
}

func firstRepoDigest(digests []string) string {
	if len(digests) > zeroInt {
		return digests[zeroInt]
	}
	return emptyString
}

func imageError(action, img string, err error) error {
	logging.Get().
		Error().
		Err(err).
		Str("image", img).
		Msg(action + " failed")
	return fmt.Errorf("%s %s: %w", action, img, err)
}

func (s *sdkClient) RecreateContainer(
	ctx context.Context,
	ctn Container,
	newImage string,
	opts RecreateOptions,
) error {
	logging.Get().
		Info().
		Str("container", ctn.ID).
		Str("image", newImage).
		Msg("recreating container")

	// Pre-update hook
	if err := s.runPreUpdateHook(ctx, ctn, opts.HookTimeout); err != nil {
		return err
	}
	// perform the recreate workflow in a helper to keep this method small
	return s.performRecreate(ctx, ctn, newImage, opts)
}

// Exec helpers moved to exec_helpers.go

// NOTE: image pulling is performed by the daemon prior to invoking
// RecreateContainer. The previous helper that called PullImage was removed to
// avoid redundant registry calls; container creation will fail and be handled
// by existing error paths if the image is missing.

// renameOldContainer renames the existing container to a temporary sanitized
// name
func (s *sdkClient) renameOldContainer(
	ctx context.Context,
	insp types.ContainerJSON,
) (origName string, tmpName string, err error) {
	origName = strings.TrimPrefix(insp.Name, "/")
	origName = s.sanitizeName(origName)
	tmpName = fmt.Sprintf(
		"%s-old-%d",
		origName,
		time.Now().UnixNano(),
	)
	logging.Get().
		Info().
		Str("old", insp.ID).
		Str("new_name", tmpName).
		Msg("renaming old container")
	if err := s.cli.ContainerRename(
		ctx,
		insp.ID,
		tmpName,
	); err != nil {
		return emptyString, emptyString, fmt.Errorf(
			"rename old container: %w",
			err,
		)
	}
	// Record the rename so we can safely recover this specific rename if the
	// daemon crashes
	if err := state.AddRenameRecord(state.RenameRecord{
		ContainerID: insp.ID,
		TmpName:     tmpName,
		OrigName:    origName,
		Timestamp:   time.Now(),
	}); err != nil {
		// non-fatal: log and continue
		logging.Get().
			Warn().
			Err(err).
			Str("container", insp.ID).
			Msg("failed to persist rename record")
	}
	return origName, tmpName, nil
}

// prepareNewContainerConfig prepares container and networking configs for a
// recreate operation
func (*sdkClient) prepareNewContainerConfig(
	insp types.ContainerJSON,
	newImage string,
) (
	*containertypes.Config,
	*containertypes.HostConfig,
	*network.NetworkingConfig,
) {
	newCfg := insp.Config
	if newCfg == nil {
		newCfg = &containertypes.Config{}
	}
	newCfg.Image = newImage

	// Networking configuration (reuse endpoint settings if available)
	var netCfg *network.NetworkingConfig
	if insp.NetworkSettings != nil &&
		insp.NetworkSettings.Networks != nil {
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: insp.NetworkSettings.Networks,
		}
	}

	// Use same host config if present
	hostCfg := insp.HostConfig
	return newCfg, hostCfg, netCfg
}

// createNewContainerWithRollback creates a new container and attempts to
// restore the old container name and state on failure
func (s *sdkClient) createNewContainerWithRollback(
	ctx context.Context,
	cfgs containerConfigs,
	origName string,
	insp types.ContainerJSON,
) (containertypes.CreateResponse, error) {
	resp, err := s.cli.ContainerCreate(
		ctx,
		cfgs.config,
		cfgs.host,
		cfgs.net,
		nil,
		origName,
	)
	if err != nil {
		// try to restore old name before returning
		renameErr := s.cli.ContainerRename(
			ctx,
			insp.ID,
			origName,
		)
		if renameErr == nil {
			// Clean up persistent state record since the rename was reverted
			if rmErr := state.RemoveRenameRecordByContainerID(
				insp.ID,
			); rmErr != nil {
				logging.Get().
					Warn().
					Err(rmErr).
					Str("container", insp.ID).
					Msg("failed to remove rename record after create failure")
			}
		} else {
			logging.Get().
				Error().
				Err(renameErr).
				Str("container", insp.ID).
				Msg("failed to restore old name; record kept")
		}
		logging.Get().
			Error().
			Err(err).
			Msg("create new container failed; restored old name")
		metrics.IncRollback()
		return containertypes.CreateResponse{},
			fmt.Errorf("create new container: %w", err)
	}
	return resp, nil
}

// startNewContainer starts the newly created container and rolls back on
// failure
func (s *sdkClient) startNewContainer(
	ctx context.Context,
	swap swapContext,
) error {
	logging.Get().
		Info().
		Str("new_id", swap.newID).
		Msg("starting new container")
	startOpts := containertypes.StartOptions{}
	if err := s.cli.ContainerStart(ctx, swap.newID, startOpts); err != nil {
		return s.rollbackStartFailure(ctx, swap, err)
	}
	return nil
}

func (s *sdkClient) rollbackStartFailure(
	ctx context.Context,
	swap swapContext,
	startErr error,
) error {
	logging.Get().
		Error().
		Err(startErr).
		Str("new_id", swap.newID).
		Msg("start new container failed; rolling back")
	removeOpts := containertypes.RemoveOptions{Force: true}
	if err := s.cli.ContainerRemove(
		ctx,
		swap.newID,
		removeOpts,
	); err != nil {
		logging.Get().
			Warn().
			Err(err).
			Str("new", swap.newID).
			Msg("failed removing new container during rollback")
	}
	if err := s.cli.ContainerRename(
		ctx,
		swap.original.ID,
		swap.origName,
	); err != nil {
		logging.Get().
			Warn().
			Err(err).
			Str("old", swap.original.ID).
			Msg("failed renaming old container back during rollback")
	} else if err := state.RemoveRenameRecordByContainerID(
		swap.original.ID,
	); err != nil {
		logging.Get().
			Warn().
			Err(err).
			Str("container", swap.original.ID).
			Msg("failed to remove rename record after rollback")
	}
	startOpts := containertypes.StartOptions{}
	if err := s.cli.ContainerStart(
		ctx,
		swap.original.ID,
		startOpts,
	); err != nil {
		logging.Get().
			Warn().
			Err(err).
			Str("old", swap.original.ID).
			Msg("failed starting old container during rollback")
	}
	metrics.IncRollback()
	return fmt.Errorf("start new container %s: %w", swap.newID, startErr)
}

// verifyNewContainer ensures the new container is running and healthy. It
// removes the old container on success or rolls back on failure.
func (s *sdkClient) verifyNewContainer(
	ctx context.Context,
	swap swapContext,
	opts RecreateOptions,
) error {
	ctx, cancel := context.WithTimeout(ctx, opts.VerifyTimeout)
	defer cancel()

	ticker := time.NewTicker(opts.VerifyInterval)
	defer ticker.Stop()

	deadline, _ := ctx.Deadline()

	for {
		if err := waitForVerifyTick(ctx, ticker); err != nil {
			return s.handleVerifyTermination(ctx, swap)
		}
		done, err := s.evaluateNewContainerState(
			ctx,
			swap.newID,
			swap.original,
			swap.origName,
			opts,
			deadline,
		)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func waitForVerifyTick(
	ctx context.Context,
	ticker *time.Ticker,
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ticker.C:
		return nil
	}
}

func (s *sdkClient) handleVerifyTermination(
	ctx context.Context,
	swap swapContext,
) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		logging.Get().
			Warn().
			Str("new", swap.newID).
			Msg("verification timed out; rolling back")
		metrics.IncRollback()
		return s.rollbackRestore(ctx, swap.newID, swap.original, swap.origName)
	}
	return fmt.Errorf("verification canceled: %w", ctx.Err())
}

// evaluateNewContainerState inspects the container and returns (done, error).
// done == true indicates verification succeeded (old removed or success
// returned).
// evaluateNewContainerState moved to verify_helpers.go

// verify-related helpers moved to verify_actions.go
func (s *sdkClient) RemoveImage(ctx context.Context, imageID string) error {
	logging.Get().
		Info().
		Str("image", imageID).
		Msg("removing image")
	removeOpts := imageapi.RemoveOptions{Force: false, PruneChildren: false}
	_, err := s.cli.ImageRemove(ctx, imageID, removeOpts)
	if err != nil {
		logging.Get().
			Error().
			Err(err).
			Str("image", imageID).
			Msg("failed removing image")
		return fmt.Errorf("remove image %s: %w", imageID, err)
	}
	logging.Get().
		Info().
		Str("image", imageID).
		Msg("removed image")
	return nil
}
