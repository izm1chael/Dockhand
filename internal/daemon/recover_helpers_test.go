package daemon

import (
	"testing"
	"time"

	"github.com/dockhand/dockhand/internal/docker"
	"github.com/dockhand/dockhand/internal/state"
)

func TestFindStaleEntries(t *testing.T) {
	c1 := docker.Container{ID: "a", Names: []string{"/app-old-123"}}
	c2 := docker.Container{ID: "b", Names: []string{"/app"}}
	list := []docker.Container{c1, c2}
	d := &Daemon{}
	existing := d.buildExistingNames(list)
	entries := d.findStaleEntries(list, existing)
	if len(entries) != 0 {
		t.Fatalf("expected 0 stale entries when original exists, got %d", len(entries))
	}

	// Now remove the original but without a recorded rename => no stale entries
	list = []docker.Container{c1}
	existing = d.buildExistingNames(list)
	entries = d.findStaleEntries(list, existing)
	if len(entries) != 0 {
		t.Fatalf("expected 0 stale entries without a recorded rename, got %d", len(entries))
	}

	// Now add a recorded rename and expect it to be considered
	t.Setenv("DOCKHAND_STATE_DIR", t.TempDir())
	if err := state.AddRenameRecord(state.RenameRecord{ContainerID: "a", TmpName: "app-old-123", OrigName: "app", Timestamp: time.Now()}); err != nil {
		t.Fatalf("failed to setup state: %v", err)
	}

	entries = d.findStaleEntries(list, existing)
	if len(entries) != 1 {
		t.Fatalf("expected 1 stale entry when recorded, got %d", len(entries))
	}
	if entries[0].orig != "app" {
		t.Fatalf("expected orig 'app', got %s", entries[0].orig)
	}
}
