package metrics

import (
	"testing"
	"time"
)

func TestMetricsCounters(t *testing.T) {
	// Get initial state
	s := GetSnapshot()
	initialUpdates := s.Updates
	initialFailed := s.UpdatesFailed
	initialRollbacks := s.Rollbacks
	initialCleanup := s.CleanupFailed
	initialPatchSkips := s.PatchWindowSkips
	initialPullSuccess := s.ImagePullsSuccess
	initialPullFailure := s.ImagePullsFailure

	IncUpdate()
	IncUpdateFailed()
	IncRollback()
	IncCleanupFailed()
	IncPatchWindowSkip()
	IncImagePullSuccess()
	IncImagePullFailure()
	SetLastRun(time.Unix(123456789, 0))

	s2 := GetSnapshot()
	if s2.Updates != initialUpdates+1 {
		t.Fatalf("expected updates to increment by 1, got %d", s2.Updates)
	}
	if s2.UpdatesFailed != initialFailed+1 {
		t.Fatalf("expected updates_failed to increment by 1, got %d", s2.UpdatesFailed)
	}
	if s2.Rollbacks != initialRollbacks+1 {
		t.Fatalf("expected rollbacks to increment by 1, got %d", s2.Rollbacks)
	}
	if s2.CleanupFailed != initialCleanup+1 {
		t.Fatalf("expected cleanup_failed to increment by 1, got %d", s2.CleanupFailed)
	}
	if s2.PatchWindowSkips != initialPatchSkips+1 {
		t.Fatalf("expected patch_window_skips to increment by 1, got %d", s2.PatchWindowSkips)
	}
	if s2.ImagePullsSuccess != initialPullSuccess+1 {
		t.Fatalf("expected image_pulls_success to increment by 1, got %d", s2.ImagePullsSuccess)
	}
	if s2.ImagePullsFailure != initialPullFailure+1 {
		t.Fatalf("expected image_pulls_failure to increment by 1, got %d", s2.ImagePullsFailure)
	}
	if s2.LastRun != 123456789 {
		t.Fatalf("expected last run timestamp 123456789, got %d", s2.LastRun)
	}
	if s2.LastRunHuman == "" {
		t.Fatal("expected non-empty LastRunHuman")
	}
}

func TestObserveUpdateDuration(t *testing.T) {
	// Just verify the function doesn't panic
	ObserveUpdateDuration(1.5)
	ObserveUpdateDuration(30.0)
	ObserveUpdateDuration(120.5)
}

func TestPromHandler(t *testing.T) {
	handler := PromHandler()
	if handler == nil {
		t.Fatal("PromHandler returned nil")
	}
}

func TestJSONHandler(t *testing.T) {
	handler := JSONHandler()
	if handler == nil {
		t.Fatal("JSONHandler returned nil")
	}
}
