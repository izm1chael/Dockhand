package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateCRUD(t *testing.T) {
    dir := t.TempDir()
    // Use temp dir for state file
    os.Setenv("DOCKHAND_STATE_DIR", dir)
    defer os.Unsetenv("DOCKHAND_STATE_DIR")

    r := RenameRecord{
        ContainerID: "cid-1",
        TmpName:     "tmp-a",
        OrigName:    "orig-a",
        Timestamp:   time.Now().UTC(),
    }

    if err := AddRenameRecord(r); err != nil {
        t.Fatalf("AddRenameRecord failed: %v", err)
    }

    got, ok, err := GetRenameRecordByTmpName(r.TmpName)
    if err != nil {
        t.Fatalf("GetRenameRecordByTmpName returned error: %v", err)
    }
    if !ok {
        t.Fatalf("expected record to exist")
    }
    if got.ContainerID != r.ContainerID || got.OrigName != r.OrigName {
        t.Fatalf("record mismatch: got %+v want %+v", got, r)
    }

    // Add second record
    r2 := RenameRecord{ContainerID: "cid-2", TmpName: "tmp-b", OrigName: "orig-b", Timestamp: time.Now().UTC()}
    if err := AddRenameRecord(r2); err != nil {
        t.Fatalf("AddRenameRecord r2 failed: %v", err)
    }

    all, err := GetAllRenameRecords()
    if err != nil {
        t.Fatalf("GetAllRenameRecords failed: %v", err)
    }
    if len(all) != 2 {
        t.Fatalf("expected 2 records, got %d", len(all))
    }

    // Remove by tmp name
    if err := RemoveRenameRecordByTmpName(r.TmpName); err != nil {
        t.Fatalf("RemoveRenameRecordByTmpName failed: %v", err)
    }
    all, err = GetAllRenameRecords()
    if err != nil {
        t.Fatalf("GetAllRenameRecords after remove failed: %v", err)
    }
    if len(all) != 1 {
        t.Fatalf("expected 1 record after remove, got %d", len(all))
    }

    // Remove by container ID
    if err := RemoveRenameRecordByContainerID(r2.ContainerID); err != nil {
        t.Fatalf("RemoveRenameRecordByContainerID failed: %v", err)
    }
    all, err = GetAllRenameRecords()
    if err != nil {
        t.Fatalf("GetAllRenameRecords after remove by id failed: %v", err)
    }
    if len(all) != 0 {
        t.Fatalf("expected 0 records after remove by id, got %d", len(all))
    }

    // Ensure file exists in temp dir (state file was created then removed)
    stateFile := filepath.Join(dir, stateFileName)
    if _, err := os.Stat(stateFile); err == nil {
        // file may exist (empty map saved) or be removed; ensure cleanup on test end
        os.Remove(stateFile)
    }
}
