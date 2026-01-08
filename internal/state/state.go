package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RenameRecord records a rename performed by Dockhand
type RenameRecord struct {
	ContainerID string    `json:"container_id"`
	TmpName     string    `json:"tmp_name"`
	OrigName    string    `json:"orig_name"`
	Timestamp   time.Time `json:"timestamp"`
}

var mu sync.Mutex

const stateFileName = "dockhand_state.json"

func stateFilePath() string {
	if dir := os.Getenv("DOCKHAND_STATE_DIR"); dir != "" {
		// Validate and canonicalize the provided directory to avoid
		// accidental path traversal or relative paths coming from the
		// environment. If the env value cannot be resolved to an absolute
		// clean path, ignore it and fall through to defaults.
		dir = filepath.Clean(dir)
		if !filepath.IsAbs(dir) {
			if abs, err := filepath.Abs(dir); err == nil {
				dir = abs
			} else {
				dir = ""
			}
		}
		if dir != "" {
			return filepath.Join(dir, stateFileName)
		}
	}
	// Prefer a persistent location under /var/lib/dockhand when possible; fall back to the current working dir
	// to avoid relying on ephemeral temp directories that may be cleared on reboot.
	defaultDir := "/var/lib/dockhand"
	// Try to create default directory with restrictive permissions; if not possible, fall back to cwd
	if err := os.MkdirAll(defaultDir, 0o700); err == nil {
		return filepath.Join(defaultDir, stateFileName)
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, stateFileName)
	}
	// Last resort: use temp dir
	return filepath.Join(os.TempDir(), stateFileName)
}

// loadAll reads state file WITHOUT acquiring the package mutex. Caller must hold the lock if concurrent access is possible.
func loadAllUnlocked() (map[string]RenameRecord, error) {
	p := stateFilePath()
	// Sanitize and validate the produced path to avoid path traversal or unexpected filenames.
	p = filepath.Clean(p)
	if filepath.Base(p) != stateFileName {
		return nil, fmt.Errorf("invalid state file path")
	}
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolve state file path: %w", err)
		}
		p = abs
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]RenameRecord), nil
		}
		return nil, fmt.Errorf("load state: %w", err)
	}
	out := make(map[string]RenameRecord)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}
	return out, nil
}

// saveAll writes state file WITHOUT acquiring the package mutex. Caller must hold the lock if concurrent access is possible.
func saveAllUnlocked(m map[string]RenameRecord) error {
	p := stateFilePath()
	// Sanitize and validate the produced path
	p = filepath.Clean(p)
	if filepath.Base(p) != stateFileName {
		return fmt.Errorf("invalid state file path")
	}
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("resolve state file path: %w", err)
		}
		p = abs
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	// Ensure directory exists with restrictive permissions
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	if err := os.WriteFile(p, b, 0o640); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	return nil
}

// AddRenameRecord persists a rename record keyed by the temporary name. This function holds the package mutex
// for the entire read-modify-write cycle to avoid lost updates.
func AddRenameRecord(r RenameRecord) error {
	mu.Lock()
	defer mu.Unlock()
	m, err := loadAllUnlocked()
	if err != nil {
		return err
	}
	m[r.TmpName] = r
	return saveAllUnlocked(m)
}

// RemoveRenameRecordByTmpName removes a rename record by the temporary name. Protected by the package mutex.
func RemoveRenameRecordByTmpName(tmp string) error {
	mu.Lock()
	defer mu.Unlock()
	m, err := loadAllUnlocked()
	if err != nil {
		return err
	}
	delete(m, tmp)
	return saveAllUnlocked(m)
}

// RemoveRenameRecordByContainerID removes any records matching the container ID. Protected by the package mutex.
func RemoveRenameRecordByContainerID(containerID string) error {
	mu.Lock()
	defer mu.Unlock()
	m, err := loadAllUnlocked()
	if err != nil {
		return err
	}
	for k, v := range m {
		if v.ContainerID == containerID {
			delete(m, k)
		}
	}
	return saveAllUnlocked(m)
}

// GetRenameRecordByTmpName looks up a record by temporary name
func GetRenameRecordByTmpName(tmp string) (RenameRecord, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	m, err := loadAllUnlocked()
	if err != nil {
		return RenameRecord{}, false, err
	}
	r, ok := m[tmp]
	return r, ok, nil
}

// GetAllRenameRecords returns all persisted rename records
func GetAllRenameRecords() (map[string]RenameRecord, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadAllUnlocked()
}
