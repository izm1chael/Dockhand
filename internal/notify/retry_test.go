package notify

import (
	"context"
	"testing"
	"time"
)

func TestMultiNotifierRetriesAndCooldown(t *testing.T) {
	m := NewMultiNotifier()
	// make sleep hook a no-op so test runs quickly
	oldSleep := sleepHook
	// no-op sleep to avoid slowing tests
	sleepHook = func(d time.Duration) { /* no-op: speed up tests by avoiding real sleeps */ }
	t.Cleanup(func() { sleepHook = oldSleep })
	oldMax := notifierMaxRetries
	notifierMaxRetries = 3
	defer func() { notifierMaxRetries = oldMax }()
	m.SetCooldown(0)
	// also test provider-specific cooldown override
	m.SetProviderCooldown("controlled", 1*time.Second)

	ctl := &controlled{}
	m.Add(ctl)
	m.Send(context.Background(), "T", "M")
	// wait for async sends to complete
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	// after a single Send call, controlled.calls should be 3 (2 failures + 1 success)
	if ctl.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", ctl.calls)
	}

	// test cooldown: mark lastSent as now and ensure subsequent send is skipped
	m.SetCooldown(1 * time.Minute)
	m.lastSent["controlled"] = time.Now()
	ctl.calls = 0
	m.Send(context.Background(), "T2", "M2")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()
	if err := m.Wait(ctx2); err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if ctl.calls != 0 {
		t.Fatalf("expected 0 attempts due to cooldown, got %d", ctl.calls)
	}
}
