package daemon

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dockhand/dockhand/internal/config"
	"github.com/dockhand/dockhand/internal/docker"
	"github.com/dockhand/dockhand/internal/logging"
	"github.com/dockhand/dockhand/internal/metrics"
	"github.com/dockhand/dockhand/internal/notify"
	"github.com/dockhand/dockhand/internal/semver"
	"github.com/dockhand/dockhand/internal/state"
)

// Daemon is the core loop that checks for image updates and applies them
type failureInfo struct {
	count           int
	lastFailureAt   time.Time
	suppressedUntil time.Time
}

type Daemon struct {
	cfg      *config.Config
	hosts    []Host
	resolver *semver.Resolver // FIELD ADDED
	quit     chan struct{}
	wg       sync.WaitGroup   // tracks active reconciliation passes
	Now      func() time.Time // injectable clock for testing
	notifier *notify.MultiNotifier
	exitFunc func(int) // injectable exit function (defaults to os.Exit)
	cancel   func()    // cancel function for active context (set at Start)
	// circuit breaker state for pull failures
	cbMu         sync.Mutex
	pullFailures map[string]*failureInfo
}

// New creates a daemon with an injected docker client
// Host represents a managed Docker endpoint
type Host struct {
	Name   string
	Client docker.Client
}

// New creates a daemon with an injected list of hosts to manage
func New(cfg *config.Config, hosts []Host) *Daemon {
	d := &Daemon{cfg: cfg, hosts: hosts, resolver: semver.NewResolver(), quit: make(chan struct{}), Now: time.Now, exitFunc: os.Exit}

	// Initialize notifiers
	d.initNotifiers()

	// Log config validation warnings
	for _, w := range cfg.Validate() {
		logging.Get().Warn().Str("warning", w).Msg("config validation")
	}

	return d
}

// initNotifiers initializes all configured notifiers for the daemon
func (d *Daemon) initNotifiers() {
	// create notifier container
	d.notifier = notify.NewMultiNotifier()
	cfg := d.cfg
	// Use a compact entries list to reduce cognitive complexity (avoids many inline ifs)
	entries := []struct {
		enabled bool
		add     func()
	}{
		{cfg.DiscordWebhook != "", func() { d.notifier.Add(&notify.Discord{WebhookURL: cfg.DiscordWebhook}) }},
		{cfg.SlackWebhook != "", func() { d.notifier.Add(&notify.Slack{WebhookURL: cfg.SlackWebhook}) }},
		{cfg.TeamsWebhook != "", func() { d.notifier.Add(&notify.Teams{WebhookURL: cfg.TeamsWebhook}) }},
		{cfg.TelegramToken != "" && cfg.TelegramChatID != "", func() { d.notifier.Add(&notify.Telegram{BotToken: cfg.TelegramToken, ChatID: cfg.TelegramChatID}) }},
		{cfg.MastodonServer != "" && cfg.MastodonToken != "", func() {
			d.notifier.Add(&notify.Mastodon{ServerURL: cfg.MastodonServer, AccessToken: cfg.MastodonToken})
		}},
		{cfg.EmailHost != "" && len(cfg.EmailTo) > 0, func() {
			d.notifier.Add(&notify.Email{Host: cfg.EmailHost, Port: cfg.EmailPort, User: cfg.EmailUser, Pass: cfg.EmailPass, To: cfg.EmailTo})
		}},
		{cfg.GenericWebhookURL != "", func() { d.notifier.Add(&notify.Generic{WebhookURL: cfg.GenericWebhookURL}) }},
		{cfg.GotifyURL != "" && cfg.GotifyToken != "", func() { d.notifier.Add(&notify.Gotify{ServerURL: cfg.GotifyURL, Token: cfg.GotifyToken}) }},
		{cfg.PushoverUser != "" && cfg.PushoverToken != "", func() { d.notifier.Add(&notify.Pushover{UserKey: cfg.PushoverUser, APIToken: cfg.PushoverToken}) }},
		{cfg.AppriseURL != "", func() { d.notifier.Add(&notify.Apprise{APIURL: cfg.AppriseURL}) }},
	}
	for _, e := range entries {
		if e.enabled {
			e.add()
		}
	}
}

