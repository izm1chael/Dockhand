package metrics

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dockhand/dockhand/internal/logging"
)

// StartInfluxPusher starts a background loop to push metrics to InfluxDB
func StartInfluxPusher(ctx context.Context, url, token, org, bucket string, interval time.Duration) {
	if url == "" || bucket == "" {
		return
	}
	logging.Get().Info().Str("url", url).Dur("interval", interval).Msg("starting influxdb pusher")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	client := &http.Client{Timeout: 5 * time.Second}
	writeURL := fmt.Sprintf("%s/api/v2/write?org=%s&bucket=%s&precision=s", strings.TrimRight(url, "/"), org, bucket)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pushToInflux(client, writeURL, token)
		}
	}
}

func pushToInflux(client *http.Client, url, token string) {
	s := GetSnapshot()

	// Influx Line Protocol:
	// measurement,tag=value field=value timestamp
	// Example: dockhand,host=server updates=10i,failures=0i 1678888888

	lines := fmt.Sprintf(
		"dockhand updates=%di,updates_failed=%di,rollbacks=%di,cleanup_failed=%di,last_run=%di %d",
		s.Updates, s.UpdatesFailed, s.Rollbacks, s.CleanupFailed, s.LastRun, time.Now().Unix(),
	)

	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte(lines)))
	if err != nil {
		logging.Get().Error().Err(err).Msg("influxdb request creation failed")
		return
	}

	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		logging.Get().Error().Err(err).Msg("influxdb push failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		logging.Get().Warn().Int("status", resp.StatusCode).Msg("influxdb rejected metrics")
	}
}
