package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
)

// sanitizeName returns a Docker-safe container name by removing disallowed
// characters, normalizing to lowercase, and ensuring the name starts with an
// alphanumeric character. It enforces a maximum length of `maxNameLen`.
// If the resulting name would be empty, it falls back to "container".
func (s *sdkClient) sanitizeName(name string) string {
	if s.sanitizeNames {
		name = strings.ToLower(name)
	}
	re := regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	clean := re.ReplaceAllString(name, "")
	if clean == "" {
		return "container"
	}
	// Enforce max length
	if len(clean) > maxNameLen {
		clean = clean[:maxNameLen]
	}
	// Ensure first character is alphanumeric
	r := rune(clean[0])
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		clean = "c" + clean
		if len(clean) > maxNameLen {
			clean = clean[:maxNameLen]
		}
	}
	return clean
}

// Client is the interface used by the daemon for Docker operations
type RecreateOptions struct {
	VerifyTimeout  time.Duration
	VerifyInterval time.Duration
	HealthcheckCmd []string      // optional exec-based healthcheck command to run inside new container
	HookTimeout    time.Duration // timeout for pre-update hook
}

type Client interface {
	ListRunningContainers(ctx context.Context) ([]Container, error)
	ListAllContainers(ctx context.Context) ([]Container, error)
	// PullImage pulls the image and returns the ImageID (for comparison)
	// and the RepoDigest (e.g. "repo@sha256:...") for immutable pinning when available.
	PullImage(ctx context.Context, image string) (string, string, error)
	RecreateContainer(ctx context.Context, c Container, newImage string, opts RecreateOptions) error
	RemoveImage(ctx context.Context, imageID string) error
	RenameContainer(ctx context.Context, containerID, newName string) error

	// SpawnWorker starts a temporary container (worker) from the provided image with
	// the given command, binds and labels. Returns the worker container ID.
	SpawnWorker(ctx context.Context, image string, cmd []string, name string, binds []string, labels map[string]string) (string, error)

	// ReplaceContainer waits for the target container to stop, removes it and recreates
	// it using the provided image while preserving configuration where possible.
	ReplaceContainer(ctx context.Context, targetID, newImage string) error
}

// sdkClient is the production implementation using the official Docker SDK
type dockerAPI interface {
	ImagePull(ctx context.Context, refStr string, options imageapi.PullOptions) (io.ReadCloser, error)
	ImageInspectWithRaw(ctx context.Context, image string) (types.ImageInspect, []byte, error)
	ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)
	ContainerRename(ctx context.Context, containerID, newName string) error
	ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options containertypes.StartOptions) error
	ContainerRemove(ctx context.Context, containerID string, options containertypes.RemoveOptions) error
	ContainerExecCreate(ctx context.Context, container string, config containertypes.ExecOptions) (containertypes.ExecCreateResponse, error)
	ContainerExecStart(ctx context.Context, execID string, config containertypes.ExecStartOptions) error
	ContainerExecInspect(ctx context.Context, execID string) (containertypes.ExecInspect, error)
	ContainerList(ctx context.Context, options containertypes.ListOptions) ([]types.Container, error)
	ImageRemove(ctx context.Context, image string, options imageapi.RemoveOptions) ([]imageapi.DeleteResponse, error)
}

type sdkClient struct {
	cli           dockerAPI
	registryAuth  string
	sanitizeNames bool
}

// SpawnWorker starts a temporary worker container using the specified image and
// command, mounting the provided binds and applying labels. It returns created container ID.
func (s *sdkClient) SpawnWorker(ctx context.Context, image string, cmd []string, name string, binds []string, labels map[string]string) (string, error) {
	cfg := &containertypes.Config{Image: image, Cmd: cmd, Labels: labels}
	hostCfg := &containertypes.HostConfig{Binds: binds, AutoRemove: true}
	resp, err := s.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("create worker: %w", err)
	}
	if err := s.cli.ContainerStart(ctx, resp.ID, containertypes.StartOptions{}); err != nil {
		return "", fmt.Errorf("start worker: %w", err)
	}
	return resp.ID, nil
}

// ReplaceContainer waits for the target container to stop, removes it and recreates
// it using the provided image while preserving configuration where possible.
func (s *sdkClient) ReplaceContainer(ctx context.Context, targetID, newImage string) error {
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
	_ = s.cli.ContainerRemove(ctx, targetID, containertypes.RemoveOptions{Force: true})

	if err := s.createAndStartReplacement(ctx, insp, newImage, origName); err != nil {
		return err
	}
	return nil
}

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
	return nil
}

// NewClient returns an SDK-backed Docker client
func NewClient() (Client, error) {
	return NewClientWithAuth("", "")
}