// recoverStaleOldContainers attempts to find containers left in "-old-" state
// after a crash or interrupted update and attempts to rename them back to the
// original name when no replacement exists.
func (d *Daemon) recoverStaleOldContainers(ctx context.Context) {
	for _, h := range d.hosts {
		list, err := h.Client.ListAllContainers(ctx)
		if err != nil {
			logging.Get().Warn().Err(err).Str("host", h.Name).Msg("failed listing containers for recovery pass")
			continue
		}
		existing := d.buildExistingNames(list)
		entries := d.findStaleEntries(list, existing)
		for _, e := range entries {
			d.recoverStaleEntry(ctx, h.Client, e)
		}
	}
}

// buildExistingNames returns a set of container names found in the provided list
func (d *Daemon) buildExistingNames(list []docker.Container) map[string]struct{} {
	existing := make(map[string]struct{})
	for _, c := range list {
		for _, n := range c.Names {
			trimmed := strings.TrimPrefix(n, "/")
			existing[trimmed] = struct{}{}
		}
	}
	return existing
}

type staleEntry struct {
	c       docker.Container
	trimmed string
	orig    string
}

// findStaleEntries returns containers whose name matches the '-old-<id>' pattern, whose original name is missing,
// and which are known rename records previously performed by Dockhand.
func (d *Daemon) isRecordedStale(trimmed, orig string) bool {
	// Only consider entries that we recorded earlier during a rename
	rec, found, err := state.GetRenameRecordByTmpName(trimmed)
	if err != nil {
		logging.Get().Warn().Err(err).Str("tmp_name", trimmed).Msg("failed reading state file when looking for stale entries")
		return false
	}
	if !found {
		// not our rename; skip
		return false
	}
	if rec.OrigName != orig {
		// mismatch: skip to be safe
		return false
	}
	return true
}

func (d *Daemon) findStaleEntries(list []docker.Container, existing map[string]struct{}) []staleEntry {
	re := regexp.MustCompile(`(?i)^(.+)-old-[0-9A-Za-z]+$`)
	out := make([]staleEntry, 0)
	for _, c := range list {
		for _, n := range c.Names {
			trimmed := strings.TrimPrefix(n, "/")
			if m := re.FindStringSubmatch(trimmed); m != nil {
				orig := m[1]
				if _, ok := existing[orig]; !ok {
					if d.isRecordedStale(trimmed, orig) {
						out = append(out, staleEntry{c: c, trimmed: trimmed, orig: orig})
					}
				}
			}
		}
	}
	return out
}

// recoverStaleEntry attempts to rename a stale '-old-' container back to its original name
// recoverStaleEntry attempts to rename a stale '-old-' container back to its original name
func (d *Daemon) recoverStaleEntry(ctx context.Context, cli docker.Client, e staleEntry) {
	logging.Get().Warn().Str("found", e.trimmed).Str("target", e.orig).Msg("stale old container detected; attempting recovery")
	if err := cli.RenameContainer(ctx, e.c.ID, e.orig); err != nil {
		logging.Get().Error().Err(err).Str("container", e.c.ID).Msg("failed to rename stale container")
		if d.notifier != nil {
			d.notify(ctx, "warning", "Recovery failed", fmt.Sprintf("failed to rename %s back to %s: %v", e.trimmed, e.orig, err))
		}
		return
	}
	logging.Get().Info().Str("container", e.c.ID).Str("new_name", e.orig).Msg("renamed stale container back to original name")
	// Clean up the state record now that recovery is complete
	if err := state.RemoveRenameRecordByContainerID(e.c.ID); err != nil {
		logging.Get().Warn().Err(err).Str("container", e.c.ID).Msg("failed to clean up state record after recovery")
	}
	if d.notifier != nil {
		d.notify(ctx, "warning", "Recovery performed", fmt.Sprintf("renamed %s back to %s", e.trimmed, e.orig))
	}
}

