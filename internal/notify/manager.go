package notify

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/dockhand/dockhand/internal/logging"
)

// DefaultNotifierCooldown is the default cooldown between notifications to the same service
// Reduced to a small delay to avoid suppressing distinct events (e.g., rapid updates across a stack).
var DefaultNotifierCooldown = 100 * time.Millisecond

// NotifierRetry settings (can be tuned in tests)
var notifierMaxRetries = 3
var notifierBaseBackoff = 100 * time.Millisecond

// notifierBackoffJitter adds up to this random duration to backoff (to avoid thundering herd)
var notifierBackoffJitter = 0 * time.Millisecond

// sleepHook is used in tests to avoid sleeping for real
var sleepHook = time.Sleep

// Service is the interface all notifiers must implement
type Service interface {
	Send(ctx context.Context, title, message string) error
	Name() string
}

// MultiNotifier bundles all active services
type MultiNotifier struct {
	services []Service
	// lastSent tracks last successful send per service name
	lastSent map[string]time.Time
	// cooldown applies to all services by default; can be overridden in tests
	cooldown time.Duration
	// per-provider cooldowns
	providerCooldowns map[string]time.Duration
	mu                sync.Mutex
	wg                sync.WaitGroup
}

// SetProviderCooldown sets a cooldown for a named provider (by Service.Name())
func (m *MultiNotifier) SetProviderCooldown(name string, d time.Duration) {
	if m.providerCooldowns == nil {
		m.providerCooldowns = make(map[string]time.Duration)
	}
	m.providerCooldowns[name] = d
}

// ProviderCooldown returns the cooldown for a given provider or the global default
func (m *MultiNotifier) ProviderCooldown(name string) time.Duration {
	if v, ok := m.providerCooldowns[name]; ok {
		return v
	}
	return m.cooldown
}
func NewMultiNotifier() *MultiNotifier {
	return &MultiNotifier{services: make([]Service, 0), lastSent: make(map[string]time.Time), cooldown: DefaultNotifierCooldown}
}

// Wait waits for pending notification sends to complete or until the provided
// context is cancelled.
func (m *MultiNotifier) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *MultiNotifier) Add(s Service) {
	if s != nil {
		m.services = append(m.services, s)
	}
}

func (m *MultiNotifier) Len() int {
	return len(m.services)
}

// SetCooldown allows tests or callers to adjust global cooldown
func (m *MultiNotifier) SetCooldown(d time.Duration) {
	m.cooldown = d
}

// ResetLastSent resets the last-sent timestamp for a service (tests)
func (m *MultiNotifier) ResetLastSent(name string) {
	delete(m.lastSent, name)
}

// Send sends notifications to all services with per-service retries and a cooldown to avoid spamming.
func (m *MultiNotifier) Send(ctx context.Context, title, message string) {
	now := time.Now()
	for _, s := range m.services {
		name := s.Name()
		m.wg.Add(1)
		go func(svc Service, svcName string) {
			defer m.wg.Done()
			if m.shouldSkipDueToCooldown(svcName, now) {
				logging.Get().Warn().Str("service", svcName).Msg("skipping notification due to cooldown")
				return
			}
			if err := m.sendWithRetries(ctx, svc, title, message, svcName); err != nil {
				logging.Get().Error().Err(err).Str("service", svcName).Msg("all notification retries failed")
			}
		}(s, name)
	}
}

// shouldSkipDueToCooldown returns true when a service should be skipped due to cooldown
func (m *MultiNotifier) shouldSkipDueToCooldown(name string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.lastSent[name]; ok {
		if now.Sub(last) < m.ProviderCooldown(name) {
			return true
		}
	}
	return false
}

// sendWithRetries attempts to send a notification with retries and backoff. Returns last error if any.
func (m *MultiNotifier) sendWithRetries(ctx context.Context, s Service, title, message, name string) error {
	var lastErr error
	for attempt := 1; attempt <= notifierMaxRetries; attempt++ {
		if err := s.Send(ctx, title, message); err != nil {
			lastErr = err
			logging.Get().Warn().Err(err).Str("service", name).Int("attempt", attempt).Msg("notification attempt failed")
			if attempt < notifierMaxRetries {
				// context-aware sleep: allow cancellation via ctx, but use sleepHook to speed tests.
				d := m.backoffDuration(attempt)
				dCh := make(chan struct{})
				go func() {
					sleepHook(d)
					close(dCh)
				}()
				select {
				case <-dCh:
					// slept; continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			continue
		}
		// success
		m.mu.Lock()
		m.lastSent[name] = time.Now()
		m.mu.Unlock()
		logging.Get().Debug().Str("service", name).Msg("notification sent")
		return nil
	}
	return lastErr
}

// backoffDuration returns the computed backoff including optional jitter for the given attempt
func (m *MultiNotifier) backoffDuration(attempt int) time.Duration {
	d := notifierBaseBackoff * time.Duration(1<<uint(attempt-1))
	if notifierBackoffJitter > 0 {
		// Use crypto/rand to generate non-predictable jitter for backoff
		max := big.NewInt(int64(notifierBackoffJitter))
		if n, err := crand.Int(crand.Reader, max); err == nil {
			d += time.Duration(n.Int64())
		}
	}
	return d
}

// postJSON is a shared helper used by providers
func postJSON(ctx context.Context, url string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("api returned status %d", resp.StatusCode)
	}
	return nil
}
