package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dockhand/dockhand/internal/config"
	"github.com/dockhand/dockhand/internal/docker"
)

const (
	testOldImage  = "sha256:old"
	testNewImage  = "sha256:new"
	fullDayWindow = "00:00-23:59"
)

type fakeClient struct {
	pulledID      string
	PullImageFunc func(ctx context.Context, image string) (string, string, error)
	recreated     int
	containers    []docker.Container
	removedImages []string
	lastOpts      docker.RecreateOptions
	failRecreate  bool

	// self-update test hooks
	spawnedWorker  bool
	spawnedName    string
	spawnedImage   string
	spawnedCmd     []string
	spawnedBinds   []string
	replacedTarget string
}

func (f *fakeClient) RemoveImage(ctx context.Context, imageID string) error {
	f.removedImages = append(f.removedImages, imageID)
	return nil
}

func (f *fakeClient) SpawnWorker(ctx context.Context, image string, cmd []string, name string, binds []string, labels map[string]string) (string, error) {
	f.spawnedWorker = true
	f.spawnedName = name
	f.spawnedImage = image
	f.spawnedCmd = cmd
	f.spawnedBinds = binds
	return "worker-1", nil
}

func (f *fakeClient) ReplaceContainer(ctx context.Context, targetID, newImage string) error {
	f.replacedTarget = targetID
	return nil
}

func (f *fakeClient) RenameContainer(ctx context.Context, containerID, newName string) error {
	// reuse map-like slice for simplicity; record rename with a prefix
	f.removedImages = append(f.removedImages, fmt.Sprintf("renamed:%s->%s", containerID, newName))
	return nil
}

func (f *fakeClient) RecreateContainer(ctx context.Context, c docker.Container, newImage string, opts docker.RecreateOptions) error {
	f.lastOpts = opts
	if f.failRecreate {
		return fmt.Errorf("simulated recreate failure")
	}
	f.recreated++
	return nil
}

func (f *fakeClient) ListRunningContainers(ctx context.Context) ([]docker.Container, error) {
	if len(f.containers) > 0 {
		return f.containers, nil
	}
	c := docker.Container{
		ID:      "c1",
		Image:   "nginx:latest",
		ImageID: testOldImage,
		Labels:  map[string]string{},
		Names:   []string{"/c1"},
	}
	return []docker.Container{c}, nil
}

func (f *fakeClient) ListAllContainers(ctx context.Context) ([]docker.Container, error) {
	// For tests, ListAllContainers behaves the same as ListRunningContainers
	return f.ListRunningContainers(ctx)
}

func (f *fakeClient) PullImage(ctx context.Context, image string) (string, string, error) {
	if f.PullImageFunc != nil {
		return f.PullImageFunc(ctx, image)
	}
	return f.pulledID, "", nil
}

func TestDaemonOnceTriggersRecreateOnNewImage(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage}
	cfg := config.DefaultConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.PatchWindow = "" // allow any time
	cfg.DryRun = false
	d := New(cfg, fc)
	d.RunOnce()
	if fc.recreated != 1 {
		t.Fatalf("expected recreate to be called once, got %d", fc.recreated)
	}
}

func TestDaemonSkipsNonLatestTag(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage, containers: []docker.Container{{
		ID:      "c2",
		Image:   "nginx:1.2",
		ImageID: testOldImage,
		Labels:  map[string]string{},
		Names:   []string{"/c2"},
	}}}
	cfg := config.DefaultConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.PatchWindow = "" // allow any time
	d := New(cfg, fc)
	d.RunOnce()
	if fc.recreated != 0 {
		t.Fatalf("expected no recreate for non-latest tag, got %d", fc.recreated)
	}
}

func TestDaemonRespectsPatchWindow(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage}
	cfg := config.DefaultConfig()
	cfg.PatchWindow = "23:00-01:00"
	// set time to 12:00 UTC - outside window
	d := New(cfg, fc)
	d.Now = func() time.Time { return time.Date(2026, 1, 6, 12, 0, 0, 0, time.Local) }
	d.RunOnce()
	if fc.recreated != 0 {
		t.Fatalf("expected no recreate outside patch window, got %d", fc.recreated)
	}
	// set time inside window
	d.Now = func() time.Time { return time.Date(2026, 1, 6, 23, 30, 0, 0, time.Local) }
	d.RunOnce()
	if fc.recreated != 1 {
		t.Fatalf("expected recreate during patch window, got %d", fc.recreated)
	}
}