// Start runs the main polling loop
func (d *Daemon) Start() {
	logging.Get().Info().Dur("interval", d.cfg.PollInterval).Msg("starting dockhand daemon")
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	// Attempt recovery of stale '-old-...' containers on startup
	d.recoverStaleOldContainers(ctx)

	// Run an immediate pass so users don't wait for the first tick
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.once(ctx)
	}()

	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.wg.Add(1)
			d.once(ctx)
			d.wg.Done()
		case <-d.quit:
			logging.Get().Info().Msg("stopping daemon")
			return
		}
	}
}

// once runs one reconciliation pass
func (d *Daemon) once(ctx context.Context) {
	logging.Get().Info().Msg("polling for updates")
	// Check patch window
	if !d.cfg.IsWithinPatchWindow(d.Now()) {
		logging.Get().Info().Str("window", d.cfg.PatchWindow).Msg("outside patch window, skipping update pass")
		metrics.IncPatchWindowSkip()
		return
	}
	// Shared concurrency limit across ALL hosts
	limit := d.cfg.MaxConcurrentUpdates
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	// Global stop signal
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	triggerStop := func() { stopOnce.Do(func() { close(stopCh) }) }

	// Iterate over all hosts
	for _, h := range d.hosts {
		containers, err := h.Client.ListRunningContainers(ctx)
		if err != nil {
			logging.Get().Error().Err(err).Str("host", h.Name).Msg("failed to list containers")
			continue
		}

		for _, c := range containers {
			// Check stop signal
			select {
			case <-stopCh:
				goto WaitAndExit
			case <-ctx.Done():
				goto WaitAndExit
			default:
			}

			wg.Add(1)
			// PASS THE SPECIFIC CLIENT (h.Client) to the goroutine
			go func(cli docker.Client, c docker.Container, hostName string) {
				defer wg.Done()

				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-stopCh:
					return
				case <-ctx.Done():
					return
				}

				if shouldStop := d.handleContainer(ctx, cli, c); shouldStop {
					logging.Get().Info().Str("container", c.ID).Str("host", hostName).Msg("self-update triggered; aborting remaining parallel tasks")
					triggerStop()
				}
			}(h.Client, c, h.Name)
		}
	}

WaitAndExit:
	wg.Wait()
	metrics.SetLastRun(d.Now())
}

// shouldManage returns whether the daemon should attempt to manage the given container
func (d *Daemon) shouldManage(c docker.Container) bool {
	if d.cfg.OptOut {
		// opt-out: manage unless 'dockhand.disable=true'
		if v, ok := c.Labels["dockhand.disable"]; ok && v == "true" {
			return false
		}
	} else {
		// opt-in: only manage when 'dockhand.enable=true'
		if v, ok := c.Labels["dockhand.enable"]; !ok || v != "true" {
			return false
		}
	}
	// only manage containers using tag 'latest' by default if configured
	if d.cfg.ManageLatestOnly && !isLatestTag(c.Image) {
		logging.Get().Info().Strs("names", c.Names).Str("id", c.ID).Msg("skipping container – not using latest tag")
		return false
	}
	return true
}

// handleContainer pulls image for a container and triggers processing if an update is found.
// Returns true when the daemon should stop (e.g., due to self-update initiation).
func (d *Daemon) handleContainer(ctx context.Context, cli docker.Client, c docker.Container) bool {
	if !d.shouldManage(c) {
		return false
	}

	// 1. Resolve target image (policy-aware)
	targetImage, ok := d.resolveTargetImage(ctx, c)
	if !ok {
		return false
	}

	logging.Get().Info().Strs("names", c.Names).Str("id", c.ID).Str("target", targetImage).Msg("considering container")

	// 2. Pull and compute run image (may be pinned)
	pulledID, runImage, err := d.pullAndPin(ctx, cli, targetImage, c)
	if err != nil {
		return false
	}

	// 3. Decide whether to process/update
	return d.decideAndProcess(ctx, cli, c, targetImage, pulledID, runImage)
}

