package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApprisePayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("invalid payload: %v", err)
		}
		if payload["title"] != "T" || payload["body"] != "M" {
			t.Fatalf("unexpected apprise payload: %v", payload)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	a := &Apprise{APIURL: server.URL}
	if err := a.Send(context.Background(), "T", "M"); err != nil {
		t.Fatalf("apprise send failed: %v", err)
	}
}