// NewClientWithAuthWithSanitize returns a client configured with simple registry auth
// and a sanitize-names option.
func NewClientWithAuthWithSanitize(user, pass string, sanitize bool) (Client, error) {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	s := &sdkClient{cli: c, sanitizeNames: sanitize}
	if user != "" || pass != "" {
		auth := map[string]string{"username": user, "password": pass}
		b, _ := json.Marshal(auth)
		s.registryAuth = base64.StdEncoding.EncodeToString(b)
	}
	return s, nil
}

// NewClientWithAuth returns a client configured with simple registry auth (username/password)
// and uses a default sanitize behavior (true).
func NewClientWithAuth(user, pass string) (Client, error) {
	return NewClientWithAuthWithSanitize(user, pass, true)
}

// NewClientForHost returns a client configured for a specific host endpoint
// host may be empty to indicate default behavior (FromEnv).
func NewClientForHost(host, user, pass string, sanitize bool) (Client, error) {
	opts := []client.Opt{client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	} else {
		opts = append(opts, client.FromEnv)
	}

	c, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}

	s := &sdkClient{cli: c, sanitizeNames: sanitize}
	if user != "" || pass != "" {
		auth := map[string]string{"username": user, "password": pass}
		b, _ := json.Marshal(auth)
		s.registryAuth = base64.StdEncoding.EncodeToString(b)
	}
	return s, nil
}

func (s *sdkClient) ListRunningContainers(ctx context.Context) ([]Container, error) {
	list, err := s.cli.ContainerList(ctx, containertypes.ListOptions{All: false})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(list))
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

func (s *sdkClient) ListAllContainers(ctx context.Context) ([]Container, error) {
	list, err := s.cli.ContainerList(ctx, containertypes.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(list))
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

func (s *sdkClient) RenameContainer(ctx context.Context, containerID, newName string) error {
	return s.cli.ContainerRename(ctx, containerID, newName)
}

func (s *sdkClient) PullImage(ctx context.Context, img string) (string, string, error) {
	logging.Get().Info().Str("image", img).Msg("pulling image")
	opts := imageapi.PullOptions{}
	if s.registryAuth != "" {
		opts.RegistryAuth = s.registryAuth
	}
	rc, err := s.cli.ImagePull(ctx, img, opts)
	if err != nil {
		logging.Get().Error().Err(err).Str("image", img).Msg("image pull failed")
		return "", "", fmt.Errorf("image pull %s: %w", img, err)
	}
	defer rc.Close()
	// consume stream to completion
	_, _ = io.Copy(io.Discard, rc)
	// inspect image to get ID
	inspected, _, err := s.cli.ImageInspectWithRaw(ctx, img)
	if err != nil {
		logging.Get().Error().Err(err).Str("image", img).Msg("inspect image failed")
		return "", "", fmt.Errorf("inspect image %s: %w", img, err)
	}
	// Determine a repo digest for pinning (if available)
	repoDigest := ""
	if len(inspected.RepoDigests) > 0 {
		repoDigest = inspected.RepoDigests[0]
	}
	logging.Get().Info().Str("image", img).Str("id", inspected.ID).Str("digest", repoDigest).Msg("pulled image")
	return inspected.ID, repoDigest, nil
}

func (s *sdkClient) RecreateContainer(ctx context.Context, ctn Container, newImage string, opts RecreateOptions) error {
	logging.Get().Info().Str("container", ctn.ID).Str("image", newImage).Msg("recreating container")

	// Pre-update hook
	if err := s.runPreUpdateHook(ctx, ctn, opts.HookTimeout); err != nil {
		return err
	}

	// Image was already pulled by the daemon earlier; skip redundant pull here to avoid double hits to registry.

	// Inspect existing container to replicate config
	insp, err := s.cli.ContainerInspect(ctx, ctn.ID)
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", ctn.ID, err)
	}

	// Rename old container to keep it around while we start the new one
	origName, tmpName, err := s.renameOldContainer(ctx, insp)
	if err != nil {
		return err
	}
	_ = tmpName // suppress unused in case

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

	return nil
}

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

// NOTE: image pulling is performed by the daemon prior to invoking RecreateContainer.
// The previous helper that called PullImage was removed to avoid redundant registry
// calls; container creation will fail and be handled by existing error paths if
// the image is missing.