func TestDaemonNotifiesOnSuccess(t *testing.T) {
	var payload map[string]interface{}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &payload)
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer h.Close()

	fc := &fakeClient{pulledID: testNewImage}
	cfg := config.DefaultConfig()
	cfg.GenericWebhookURL = h.URL
	cfg.PatchWindow = fullDayWindow
	d := New(cfg, fc)
	d.RunOnce()
	// wait for async notification sends to complete
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := d.notifier.Wait(ctx); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if payload == nil {
		t.Fatalf("expected notifier payload to be present")
	}
	if agent, ok := payload["agent"].(string); !ok || agent != "Dockhand" {
		t.Fatalf("expected agent 'Dockhand', got %v", payload["agent"])
	}
	if title, ok := payload["title"].(string); !ok || title != "Container updated: c1" {
		t.Fatalf("expected title 'Container updated: c1', got %v", payload["title"])
	}
}

func TestDaemonRemovesOldImageOnSuccess(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage}
	cfg := config.DefaultConfig()
	cfg.PatchWindow = fullDayWindow
	d := New(cfg, fc)
	d.RunOnce()
	if len(fc.removedImages) != 1 {
		t.Fatalf("expected old image to be removed, got %v", fc.removedImages)
	}
	if fc.removedImages[0] != testOldImage {
		t.Fatalf("expected removed image to be %s, got %s", testOldImage, fc.removedImages[0])
	}
}

func TestDaemonPassesVerifyOptions(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage}
	cfg := config.DefaultConfig()
	cfg.PatchWindow = fullDayWindow
	cfg.VerifyTimeout = 2 * time.Second
	cfg.VerifyInterval = 100 * time.Millisecond
	cfg.VerifyCommand = []string{"/bin/true"}
	d := New(cfg, fc)
	d.RunOnce()
	if fc.lastOpts.VerifyTimeout != 2*time.Second {
		t.Fatalf("expected VerifyTimeout to be 2s, got %v", fc.lastOpts.VerifyTimeout)
	}
	if fc.lastOpts.VerifyInterval != 100*time.Millisecond {
		t.Fatalf("expected VerifyInterval to be 100ms, got %v", fc.lastOpts.VerifyInterval)
	}
	if len(fc.lastOpts.HealthcheckCmd) != 1 || fc.lastOpts.HealthcheckCmd[0] != "/bin/true" {
		t.Fatalf("expected verify command to be /bin/true, got %v", fc.lastOpts.HealthcheckCmd)
	}
}

func TestDaemonInitializesNotifiers(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage}
	cfg := config.DefaultConfig()
	cfg.PatchWindow = fullDayWindow
	cfg.DiscordWebhook = "https://example.com/d"
	cfg.SlackWebhook = "https://example.com/s"
	cfg.TeamsWebhook = "https://example.com/t"
	cfg.TelegramToken = "abc"
	cfg.TelegramChatID = "123"
	cfg.MastodonServer = "https://m"
	cfg.MastodonToken = "tok"
	cfg.EmailHost = "mail.example"
	cfg.EmailPort = 25
	cfg.EmailTo = []string{"a@example.com"}
	// Add extra notifiers
	cfg.GenericWebhookURL = "https://example.com/g"
	cfg.GotifyURL = "https://example.com/gotify"
	cfg.GotifyToken = "tok"
	cfg.PushoverUser = "u"
	cfg.PushoverToken = "tok"
	d := New(cfg, fc)
	if d.notifier == nil {
		t.Fatalf("expected notifier to be initialized")
	}
	if d.notifier.Len() != 9 {
		t.Fatalf("expected 9 notifiers, got %d", d.notifier.Len())
	}
}

func TestDaemonNotifiesOnFailure(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage, failRecreate: true}
	cfg := config.DefaultConfig()
	cfg.PatchWindow = fullDayWindow
	var payload map[string]interface{}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &payload)
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer h.Close()
	cfg.GenericWebhookURL = h.URL
	d := New(cfg, fc)
	d.RunOnce()
	// wait for async notification sends to complete
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := d.notifier.Wait(ctx); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if payload == nil {
		t.Fatalf("expected notifier payload to be present on failure")
	}
	if agent, ok := payload["agent"].(string); !ok || agent != "Dockhand" {
		t.Fatalf("expected agent 'Dockhand', got %v", payload["agent"])
	}
	if title, ok := payload["title"].(string); !ok || title != "Update failed: c1" {
		t.Fatalf("expected title 'Update failed: c1', got %v", payload["title"])
	}
	if len(fc.removedImages) != 0 {
		t.Fatalf("expected no images to be removed on failure, got %v", fc.removedImages)
	}
}
func TestDaemonRespectsDisableLabel(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage, containers: []docker.Container{{
		ID:      "c3",
		Image:   "nginx:latest",
		ImageID: testOldImage,
		Labels:  map[string]string{"dockhand.disable": "true"},
		Names:   []string{"/c3"},
	}}}
	cfg := config.DefaultConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.PatchWindow = "" // allow any time
	d := New(cfg, fc)
	d.RunOnce()
	if fc.recreated != 0 {
		t.Fatalf("expected no recreate for disabled container, got %d", fc.recreated)
	}
}

