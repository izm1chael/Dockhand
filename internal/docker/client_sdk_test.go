package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/dockhand/dockhand/internal/state"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	testOldID    = "old-id"
	testNewImage = "new-image"
)

// fakeDockerAPI implements the subset of Docker client methods used by sdkClient
type fakeDockerAPI struct {
	renamed       map[string]string
	createdName   string
	createdID     string
	started       []string
	removed       []string
	removedImages []string
	execInspects  map[string]types.ContainerExecInspect
	list          []types.Container
	// inspectNames allows tests to specify the Name returned for a container ID
	inspectNames map[string]string
}

func (f *fakeDockerAPI) ImagePull(ctx context.Context, refStr string, options types.ImagePullOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeDockerAPI) ImageInspectWithRaw(ctx context.Context, image string) (types.ImageInspect, []byte, error) {
	return types.ImageInspect{ID: "sha256:new"}, nil, nil
}

func (f *fakeDockerAPI) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	// For new container id "new-id" return running state
	state := &types.ContainerState{Running: true}
	name := "/new"
	if f.inspectNames != nil {
		if v, ok := f.inspectNames[containerID]; ok {
			name = "/" + v
		}
	}
	if containerID == testOldID && name == "/new" {
		name = "/orig"
	}
	return types.ContainerJSON{ContainerJSONBase: &types.ContainerJSONBase{ID: containerID, Name: name, Image: "new-image", HostConfig: &containertypes.HostConfig{}, State: state}, Config: &containertypes.Config{}, NetworkSettings: &types.NetworkSettings{Networks: map[string]*network.EndpointSettings{}}}, nil
}

func (f *fakeDockerAPI) ContainerRename(ctx context.Context, containerID, newName string) error {
	if f.renamed == nil {
		f.renamed = map[string]string{}
	}
	f.renamed[containerID] = newName
	return nil
}

func (f *fakeDockerAPI) ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
	f.createdName = containerName
	f.createdID = "new-id"
	return containertypes.CreateResponse{ID: f.createdID}, nil
}

func (f *fakeDockerAPI) ContainerStart(ctx context.Context, containerID string, options types.ContainerStartOptions) error {
	f.started = append(f.started, containerID)
	return nil
}

func (f *fakeDockerAPI) ContainerRemove(ctx context.Context, containerID string, options types.ContainerRemoveOptions) error {
	f.removed = append(f.removed, containerID)
	return nil
}

func (f *fakeDockerAPI) ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error) {
	return f.list, nil
}

func (f *fakeDockerAPI) ImageRemove(ctx context.Context, image string, options types.ImageRemoveOptions) ([]types.ImageDeleteResponseItem, error) {
	f.removedImages = append(f.removedImages, image)
	return nil, nil
}

func (f *fakeDockerAPI) ContainerExecCreate(ctx context.Context, container string, config types.ExecConfig) (types.IDResponse, error) {
	return types.IDResponse{ID: "exec1"}, nil
}

func (f *fakeDockerAPI) ContainerExecStart(ctx context.Context, execID string, config types.ExecStartCheck) error {
	// no-op
	return nil
}

func (f *fakeDockerAPI) ContainerExecInspect(ctx context.Context, execID string) (types.ContainerExecInspect, error) {
	if v, ok := f.execInspects[execID]; ok {
		return v, nil
	}
	return types.ContainerExecInspect{ExitCode: 1}, nil
}

// createFail simulates a failure during container creation
type createFail struct{ fakeDockerAPI }

func (f *createFail) ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
	return containertypes.CreateResponse{}, fmt.Errorf("create failed")
}

// removeFail simulates a failure when attempting to remove a specific container
type removeFail struct{ fakeDockerAPI }

func (f *removeFail) ContainerRemove(ctx context.Context, containerID string, options types.ContainerRemoveOptions) error {
	f.removed = append(f.removed, containerID)
	if containerID == testOldID {
		return fmt.Errorf("remove failed")
	}
	return nil
}

// The real client has many more methods; we only implement the subset we need for tests.

func TestRecreateWithExecHealthcheckSuccess(t *testing.T) {
	fake := &fakeDockerAPI{execInspects: map[string]types.ContainerExecInspect{"exec1": {ExitCode: 0, Running: false}}}
	s := &sdkClient{cli: fake}
	ctx := context.Background()
	opts := RecreateOptions{VerifyTimeout: 5 * time.Second, VerifyInterval: 100 * time.Millisecond, HealthcheckCmd: []string{"/bin/true"}}
	err := s.RecreateContainer(ctx, Container{ID: testOldID}, testNewImage, opts)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	// ensure old container was removed
	found := false
	for _, id := range fake.removed {
		if id == testOldID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected old container to be removed, removed=%v", fake.removed)
	}
}

func TestRenameRecordRemovedOnCreateFailure(t *testing.T) {
	// ensure state file is isolated
	t.Setenv("DOCKHAND_STATE_DIR", t.TempDir())
	// fake that fails ContainerCreate
	f := &createFail{}
	s := &sdkClient{cli: f}
	ctx := context.Background()
	opts := RecreateOptions{VerifyTimeout: 1 * time.Second, VerifyInterval: 100 * time.Millisecond}
	err := s.RecreateContainer(ctx, Container{ID: testOldID}, testNewImage, opts)
	if err == nil {
		t.Fatalf("expected error when create fails")
	}
	// state should be clean (no zombie records)
	m, err := state.GetAllRenameRecords()
	if err != nil {
		t.Fatalf("failed reading state: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected no rename records after create failure, got %v", m)
	}
}