// resolveTargetImage applies SemVer policy (if present) and returns the
// resolved target image. The returned boolean indicates whether processing
// should continue (false = skip this pass).
func (d *Daemon) resolveTargetImage(ctx context.Context, c docker.Container) (string, bool) {
	targetImage := c.Image
	if policy, ok := c.Labels["dockhand.policy"]; ok && policy != "" {
		logging.Get().Debug().Str("container", c.ID).Str("policy", policy).Msg("resolving semver policy")
		resolved, err := d.resolver.Resolve(ctx, c.Image, policy)
		if err != nil {
			logging.Get().Error().Err(err).Str("container", c.ID).Msg("failed to resolve semver policy")
			// Skip this pass on resolution failure
			return "", false
		}
		if resolved != c.Image {
			logging.Get().Info().Str("container", c.ID).Str("current", c.Image).Str("target", resolved).Msg("semver policy resolved new tag")
			targetImage = resolved
		}
	}
	return targetImage, true
}

// pullAndPin pulls the target image and returns the pulled image ID and the
// actual runImage (which may be the repo digest if pinning is enabled).
// On error it logs, notifies if appropriate, increments metrics, and
// returns an error.
func (d *Daemon) pullAndPin(ctx context.Context, cli docker.Client, targetImage string, c docker.Container) (string, string, error) {
	pulledID, repoDigest, err := cli.PullImage(ctx, targetImage)
	if err != nil {
		logging.Get().Error().Err(err).Str("image", targetImage).Str("container", c.ID).Msg("failed pulling image")
		metrics.IncImagePullFailure()
		if d.shouldNotifyPullFailure(targetImage) {
			d.notify(ctx, "failure", fmt.Sprintf("Pull failed: %s", c.ID), err.Error())
		}
		metrics.IncUpdateFailed()
		return "", "", err
	}

	d.clearPullFailure(targetImage)
	metrics.IncImagePullSuccess()

	runImage := targetImage
	shouldPin := d.cfg.PinDigests
	if v, ok := c.Labels["dockhand.pin"]; ok {
		shouldPin = (v == "true")
	}
	if shouldPin && repoDigest != "" {
		runImage = repoDigest
		logging.Get().Debug().Str("container", c.ID).Str("pinned_digest", runImage).Msg("pinning update to digest")
	}
	return pulledID, runImage, nil
}

// decideAndProcess determines if an update should be applied and triggers processing.
// Returns true when daemon should stop (e.g., self-update initiated).
func (d *Daemon) decideAndProcess(ctx context.Context, cli docker.Client, c docker.Container, targetImage, pulledID, runImage string) bool {
	// Update when tag changed or digest changed
	if targetImage != c.Image || (pulledID != "" && pulledID != c.ImageID) {
		logging.Get().Info().Str("container", c.ID).Str("image_new", runImage).Msg("update required")
		if stop := d.processContainer(ctx, cli, c, pulledID, runImage); stop {
			return true
		}
		return false
	}

	// Edge case: apply pin even if content unchanged
	shouldPin := d.cfg.PinDigests
	if v, ok := c.Labels["dockhand.pin"]; ok {
		shouldPin = (v == "true")
	}
	if runImage != c.Image && shouldPin {
		logging.Get().Info().Str("container", c.ID).Str("image_new", runImage).Msg("content unchanged, but applying digest pin")
		if stop := d.processContainer(ctx, cli, c, pulledID, runImage); stop {
			return true
		}
	}
	return false
}

// processContainer handles update logic for a single container. Returns true if the daemon should stop (e.g. when initiating self-update)
func (d *Daemon) processContainer(ctx context.Context, cli docker.Client, c docker.Container, pulledID string, targetImage string) bool {
	// Self-update protection
	if d.handleSelfUpdate(ctx, cli, c, pulledID) {
		return true
	}
	// Dry-run
	if d.handleDryRun(ctx, c, pulledID, targetImage) {
		return false
	}
	// Perform actual update
	d.performUpdate(ctx, cli, c, targetImage)
	return false
}

