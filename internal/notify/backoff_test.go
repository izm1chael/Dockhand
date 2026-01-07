package notify

import (
	"context"
	"errors"
	"testing"
	"time"
)

// controlled is a helper fake used in tests
type controlled struct{ calls int }

func (c *controlled) Send(ctx context.Context, title, message string) error {
	c.calls++
	if c.calls < 3 {
		return errors.New("temp")
	}
	return nil
}

func (c *controlled) Name() string { return "controlled" }

func TestBackoffJitterAndSleepHook(t *testing.T) {
	m := NewMultiNotifier()
	oldSleep := sleepHook
	durations := make([]time.Duration, 0)
	sleepHook = func(d time.Duration) { durations = append(durations, d) }
	t.Cleanup(func() { sleepHook = oldSleep })

	oldBase := notifierBaseBackoff
	oldJitter := notifierBackoffJitter
	notifierBaseBackoff = 10 * time.Millisecond
	notifierBackoffJitter = 20 * time.Millisecond
	defer func() { notifierBaseBackoff = oldBase; notifierBackoffJitter = oldJitter }()

	ctl := &controlled{}
	m.Add(ctl)
	m.SetCooldown(0)
	notifierMaxRetries = 3
	m.Send(context.Background(), "T", "M")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	// we expect two sleep durations (after attempts 1 & 2)
	if len(durations) != 2 {
		t.Fatalf("expected 2 backoff sleeps, got %d", len(durations))
	}
	for _, d := range durations {
		if d < notifierBaseBackoff {
			t.Fatalf("expected sleep >= base backoff, got %v", d)
		}
	}
}