func TestRemoveOldFailureDoesNotCauseRecreateFailure(t *testing.T) {
	// ensure state file is isolated
	t.Setenv("DOCKHAND_STATE_DIR", t.TempDir())
	// fake that fails ContainerRemove for the old ID
	f := &removeFail{}
	f.execInspects = map[string]types.ContainerExecInspect{"exec1": {ExitCode: 0, Running: false}}
	s := &sdkClient{cli: f}
	ctx := context.Background()
	opts := RecreateOptions{VerifyTimeout: 2 * time.Second, VerifyInterval: 100 * time.Millisecond, HealthcheckCmd: []string{"/bin/true"}}
	err := s.RecreateContainer(ctx, Container{ID: testOldID}, testNewImage, opts)
	if err != nil {
		t.Fatalf("expected success even if old remove fails, got %v", err)
	}
	// verify it attempted to remove the old container
	found := false
	for _, id := range f.removed {
		if id == testOldID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected remove to be attempted for old container, removed=%v", f.removed)
	}
}

func TestRecreateWithExecHealthcheckFailureRollsBack(t *testing.T) {
	fake := &fakeDockerAPI{execInspects: map[string]types.ContainerExecInspect{"exec1": {ExitCode: 1, Running: false}}}
	s := &sdkClient{cli: fake}
	ctx := context.Background()
	opts := RecreateOptions{VerifyTimeout: 1 * time.Second, VerifyInterval: 100 * time.Millisecond, HealthcheckCmd: []string{"/bin/false"}}
	err := s.RecreateContainer(ctx, Container{ID: testOldID}, testNewImage, opts)
	if err == nil {
		t.Fatalf("expected error on failed healthcheck")
	}
	// ensure new container was removed and old was attempted to be restored (renamed back)
	foundNewRemoved := false
	for _, id := range fake.removed {
		if id == fake.createdID || id == "new-id" {
			foundNewRemoved = true
		}
	}
	if !foundNewRemoved {
		t.Fatalf("expected new container to be removed on rollback, removed=%v", fake.removed)
	}
}

func TestPreUpdateHookTimeout(t *testing.T) {
	// Exec never completes (always running) -> should timeout and cause RecreateContainer to fail
	fake := &fakeDockerAPI{execInspects: map[string]types.ContainerExecInspect{"exec1": {ExitCode: 1, Running: true}}}
	s := &sdkClient{cli: fake}
	ctx := context.Background()
	opts := RecreateOptions{VerifyTimeout: 1 * time.Second, VerifyInterval: 100 * time.Millisecond, HookTimeout: 100 * time.Millisecond}
	err := s.RecreateContainer(ctx, Container{ID: testOldID, Labels: map[string]string{"dockhand.pre-update": "sleep 10"}}, testNewImage, opts)
	if err == nil {
		t.Fatalf("expected error due to pre-update hook timeout")
	}
	if !strings.Contains(err.Error(), "pre-update exec timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestListRunningContainers(t *testing.T) {
	fake := &fakeDockerAPI{
		list: []types.Container{
			{ID: "c1", Image: "image1:latest", ImageID: "sha256:abc", Labels: map[string]string{"test": "true"}, Names: []string{"/container1"}},
			{ID: "c2", Image: "image2:v1.0", ImageID: "sha256:def", Labels: map[string]string{}, Names: []string{"/container2"}},
		},
	}
	s := &sdkClient{cli: fake}
	ctx := context.Background()

	containers, err := s.ListRunningContainers(ctx)
	if err != nil {
		t.Fatalf("ListRunningContainers failed: %v", err)
	}

	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}

	if containers[0].ID != "c1" || containers[0].Image != "image1:latest" {
		t.Errorf("container 0 mismatch: got %+v", containers[0])
	}

	if containers[1].ID != "c2" || containers[1].Image != "image2:v1.0" {
		t.Errorf("container 1 mismatch: got %+v", containers[1])
	}
}

func TestPullImage(t *testing.T) {
	fake := &fakeDockerAPI{}
	s := &sdkClient{cli: fake}
	ctx := context.Background()

	imageID, err := s.PullImage(ctx, "test:latest")
	if err != nil {
		t.Fatalf("PullImage failed: %v", err)
	}

	if imageID != "sha256:new" {
		t.Errorf("expected image ID sha256:new, got %s", imageID)
	}
}

func TestRemoveImage(t *testing.T) {
	fake := &fakeDockerAPI{}
	s := &sdkClient{cli: fake}
	ctx := context.Background()

	err := s.RemoveImage(ctx, "sha256:old")
	if err != nil {
		t.Fatalf("RemoveImage failed: %v", err)
	}

	if len(fake.removedImages) != 1 || fake.removedImages[0] != "sha256:old" {
		t.Errorf("expected image sha256:old to be removed, got %v", fake.removedImages)
	}
}

func TestNewClientWithAuth(t *testing.T) {
	// This test just verifies the client can be created with auth
	// We can't test the actual Docker connection without a running daemon
	// But we can verify the code path compiles and doesn't panic
	_, err := NewClientWithAuth("testuser", "testpass")
	// In a test environment without Docker, this may fail, which is expected
	// We're just verifying the code compiles
	_ = err
}

func TestNewClient(t *testing.T) {
	// Similar to above - just verify the code path works
	_, err := NewClient()
	_ = err
}
