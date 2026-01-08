package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dockhand/dockhand/internal/config"
	"github.com/dockhand/dockhand/internal/notify"
)

const waitFailMsg = "notifier Wait failed: %v"

type fakeSvc struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeSvc) Send(ctx context.Context, title, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, title+"|"+message)
	return nil
}

func (f *fakeSvc) Name() string { return "fake" }

func TestNotificationLevelFiltering(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PatchWindow = "00:00-23:59"
	cfg.PollInterval = 1 * time.Hour

	d := New(cfg, nil)
	f := &fakeSvc{}
	d.notifier = notify.NewMultiNotifier()
	// FIX: Use a tiny non-zero cooldown. 0 often implies "use default cooldown",
	// which can suppress immediately-sequential notifications. 1ns effectively
	// disables the cooldown behavior for fast, sequential tests.
	d.notifier.SetCooldown(1 * time.Nanosecond)
	d.notifier.Add(f)

	// default (all)
	d.cfg.NotificationLevel = "all"
	d.notify(context.Background(), "success", "S", "ok")
	// Ensure the tiny cooldown has time to expire on fast machines.
	time.Sleep(1 * time.Millisecond)
	d.notify(context.Background(), "failure", "F", "err")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := d.notifier.Wait(ctx); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected 2 calls for 'all', got %d", len(f.calls))
	}

	// failure only
	f.calls = nil
	d.cfg.NotificationLevel = "failure"
    d.notify(context.Background(), "success", "S", "ok")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()
	if err := d.notifier.Wait(ctx2); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("expected 0 calls for 'failure' when sending success, got %d", len(f.calls))
	}
	// Small sleep to ensure cooldown doesn't affect the next valid call
	time.Sleep(1 * time.Millisecond)

	d.notify(context.Background(), "failure", "F", "err")
	ctx3, cancel3 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel3()
	if err := d.notifier.Wait(ctx3); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected 1 call for 'failure' when sending failure, got %d", len(f.calls))
	}

	// none
	f.calls = nil
	d.cfg.NotificationLevel = "none"
	d.notify(context.Background(), "failure", "F", "err")
	ctx4, cancel4 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel4()
	if err := d.notifier.Wait(ctx4); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("expected 0 calls for 'none', got %d", len(f.calls))
	}
}
