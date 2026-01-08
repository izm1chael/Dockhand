// Package metrics provides counters, Prometheus collectors, and HTTP
// handlers for exporting Dockhand runtime metrics.
package metrics

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// 1. Internal State (Source of Truth)
var (
	updates           int64
	updatesFailed     int64
	rollbacks         int64
	cleanupFailed     int64
	patchWindowSkips  int64
	imagePullsSuccess int64
	imagePullsFailure int64
	lastRun           int64
)

const counterInc int64 = 1

// 2. Prometheus Collectors
var (
	promUpdates = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "dockhand_updates_total",
			Help: "Total successful updates",
		},
	)
	promFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "dockhand_updates_failed_total",
			Help: "Total failed update attempts",
		},
	)
	promRollbacks = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "dockhand_rollbacks_total",
			Help: "Total rollbacks performed",
		},
	)
	promCleanup = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "dockhand_cleanup_failed_total",
			Help: "Total failed image cleanup operations",
		},
	)
	promPatchWindowSkips = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "dockhand_patch_window_skips_total",
			Help: "Total reconciliation passes skipped due to patch window",
		},
	)
	promImagePulls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dockhand_image_pulls_total",
			Help: "Total image pull attempts",
		},
		[]string{"status"},
	)
	promUpdateDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name: "dockhand_update_duration_seconds",
			Help: "Duration of container update operations",
			Buckets: []float64{
				0.5,
				1,
				2,
				5,
				10,
				30,
				60,
				120,
				300,
			},
		},
	)
	promLastRun = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dockhand_last_run_timestamp_seconds",
			Help: "Unix timestamp of last run",
		},
	)
)

func init() {
	prometheus.MustRegister(
		promUpdates,
		promFailed,
		promRollbacks,
		promCleanup,
		promPatchWindowSkips,
		promImagePulls,
		promUpdateDuration,
		promLastRun,
	)
}

// 3. Public API (Updates both Atomic and Prometheus)

// IncUpdate increments the number of successful updates.
func IncUpdate() {
	atomic.AddInt64(&updates, counterInc)
	promUpdates.Inc()
}

// IncUpdateFailed increments the counter for failed update attempts.
func IncUpdateFailed() {
	atomic.AddInt64(&updatesFailed, counterInc)
	promFailed.Inc()
}

// IncRollback increments the counter for performed rollbacks.
func IncRollback() {
	atomic.AddInt64(&rollbacks, counterInc)
	promRollbacks.Inc()
}

// IncCleanupFailed increments the counter for failed cleanup operations.
func IncCleanupFailed() {
	atomic.AddInt64(&cleanupFailed, counterInc)
	promCleanup.Inc()
}

// IncPatchWindowSkip increments the counter for reconciliation passes
// skipped due to patch window restrictions.
func IncPatchWindowSkip() {
	atomic.AddInt64(&patchWindowSkips, counterInc)
	promPatchWindowSkips.Inc()
}

// IncImagePullSuccess increments the counter for successful image pulls.
func IncImagePullSuccess() {
	atomic.AddInt64(&imagePullsSuccess, counterInc)
	promImagePulls.WithLabelValues("success").Inc()
}

// IncImagePullFailure increments the counter for failed image pulls.
func IncImagePullFailure() {
	atomic.AddInt64(&imagePullsFailure, counterInc)
	promImagePulls.WithLabelValues("failure").Inc()
}

// ObserveUpdateDuration records the duration (in seconds) of an update
// operation in the Prometheus histogram.
func ObserveUpdateDuration(seconds float64) {
	promUpdateDuration.Observe(seconds)
}

// SetLastRun stores the provided time as the last run timestamp and
// updates the corresponding Prometheus gauge.
func SetLastRun(t time.Time) {
	atomic.StoreInt64(&lastRun, t.Unix())
	promLastRun.Set(float64(t.Unix()))
}

// 4. JSON Snapshot Struct (For Zabbix/API)

// StatsSnapshot is a snapshot of metrics for JSON encoding.
type StatsSnapshot struct {
	Updates           int64  `json:"updates"`
	UpdatesFailed     int64  `json:"updates_failed"`
	Rollbacks         int64  `json:"rollbacks"`
	CleanupFailed     int64  `json:"cleanup_failed"`
	PatchWindowSkips  int64  `json:"patch_window_skips"`
	ImagePullsSuccess int64  `json:"image_pulls_success"`
	ImagePullsFailure int64  `json:"image_pulls_failure"`
	LastRun           int64  `json:"last_run_timestamp"`
	LastRunHuman      string `json:"last_run_human"`
}

// GetSnapshot returns a StatsSnapshot with the current values of all
// internal counters and timestamps.
func GetSnapshot() StatsSnapshot {
	ts := atomic.LoadInt64(&lastRun)
	lastRunHuman := time.Unix(ts, 0).Format(time.RFC3339)
	return StatsSnapshot{
		Updates:           atomic.LoadInt64(&updates),
		UpdatesFailed:     atomic.LoadInt64(&updatesFailed),
		Rollbacks:         atomic.LoadInt64(&rollbacks),
		CleanupFailed:     atomic.LoadInt64(&cleanupFailed),
		PatchWindowSkips:  atomic.LoadInt64(&patchWindowSkips),
		ImagePullsSuccess: atomic.LoadInt64(&imagePullsSuccess),
		ImagePullsFailure: atomic.LoadInt64(&imagePullsFailure),
		LastRun:           ts,
		LastRunHuman:      lastRunHuman,
	}
}

// 5. Handlers

// PromHandler returns an HTTP handler that exposes Prometheus metrics.
func PromHandler() http.Handler { return promhttp.Handler() }

// JSONHandler returns an HTTP handler that serves the current metrics as
// a JSON-encoded StatsSnapshot.
func JSONHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GetSnapshot())
	})
}
