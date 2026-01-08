package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/dockhand/dockhand/internal/config"
	"github.com/dockhand/dockhand/internal/docker"
	"github.com/dockhand/dockhand/internal/state"
)

type fakeClientForRecover struct {
	containers []docker.Container
	renamed    map[string]string // containerID -> newName
}

func (f *fakeClientForRecover) ListRunningContainers(ctx context.Context) ([]docker.Container, error) {
	return nil, nil
}
func (f *fakeClientForRecover) ListAllContainers(ctx context.Context) ([]docker.Container, error) {
	return f.containers, nil
}
func (f *fakeClientForRecover) PullImage(ctx context.Context, image string) (string, string, error) {
	return "", "", nil
}
func (f *fakeClientForRecover) RecreateContainer(ctx context.Context, c docker.Container, newImage string, opts docker.RecreateOptions) error {
	return nil
}
func (f *fakeClientForRecover) RemoveImage(ctx context.Context, imageID string) error { return nil }
func (f *fakeClientForRecover) RenameContainer(ctx context.Context, containerID, newName string) error {
	if f.renamed == nil {
		f.renamed = make(map[string]string)
	}
	f.renamed[containerID] = newName
	return nil
}
func (f *fakeClientForRecover) SpawnWorker(ctx context.Context, image string, cmd []string, opts docker.WorkerOptions) (string, error) {
	return "", nil
}
func (f *fakeClientForRecover) ReplaceContainer(ctx context.Context, targetID, newImage string) error {
	return nil
}

func TestRecoverStaleOldRenamesWhenNoNewContainer(t *testing.T) {
	cfg := config.DefaultConfig()
	// configure state dir for test
	t.Setenv("DOCKHAND_STATE_DIR", t.TempDir())
	// record the rename that we expect to recover
	if err := state.AddRenameRecord(state.RenameRecord{ContainerID: "oldid", TmpName: "myapp-old-123", OrigName: "myapp", Timestamp: time.Now()}); err != nil {
		t.Fatalf("failed to setup state: %v", err)
	}
	fc := &fakeClientForRecover{
		containers: []docker.Container{{ID: "oldid", Names: []string{"/myapp-old-123"}}},
	}
	d := &Daemon{cfg: cfg, hosts: []Host{{Name: "test", Client: fc}}, notifier: nil}
	d.recoverStaleOldContainers(context.Background())
	if newName, ok := fc.renamed["oldid"]; !ok || newName != "myapp" {
		t.Fatalf("expected rename of oldid to myapp, got %v", fc.renamed)
	}
}

func TestRecoverStaleOldNoRenameWhenNewExists(t *testing.T) {
	cfg := config.DefaultConfig()
	fc := &fakeClientForRecover{
		containers: []docker.Container{
			{ID: "oldid", Names: []string{"/myapp-old-123"}},
			{ID: "newid", Names: []string{"/myapp"}},
		},
	}
	d := &Daemon{cfg: cfg, hosts: []Host{{Name: "test", Client: fc}}, notifier: nil}
	d.recoverStaleOldContainers(context.Background())
	if len(fc.renamed) != 0 {
		t.Fatalf("expected no renames, got %v", fc.renamed)
	}
}