// renameOldContainer renames the existing container to a temporary sanitized name
func (s *sdkClient) renameOldContainer(ctx context.Context, insp types.ContainerJSON) (string, string, error) {
	origName := strings.TrimPrefix(insp.Name, "/")
	origName = s.sanitizeName(origName)
	tmpName := fmt.Sprintf("%s-old-%d", origName, time.Now().UnixNano())
	logging.Get().Info().Str("old", insp.ID).Str("new_name", tmpName).Msg("renaming old container")
	if err := s.cli.ContainerRename(ctx, insp.ID, tmpName); err != nil {
		return "", "", fmt.Errorf("rename old container: %w", err)
	}
	// Record the rename so we can safely recover this specific rename if the daemon crashes
	if err := state.AddRenameRecord(state.RenameRecord{ContainerID: insp.ID, TmpName: tmpName, OrigName: origName, Timestamp: time.Now()}); err != nil {
		// non-fatal: log and continue
		logging.Get().Warn().Err(err).Str("container", insp.ID).Msg("failed to persist rename record")
	}
	return origName, tmpName, nil
}

// prepareNewContainerConfig prepares container and networking configs for a recreate operation
func (s *sdkClient) prepareNewContainerConfig(insp types.ContainerJSON, newImage string) (*containertypes.Config, *containertypes.HostConfig, *network.NetworkingConfig) {
	newCfg := insp.Config
	if newCfg == nil {
		newCfg = &containertypes.Config{}
	}
	newCfg.Image = newImage

	// Networking configuration (reuse endpoint settings if available)
	var netCfg *network.NetworkingConfig
	if insp.NetworkSettings != nil && insp.NetworkSettings.Networks != nil {
		netCfg = &network.NetworkingConfig{EndpointsConfig: insp.NetworkSettings.Networks}
	}

	// Use same host config if present
	hostCfg := insp.HostConfig
	return newCfg, hostCfg, netCfg
}

// createNewContainerWithRollback creates a new container and attempts to restore the old container name and state on failure
func (s *sdkClient) createNewContainerWithRollback(ctx context.Context, newCfg *containertypes.Config, hostCfg *containertypes.HostConfig, netCfg *network.NetworkingConfig, origName string, insp types.ContainerJSON) (containertypes.CreateResponse, error) {
	resp, err := s.cli.ContainerCreate(ctx, newCfg, hostCfg, netCfg, nil, origName)
	if err != nil {
		// try to restore old name before returning
		renameErr := s.cli.ContainerRename(ctx, insp.ID, origName)
		if renameErr == nil {
			// Clean up persistent state record since the rename was reverted
			if rmErr := state.RemoveRenameRecordByContainerID(insp.ID); rmErr != nil {
				logging.Get().Warn().Err(rmErr).Str("container", insp.ID).Msg("failed to remove rename record after create failure")
			}
		} else {
			logging.Get().Error().Err(renameErr).Str("container", insp.ID).Msg("failed to restore old name after create failure; keeping record for future recovery")
		}
		logging.Get().Error().Err(err).Msg("create new container failed; restored old name")
		metrics.IncRollback()
		return containertypes.CreateResponse{}, fmt.Errorf("create new container: %w", err)
	}
	return resp, nil
}

// startNewContainer starts the newly created container and rolls back on failure
func (s *sdkClient) startNewContainer(ctx context.Context, newID string, insp types.ContainerJSON, origName string) error {
	logging.Get().Info().Str("new_id", newID).Msg("starting new container")
	if err := s.cli.ContainerStart(ctx, newID, containertypes.StartOptions{}); err != nil {
		// start failed: remove new container and restore old
		logging.Get().Error().Err(err).Str("new_id", newID).Msg("start new container failed; rolling back")
		if remErr := s.cli.ContainerRemove(ctx, newID, containertypes.RemoveOptions{Force: true}); remErr != nil {
			logging.Get().Warn().Err(remErr).Str("new", newID).Msg("failed removing new container during rollback")
		}
		if renameErr := s.cli.ContainerRename(ctx, insp.ID, origName); renameErr != nil {
			logging.Get().Warn().Err(renameErr).Str("old", insp.ID).Msg("failed renaming old container back during rollback")
		} else {
			// Old name restored successfully; clean up persistent record
			if rmErr := state.RemoveRenameRecordByContainerID(insp.ID); rmErr != nil {
				logging.Get().Warn().Err(rmErr).Str("container", insp.ID).Msg("failed to remove rename record after rollback")
			}
		}
		if startErr := s.cli.ContainerStart(ctx, insp.ID, containertypes.StartOptions{}); startErr != nil {
			logging.Get().Warn().Err(startErr).Str("old", insp.ID).Msg("failed starting old container during rollback")
		}
		metrics.IncRollback()
		return fmt.Errorf("start new container %s: %w", newID, err)
	}
	return nil
}

