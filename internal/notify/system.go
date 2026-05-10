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

// Apprise sends notifications via an Apprise gateway (API-compatible endpoint).
type Apprise struct{ APIURL string }

// Name returns the notifier name.
func (a *Apprise) Name() string { return "Apprise" }

// Send posts a notification to the Apprise gateway.
func (a *Apprise) Send(ctx context.Context, title, message string) error {
	payload := map[string]string{"title": title, "body": message, "format": "markdown", "type": "info"}
	return postJSON(ctx, a.APIURL, payload)
}

// Gotify sends notifications to a self-hosted Gotify push server.
type Gotify struct{ ServerURL, Token string }

// Name returns the notifier name.
func (g *Gotify) Name() string { return "Gotify" }

// Send posts a notification to the Gotify server using the configured token.
func (g *Gotify) Send(ctx context.Context, title, message string) error {
	url := fmt.Sprintf("%s/message", strings.TrimRight(g.ServerURL, "/"))
	payload := map[string]interface{}{"title": title, "message": message, "priority": 5, "extras": map[string]interface{}{"client::display": map[string]string{"contentType": "text/markdown"}}}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", g.Token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gotify returned %d", resp.StatusCode)
	}
	return nil
}

var pushoverAPIURL = "https://api.pushover.net/1/messages.json"

// Pushover sends notifications to mobile devices via the Pushover API.
type Pushover struct{ UserKey, APIToken string }

// Name returns the notifier name.
func (p *Pushover) Name() string { return "Pushover" }

// Send posts a notification to Pushover using the configured user key and API token.
func (p *Pushover) Send(ctx context.Context, title, message string) error {
	url := pushoverAPIURL
	payload := map[string]string{"token": p.APIToken, "user": p.UserKey, "title": title, "message": message, "html": "0"}
	return postJSON(ctx, url, payload)
}

// Generic sends notifications to an arbitrary webhook endpoint.
type Generic struct{ WebhookURL string }

// Name returns the notifier name.
func (g *Generic) Name() string { return "GenericWebhook" }

// Send posts a notification payload to the configured webhook URL.
func (g *Generic) Send(ctx context.Context, title, message string) error {
	payload := map[string]string{"title": title, "message": message, "agent": "Dockhand"}
	return postJSON(ctx, g.WebhookURL, payload)
}