// handleSelfUpdate returns true when a self-update was initiated and the daemon should stop
func (d *Daemon) handleSelfUpdate(ctx context.Context, cli docker.Client, c docker.Container, pulledID string) bool {
	if !d.isSelfContainer(c) {
		return false
	}
	logging.Get().Info().Msg("self-update detected! initiating automated update sequence.")
	d.notify(ctx, "success", "Self-Update Initiated", "Spawning worker to apply update...")
	// Fire and forget the worker; then stop this daemon to allow replacement
	go d.triggerSelfUpdate(ctx, cli, c.ID, pulledID)
	return true
}

// handleDryRun returns true when dry-run handling was performed
func (d *Daemon) handleDryRun(ctx context.Context, c docker.Container, pulledID string, targetImage string) bool {
	if !d.cfg.DryRun {
		return false
	}
	logging.Get().Info().Str("container", c.ID).Msg("dry-run mode: update available (skipping recreate)")
	d.notify(ctx, "info", fmt.Sprintf("Update available (dry-run): %s", c.ID), fmt.Sprintf("new_image=%s (%s) old_image=%s", targetImage, pulledID, c.ImageID))
	return true
}

// performUpdate performs RecreateContainer and handles success/failure notifications and cleanup
func (d *Daemon) performUpdate(ctx context.Context, cli docker.Client, c docker.Container, targetImage string) {
	oldImage := c.ImageID
	opts := docker.RecreateOptions{VerifyTimeout: d.cfg.VerifyTimeout, VerifyInterval: d.cfg.VerifyInterval, HealthcheckCmd: d.cfg.VerifyCommand, HookTimeout: d.cfg.HookTimeout}
	updateStart := d.Now()
	// Use targetImage here instead of c.Image
	if err := cli.RecreateContainer(ctx, c, targetImage, opts); err != nil {
		logging.Get().Error().Err(err).Str("container", c.ID).Msg("failed to update container")
		d.notify(ctx, "failure", fmt.Sprintf("Update failed: %s", c.ID), err.Error())
		metrics.IncUpdateFailed()
		return
	}
	updateDuration := d.Now().Sub(updateStart).Seconds()
	metrics.ObserveUpdateDuration(updateDuration)
	logging.Get().Info().Str("container", c.ID).Float64("duration_seconds", updateDuration).Msg("updated container")
	metrics.IncUpdate()

	d.notify(ctx, "success", fmt.Sprintf("Container updated: %s", c.ID), fmt.Sprintf("image=%s old_image=%s", targetImage, oldImage))

	if oldImage != "" {
		if err := cli.RemoveImage(ctx, oldImage); err != nil {
			logging.Get().Error().Err(err).Str("old_image", oldImage).Msg("failed to remove old image")
			d.notify(ctx, "warning", fmt.Sprintf("Cleanup failed: %s", c.ID), fmt.Sprintf("old_image=%s err=%v", oldImage, err))
			metrics.IncCleanupFailed()
		}
	}
}

// isSelfContainer returns true if the container appears to be the dockhand daemon itself
func (d *Daemon) isSelfContainer(c docker.Container) bool {
	if strings.Contains(c.Image, "dockhand") {
		return true
	}
	for _, n := range c.Names {
		if strings.Contains(n, "dockhand") {
			return true
		}
	}
	return false
}

