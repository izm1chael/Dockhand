package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"testing"
	"time"
)

type fakeService struct {
	name  string
	calls []string
	fail  bool
}

func (f *fakeService) Send(ctx context.Context, title, message string) error {
	f.calls = append(f.calls, title+"|"+message)
	if f.fail {
		return errors.New("fail")
	}
	return nil
}

func (f *fakeService) Name() string { return f.name }

func TestMultiNotifierSend(t *testing.T) {
	m := NewMultiNotifier()
	s1 := &fakeService{name: "s1"}
	s2 := &fakeService{name: "s2", fail: true}
	m.Add(s1)
	m.Add(s2)
	m.Send(context.Background(), "title", "msg")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if len(s1.calls) != 1 {
		t.Fatalf("expected s1 to be called once, got %v", s1.calls)
	}
	if len(s2.calls) != notifierMaxRetries {
		t.Fatalf("expected s2 to be retried %d times, got %v", notifierMaxRetries, s2.calls)
	}
}

const (
	invalidPayloadMsg    = "invalid payload: %v"
	unexpectedPayloadMsg = "unexpected payload: %v"
)

func TestGenericSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		if payload["title"] == "" || payload["message"] == "" || payload["agent"] != "Dockhand" {
			t.Fatalf(unexpectedPayloadMsg, payload)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	g := &Generic{WebhookURL: server.URL}
	if err := g.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("generic send failed: %v", err)
	}
}

func TestGotifySend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message" {
			t.Fatalf("expected /message, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Gotify-Key") != "tok" {
			t.Fatalf("missing token header")
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		if payload["title"] == "" || payload["message"] == "" {
			t.Fatalf(unexpectedPayloadMsg, payload)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	g := &Gotify{ServerURL: server.URL, Token: "tok"}
	if err := g.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("gotify send failed: %v", err)
	}
}

func TestPushoverSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		if payload["token"] == "" || payload["user"] == "" {
			t.Fatalf(unexpectedPayloadMsg, payload)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	p := &Pushover{UserKey: "u", APIToken: "tok"}
	// override URL by pointing to server
	old := pushoverAPIURL
	pushoverAPIURL = server.URL
	defer func() { pushoverAPIURL = old }()
	if err := p.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("pushover send failed: %v", err)
	}
}

func TestDiscordPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		embeds, ok := payload["embeds"].([]interface{})
		if !ok || len(embeds) == 0 {
			t.Fatalf("expected embeds array in payload: %v", payload)
		}
		first := embeds[0].(map[string]interface{})
		if first["title"] != "T" || first["description"] != "M" {
			t.Fatalf("unexpected embed content: %v", first)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	d := &Discord{WebhookURL: server.URL}
	if err := d.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("discord send failed: %v", err)
	}
}

func TestSlackPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		if payload["text"] != "*T*\nM" {
			t.Fatalf(unexpectedPayloadMsg, payload)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	s := &Slack{WebhookURL: server.URL}
	if err := s.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("slack send failed: %v", err)
	}
}

func TestTeamsPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		if payload["@type"] != "MessageCard" {
			t.Fatalf("unexpected type: %v", payload)
		}
		sections, ok := payload["sections"].([]interface{})
		if !ok || len(sections) == 0 {
			t.Fatalf(unexpectedPayloadMsg, payload)
		}
		first := sections[0].(map[string]interface{})
		if first["activityTitle"] != "T" || first["activityText"] != "M" {
			t.Fatalf("unexpected section content: %v", first)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	svc := &Teams{WebhookURL: server.URL}
	if err := svc.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("teams send failed: %v", err)
	}
}

func TestTelegramPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		if payload["chat_id"] != "123" || payload["parse_mode"] != "HTML" {
			t.Fatalf(unexpectedPayloadMsg, payload)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	old := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = old }()

	g := &Telegram{BotToken: "tok", ChatID: "123"}
	if err := g.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("telegram send failed: %v", err)
	}
}

func TestMastodonPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("missing auth header")
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf(invalidPayloadMsg, err)
		}
		if payload["status"] == "" {
			t.Fatalf(unexpectedPayloadMsg, payload)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	m := &Mastodon{ServerURL: server.URL, AccessToken: "tok"}
	if err := m.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("mastodon send failed: %v", err)
	}
}

func TestEmailSend(t *testing.T) {
	var sentAddr string
	var sentFrom string
	var sentTo []string
	old := sendMailHook
	sendMailHook = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		sentAddr = addr
		sentFrom = from
		sentTo = to
		return nil
	}
	defer func() { sendMailHook = old }()

	e := &Email{Host: "mail.test", Port: 25, User: "u", Pass: "p", To: []string{"a@b"}}
	if err := e.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("email send failed: %v", err)
	}
	if sentAddr != "mail.test:25" || sentFrom != "u" || len(sentTo) != 1 {
		t.Fatalf("unexpected send args: %v %v %v", sentAddr, sentFrom, sentTo)
	}
}
