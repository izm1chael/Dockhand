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

// --- Apprise (Gateway) ---
type Apprise struct{ APIURL string }

func (a *Apprise) Name() string { return "Apprise" }
func (a *Apprise) Send(ctx context.Context, title, message string) error {
	payload := map[string]string{"title": title, "body": message, "format": "markdown", "type": "info"}
	return postJSON(ctx, a.APIURL, payload)
}

// --- Gotify (Self-Hosted Push) ---
type Gotify struct{ ServerURL, Token string }

func (g *Gotify) Name() string { return "Gotify" }
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

// --- Pushover (Mobile Push) ---
var pushoverAPIURL = "https://api.pushover.net/1/messages.json"

type Pushover struct{ UserKey, APIToken string }

func (p *Pushover) Name() string { return "Pushover" }
func (p *Pushover) Send(ctx context.Context, title, message string) error {
	url := pushoverAPIURL
	payload := map[string]string{"token": p.APIToken, "user": p.UserKey, "title": title, "message": message, "html": "0"}
	return postJSON(ctx, url, payload)
}

// --- Generic Webhook ---
type Generic struct{ WebhookURL string }

func (g *Generic) Name() string { return "GenericWebhook" }
func (g *Generic) Send(ctx context.Context, title, message string) error {
	payload := map[string]string{"title": title, "message": message, "agent": "Dockhand"}
	return postJSON(ctx, g.WebhookURL, payload)
}