// shouldNotifyPullFailure updates circuit breaker state for the given image and
// returns true when a notification should be sent (first failure and after cooldown).
func (d *Daemon) shouldNotifyPullFailure(image string) bool {
	now := d.Now()
	d.cbMu.Lock()
	if d.pullFailures == nil {
		d.pullFailures = make(map[string]*failureInfo)
	}
	fi, ok := d.pullFailures[image]
	if !ok {
		fi = &failureInfo{count: 1, lastFailureAt: now}
		d.pullFailures[image] = fi
		// first failure -> notify
		d.cbMu.Unlock()
		return true
	}
	// if currently suppressed and cooldown hasn't elapsed, keep suppressing
	if fi.suppressedUntil.After(now) {
		fi.count++
		fi.lastFailureAt = now
		d.cbMu.Unlock()
		return false
	}
	// if cooldown elapsed, reset count
	if now.Sub(fi.lastFailureAt) > d.cfg.CircuitBreakerCooldown {
		fi.count = 1
		fi.lastFailureAt = now
		fi.suppressedUntil = time.Time{}
		d.cbMu.Unlock()
		return true
	}
	fi.count++
	fi.lastFailureAt = now
	// if threshold exceeded, suppress for cooldown (allow notifications up to threshold)
	if d.cfg.CircuitBreakerThreshold > 0 && fi.count > d.cfg.CircuitBreakerThreshold {
		fi.suppressedUntil = now.Add(d.cfg.CircuitBreakerCooldown)
		d.cbMu.Unlock()
		return false
	}
	d.cbMu.Unlock()
	return true
}

func (d *Daemon) clearPullFailure(image string) {
	if d.pullFailures == nil {
		return
	}
	d.cbMu.Lock()
	defer d.cbMu.Unlock()
	delete(d.pullFailures, image)
}

// triggerSelfUpdate spawns a worker container (using the provided newImage) to perform
// the self-update, then stops this daemon and exits.
func (d *Daemon) triggerSelfUpdate(ctx context.Context, cli docker.Client, myContainerID, newImage string) {
	logging.Get().Info().Str("target", myContainerID).Str("image", newImage).Msg("spawning self-update worker")
	// use worker name with a timestamp suffix to avoid collisions
	name := fmt.Sprintf("dockhand-updater-%d", time.Now().Unix())
	// Use configured host socket path to support split-horizon Docker socket locations
	binds := []string{fmt.Sprintf("%s:/var/run/docker.sock", d.cfg.HostSocketPath)}
	cmd := []string{"/app/dockhand", "--self-update-worker", myContainerID, "--self-update-image", newImage}
	labels := map[string]string{"dockhand.worker": "true"}
	if _, err := cli.SpawnWorker(ctx, newImage, cmd, name, binds, labels); err != nil {
		logging.Get().Error().Err(err).Msg("failed to spawn update worker")
		return
	}
	logging.Get().Info().Msg("worker spawned; initiating shutdown to allow update")
	d.Stop(context.Background())
	// allow caller to override exit behavior in tests
	d.exitFunc(0)
}

// notify sends a notification if the configured level allows it
// level: "success" | "failure"
func (d *Daemon) notify(ctx context.Context, level, title, message string) {
	configLevel := strings.ToLower(d.cfg.NotificationLevel)
	if configLevel == "none" {
		return
	}
	if configLevel == "failure" && level != "failure" {
		return
	}
	d.notifier.Send(ctx, title, message)
}

// isLatestTag returns true when the image reference does not provide a tag (implicit latest) or explicitly uses 'latest'
func isLatestTag(image string) bool {
	if strings.Contains(image, "@") {
		// image pinned by digest, not 'latest'
		return false
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon == -1 || lastColon < lastSlash {
		// no explicit tag => implicit latest
		return true
	}
	tag := image[lastColon+1:]
	return tag == "latest"
}

// Stop signals the daemon to stop and waits for active operations to complete
func (d *Daemon) Stop(ctx context.Context) {
	// Cancel background context to signal in-flight operations to stop
	if d.cancel != nil {
		d.cancel()
	}
	close(d.quit)

	// Wait for active reconciliation passes to complete
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logging.Get().Info().Msg("all active operations completed")
	case <-ctx.Done():
		logging.Get().Warn().Msg("shutdown timeout exceeded, some operations may be incomplete")
	}

	// Allow some time for pending notifications to finish (best-effort)
	if d.notifier != nil {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.notifier.Wait(notifyCtx); err != nil {
			logging.Get().Warn().Err(err).Msg("timed out waiting for notifiers to finish")
		}
	}
}

// RunOnce runs a single reconciliation pass (public wrapper for tests / CLI)
func (d *Daemon) RunOnce() {
	d.once(context.Background())
}
