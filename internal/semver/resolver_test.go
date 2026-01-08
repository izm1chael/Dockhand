package semver

import (
	"testing"
)

func TestSelectHighestTag_MatchesHighest(t *testing.T) {
    tags := []string{"1.0.0", "1.2.3", "1.1.5", "not-semver", "2.0.0"}
    c, err := parseConstraint("1.x")
    if err != nil {
        t.Fatalf("parseConstraint: %v", err)
    }
    tag, err := selectHighestTag(tags, c)
    if err != nil {
        t.Fatalf("selectHighestTag returned error: %v", err)
    }
    if tag != "1.2.3" {
        t.Fatalf("expected highest matching tag 1.2.3, got %s", tag)
    }
}

func TestSelectHighestTag_NoMatch(t *testing.T) {
    tags := []string{"0.1.0", "0.2.0"}
    c, err := parseConstraint("1.x")
    if err != nil {
        t.Fatalf("parseConstraint: %v", err)
    }
    if _, err := selectHighestTag(tags, c); err == nil {
        t.Fatalf("expected error when no tags match constraint")
    }
}

func TestParseRepo_Valid(t *testing.T) {
    repo, err := parseRepo("example.com/foo/bar:1.2.3")
    if err != nil {
        t.Fatalf("parseRepo failed: %v", err)
    }
    if repo.Name() == "" {
        t.Fatalf("expected non-empty repo name")
    }
}