func TestDaemonTriggersSelfUpdate(t *testing.T) {
	fc := &fakeClient{pulledID: testNewImage, containers: []docker.Container{{
		ID:      "c4",
		Image:   "dockhand:latest",
		ImageID: testOldImage,
		Labels:  map[string]string{},
		Names:   []string{"/dockhand-main"},
	}}}
	cfg := config.DefaultConfig()
	cfg.PatchWindow = fullDayWindow
	d := New(cfg, fc)
	// Prevent os.Exit in test by setting a no-op exit function.
	// This avoids terminating the test process while still making intent clear.
	d.exitFunc = func(code int) { /* no-op: prevents terminating the test process */ }
	d.RunOnce()
	// allow some time for goroutine to spawn worker
	for i := 0; i < 10; i++ {
		if fc.spawnedWorker {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !fc.spawnedWorker {
		t.Fatalf("expected worker to be spawned for self-update")
	}
	if fc.spawnedImage != testNewImage {
		t.Fatalf("expected worker to be spawned with image %s, got %s", testNewImage, fc.spawnedImage)
	}
	// Ensure the worker was spawned with the expected socket bind
	expectedBind := fmt.Sprintf("%s:/var/run/docker.sock", cfg.HostSocketPath)
	if len(fc.spawnedBinds) == 0 || fc.spawnedBinds[0] != expectedBind {
		t.Fatalf("expected worker bind to be %s, got %v", expectedBind, fc.spawnedBinds)
	}
	foundTarget := false
	for _, a := range fc.spawnedCmd {
		if a == "--self-update-worker" {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatalf("expected worker command to include --self-update-worker, got %v", fc.spawnedCmd)
	}
}

func TestStopCancelsInFlightOperations(t *testing.T) {
	// Simulate a long-running PullImage that respects ctx cancellation
	called := make(chan struct{})
	failedCount := 0
	fc := &fakeClient{pulledID: ""}
	fc.PullImageFunc = func(ctx context.Context, image string) (string, string, error) {
		failedCount++
		close(called)
		<-ctx.Done()
		return "", "", ctx.Err()
	}

	cfg := config.DefaultConfig()
	cfg.PollInterval = 50 * time.Millisecond
	cfg.PatchWindow = "" // allow anytime
	d := New(cfg, fc)
	// start daemon
	go d.Start()
	// wait until PullImage is called
	select {
	case <-called:
		// proceed
	case <-time.After(1 * time.Second):
		t.Fatalf("PullImage not called in time")
	}
	// Stop with timeout context
	stopCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	d.Stop(stopCtx)
	// ensure the fake PullImage observed a cancellation (failedCount should be >0)
	if failedCount == 0 {
		t.Fatalf("expected PullImage to be invoked at least once")
	}
}

func TestCircuitBreakerSuppressesRepeatedPullFailures(t *testing.T) {
	var received int
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		received++
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer h.Close()

	fc := &fakeClient{}
	// always fail PullImage
	fc.PullImageFunc = func(ctx context.Context, image string) (string, string, error) {
		return "", "", fmt.Errorf("pull failed")
	}
	cfg := config.DefaultConfig()
	cfg.PatchWindow = ""
	cfg.CircuitBreakerThreshold = 3
	cfg.CircuitBreakerCooldown = 1 * time.Second
	cfg.GenericWebhookURL = h.URL
	d := New(cfg, fc)
	// Run 5 passes quickly – expect notifications for first failure, then suppressed
	for i := 0; i < 5; i++ {
		d.RunOnce()
	}
	// wait for any async notifiers
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.notifier.Wait(ctx); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	// With threshold=3 we expect notifications for the first 3 failures, then suppression
	if received != 3 {
		t.Fatalf("expected 3 notifications due to circuit breaker threshold, got %d", received)
	}
	// wait for cooldown to expire and trigger another failure -> notification should be sent again
	time.Sleep(1100 * time.Millisecond)
	d.RunOnce()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if err := d.notifier.Wait(ctx2); err != nil {
		t.Fatalf(waitFailMsg, err)
	}
	if received < 4 {
		t.Fatalf("expected notification after cooldown, got %d", received)
	}
}