// verifyNewContainer verifies the new container is running and healthy; it removes the old container on success or rolls back on failure
func (s *sdkClient) verifyNewContainer(ctx context.Context, newID string, insp types.ContainerJSON, origName string, opts RecreateOptions) error {
	deadline := time.Now().Add(opts.VerifyTimeout)
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("verification canceled: %w", ctx.Err())
		}
		done, err := s.evaluateNewContainerState(ctx, newID, insp, origName, opts, deadline)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			logging.Get().Warn().Str("new", newID).Msg("verification timed out; rolling back")
			metrics.IncRollback()
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("verification canceled: %w", ctx.Err())
		case <-time.After(opts.VerifyInterval):
		}
	}
	return s.rollbackRestore(ctx, newID, insp, origName)
}

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

// runNetworkHealthcheck performs HTTP/TCP checks if configured via labels
func (s *sdkClient) runNetworkHealthcheck(ctx context.Context, st types.ContainerJSON, deadline time.Time, interval time.Duration) error {
	ip := s.getContainerIP(st)

	// Check for HTTP Label
	if urlStr, ok := st.Config.Labels["dockhand.check.http"]; ok && urlStr != "" {
		if ip == "" {
			return fmt.Errorf("http check requested but container has no IP address")
		}
		return s.pollNetwork(ctx, "http", urlStr, ip, deadline, interval)
	}

	// Check for TCP Label
	if addrStr, ok := st.Config.Labels["dockhand.check.tcp"]; ok && addrStr != "" {
		if ip == "" {
			return fmt.Errorf("tcp check requested but container has no IP address")
		}
		return s.pollNetwork(ctx, "tcp", addrStr, ip, deadline, interval)
	}

	return nil
}

// getContainerIP returns the preferred IP for a container, preferring the primary NetworkSettings
// IPAddress and falling back to the first network endpoint IP if available.
func (s *sdkClient) getContainerIP(st types.ContainerJSON) string {
	if st.NetworkSettings == nil {
		return ""
	}
	ip := st.NetworkSettings.IPAddress
	if ip != "" {
		return ip
	}
	if st.NetworkSettings.Networks != nil {
		for _, n := range st.NetworkSettings.Networks {
			if n.IPAddress != "" {
				return n.IPAddress
			}
		}
	}
	return ""
}

// pollNetwork loops until the check succeeds or deadline is exceeded
func (s *sdkClient) pollNetwork(ctx context.Context, mode, target, ip string, deadline time.Time, interval time.Duration) error {
	var checkFunc func() error
	var err error
	if mode == "tcp" {
		checkFunc, err = s.buildTCPCheck(ctx, target, ip)
		if err != nil {
			return err
		}
	} else {
		checkFunc, err = s.buildHTTPCheck(ctx, target, ip)
		if err != nil {
			return err
		}
	}

	// Loop until deadline
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := checkFunc(); err == nil {
			logging.Get().Info().Str("mode", mode).Msg("network healthcheck succeeded")
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("%s check timed out", mode)
}

// buildTCPCheck constructs a TCP check function for the given target/ip
func (s *sdkClient) buildTCPCheck(ctx context.Context, target, ip string) (func() error, error) {
	address := target
	if !strings.Contains(target, ":") {
		address = net.JoinHostPort(ip, target)
	} else {
		_, port, _ := net.SplitHostPort(target)
		address = net.JoinHostPort(ip, port)
	}
	check := func() error {
		d := net.Dialer{Timeout: 1 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}
	return check, nil
}

// buildHTTPCheck constructs an HTTP check function that rewrites the host to the container IP
func (s *sdkClient) buildHTTPCheck(ctx context.Context, target, ip string) (func() error, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	_, port, _ := net.SplitHostPort(u.Host)
	if port == "" {
		if strings.Contains(u.Host, ":") {
			_, port, _ = net.SplitHostPort(u.Host)
		} else {
			port = "80"
		}
	}
	u.Host = net.JoinHostPort(ip, port)
	finalURL := u.String()

	check := func() error {
		client := http.Client{Timeout: 1 * time.Second}
		req, _ := http.NewRequestWithContext(ctx, "GET", finalURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return nil
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return check, nil
}
func (s *sdkClient) RemoveImage(ctx context.Context, imageID string) error {
	logging.Get().Info().Str("image", imageID).Msg("removing image")
	_, err := s.cli.ImageRemove(ctx, imageID, imageapi.RemoveOptions{Force: false, PruneChildren: false})
	if err != nil {
		logging.Get().Error().Err(err).Str("image", imageID).Msg("failed removing image")
		return fmt.Errorf("remove image %s: %w", imageID, err)
	}
	logging.Get().Info().Str("image", imageID).Msg("removed image")
	return nil
}
