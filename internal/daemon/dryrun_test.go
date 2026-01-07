package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dockhand/dockhand/internal/config"
)

func TestDaemonDryRunSkipsRecreate(t *testing.T) {
	var payload map[string]interface{}
	called := false
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &payload)
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer h.Close()

	fc := &fakeClient{pulledID: "sha256:new"}
	cfg := config.DefaultConfig()
	cfg.GenericWebhookURL = h.URL
	cfg.PatchWindow = "00:00-23:59"
	cfg.DryRun = true
	d := New(cfg, []Host{{Name: "test", Client: fc}})
	d.RunOnce()
	if fc.recreated != 0 {
		t.Fatalf("expected recreate to be skipped in dry-run, got %d", fc.recreated)
	}
	// wait for async notification sends to complete
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := d.notifier.Wait(ctx); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if !called {
		t.Fatalf("expected notifier to be called for dry-run notification")
	}
	if payload == nil {
		t.Fatalf("expected payload in dry-run notifier")
	}
	if agent, ok := payload["agent"].(string); !ok || agent != "Dockhand" {
		t.Fatalf("expected agent 'Dockhand', got %v", payload["agent"])
	}
	if title, ok := payload["title"].(string); !ok || title != "Update available (dry-run): c1" {
		t.Fatalf("expected title 'Update available (dry-run): c1', got %v", payload["title"])
	}
}
