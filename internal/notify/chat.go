package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// --- Slack ---
type Slack struct {
	WebhookURL string
}

func (s *Slack) Name() string { return "Slack" }
func (s *Slack) Send(ctx context.Context, title, message string) error {
	payload := map[string]string{"text": fmt.Sprintf("*%s*\n%s", title, message)}
	return postJSON(ctx, s.WebhookURL, payload)
}

// --- Discord ---
type Discord struct {
	WebhookURL string
}

func (d *Discord) Name() string { return "Discord" }
func (d *Discord) Send(ctx context.Context, title, message string) error {
	payload := map[string]interface{}{
		"username": "Dockhand",
		"embeds":   []map[string]interface{}{{"title": title, "description": message, "color": 3447003, "timestamp": time.Now().Format(time.RFC3339)}},
	}
	return postJSON(ctx, d.WebhookURL, payload)
}

// --- Teams ---
type Teams struct{ WebhookURL string }

func (t *Teams) Name() string { return "Teams" }
func (t *Teams) Send(ctx context.Context, title, message string) error {
	payload := map[string]interface{}{"@type": "MessageCard", "@context": "http://schema.org/extensions", "themeColor": "0076D7", "summary": title, "sections": []map[string]string{{"activityTitle": title, "activityText": message}}}
	return postJSON(ctx, t.WebhookURL, payload)
}

// --- Telegram ---
var telegramAPIBase = "https://api.telegram.org"

type Telegram struct{ BotToken, ChatID string }

func (t *Telegram) Name() string { return "Telegram" }
func (t *Telegram) Send(ctx context.Context, title, message string) error {
	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBase, t.BotToken)
	payload := map[string]string{"chat_id": t.ChatID, "text": fmt.Sprintf("<b>%s</b>\n%s", title, message), "parse_mode": "HTML"}
	return postJSON(ctx, apiURL, payload)
}

// --- Mastodon ---
type Mastodon struct{ ServerURL, AccessToken string }

func (m *Mastodon) Name() string { return "Mastodon" }
func (m *Mastodon) Send(ctx context.Context, title, message string) error {
	endpoint := fmt.Sprintf("%s/api/v1/statuses", strings.TrimRight(m.ServerURL, "/"))
	payload := map[string]string{"status": fmt.Sprintf("%s\n\n%s", title, message), "visibility": "private"}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.AccessToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mastodon api %d", resp.StatusCode)
	}
	return nil
}
