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

// 2. Prometheus Collectors
var (
	promUpdates = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dockhand_updates_total", Help: "Total successful updates",
	})
	promFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dockhand_updates_failed_total", Help: "Total failed update attempts",
	})
	promRollbacks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dockhand_rollbacks_total", Help: "Total rollbacks performed",
	})
	promCleanup = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dockhand_cleanup_failed_total", Help: "Total failed image cleanup operations",
	})
	promPatchWindowSkips = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dockhand_patch_window_skips_total", Help: "Total reconciliation passes skipped due to patch window",
	})
	promImagePulls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dockhand_image_pulls_total", Help: "Total image pull attempts",
	}, []string{"status"})
	promUpdateDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "dockhand_update_duration_seconds", Help: "Duration of container update operations",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	})
	promLastRun = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dockhand_last_run_timestamp_seconds", Help: "Unix timestamp of last run",
	})
)

func init() {
	prometheus.MustRegister(promUpdates, promFailed, promRollbacks, promCleanup, promPatchWindowSkips, promImagePulls, promUpdateDuration, promLastRun)
}

// 3. Public API (Updates both Atomic and Prometheus)
func IncUpdate() {
	atomic.AddInt64(&updates, 1)
	promUpdates.Inc()
}
func IncUpdateFailed() {
	atomic.AddInt64(&updatesFailed, 1)
	promFailed.Inc()
}
func IncRollback() {
	atomic.AddInt64(&rollbacks, 1)
	promRollbacks.Inc()
}
func IncCleanupFailed() {
	atomic.AddInt64(&cleanupFailed, 1)
	promCleanup.Inc()
}
func IncPatchWindowSkip() {
	atomic.AddInt64(&patchWindowSkips, 1)
	promPatchWindowSkips.Inc()
}
func IncImagePullSuccess() {
	atomic.AddInt64(&imagePullsSuccess, 1)
	promImagePulls.WithLabelValues("success").Inc()
}
func IncImagePullFailure() {
	atomic.AddInt64(&imagePullsFailure, 1)
	promImagePulls.WithLabelValues("failure").Inc()
}
func ObserveUpdateDuration(seconds float64) {
	promUpdateDuration.Observe(seconds)
}
func SetLastRun(t time.Time) {
	atomic.StoreInt64(&lastRun, t.Unix())
	promLastRun.Set(float64(t.Unix()))
}

// 4. JSON Snapshot Struct (For Zabbix/API)
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

func GetSnapshot() StatsSnapshot {
	ts := atomic.LoadInt64(&lastRun)
	return StatsSnapshot{
		Updates:           atomic.LoadInt64(&updates),
		UpdatesFailed:     atomic.LoadInt64(&updatesFailed),
		Rollbacks:         atomic.LoadInt64(&rollbacks),
		CleanupFailed:     atomic.LoadInt64(&cleanupFailed),
		PatchWindowSkips:  atomic.LoadInt64(&patchWindowSkips),
		ImagePullsSuccess: atomic.LoadInt64(&imagePullsSuccess),
		ImagePullsFailure: atomic.LoadInt64(&imagePullsFailure),
		LastRun:           ts,
		LastRunHuman:      time.Unix(ts, 0).Format(time.RFC3339),
	}
}

// 5. Handlers
func PromHandler() http.Handler { return promhttp.Handler() }

func JSONHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GetSnapshot())
	})
}
