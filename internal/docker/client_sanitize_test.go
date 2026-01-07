package docker

import "testing"

func TestSanitizeNamePreserveCaseWhenDisabled(t *testing.T) {
	s, _ := NewClientWithAuthWithSanitize("", "", false)
	sdk := s.(*sdkClient)
	if got := sdk.sanitizeName("Postgres!"); got != "Postgres" {
		t.Fatalf("expected 'Postgres', got %q", got)
	}
}

func TestSanitizeNameLowercaseWhenEnabled(t *testing.T) {
	s, _ := NewClientWithAuthWithSanitize("", "", true)
	sdk := s.(*sdkClient)
	if got := sdk.sanitizeName("Postgres!"); got != "postgres" {
		t.Fatalf("expected 'postgres', got %q", got)
	}
}
