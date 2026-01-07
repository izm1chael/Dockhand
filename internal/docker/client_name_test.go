package docker

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
)

const (
	testOrigTZ  = "orig+01:00"
	testOldIDCN = "old-id"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]struct {
		expectFallback  bool
		expectLowercase bool
		expectMaxLen    bool
	}{
		"orig":                   {false, true, false},
		testOrigTZ:               {false, true, false},
		"orig:zone":              {false, true, false},
		"+:+":                    {true, false, false},
		".hidden":                {false, true, false},
		"UPPERCASE":              {false, true, false},
		strings.Repeat("a", 200): {false, true, true},
	}
	re := regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]+$`)
	for input, info := range cases {
		sdk := &sdkClient{sanitizeNames: true}
		s := sdk.sanitizeName(input)
		if info.expectFallback {
			if s != "container" {
				t.Fatalf("expected fallback 'container' for input %q, got %q", input, s)
			}
			continue
		}
		if !info.expectLowercase {
			// if we don't expect lowercase, still accept it
		}
		if !re.MatchString(s) {
			t.Fatalf("sanitized name %q does not match safe regex for input %q", s, input)
		}
		if info.expectMaxLen && len(s) > maxNameLen {
			t.Fatalf("sanitized name %q exceeds max length %d", s, maxNameLen)
		}
	}
}

func TestRecreateSanitizesOriginalName(t *testing.T) {
	// Make a fake that reports a bad original name
	fake := &fakeDockerAPI{
		execInspects: map[string]types.ContainerExecInspect{"exec1": {ExitCode: 0, Running: false}},
		inspectNames: map[string]string{testOldIDCN: testOrigTZ},
	}
	s := &sdkClient{cli: fake, sanitizeNames: true}
	ctx := context.Background()
	opts := RecreateOptions{VerifyTimeout: 5 * time.Second, VerifyInterval: 100 * time.Millisecond, HealthcheckCmd: []string{"/bin/true"}}
	err := s.RecreateContainer(ctx, Container{ID: testOldIDCN}, "new-image", opts)
	if err != nil {
		t.Fatalf("expected recreate success, got error: %v", err)
	}
	// Check renamed map for the sanitized tmp name
	ren, ok := fake.renamed[testOldIDCN]
	if !ok {
		t.Fatalf("expected rename to be called, renamed=%v", fake.renamed)
	}
	// sanitized value for testOrigTZ should be something like "orig0100"
	sanit := s.sanitizeName(testOrigTZ)
	pattern := fmt.Sprintf("^%s-old-\\d+$", regexp.QuoteMeta(sanit))
	re2 := regexp.MustCompile(pattern)
	if !re2.MatchString(ren) {
		t.Fatalf("renamed value %q does not match pattern %q", ren, pattern)
	}
}

func FuzzSanitizeName(f *testing.F) {
	seeds := []string{"orig", testOrigTZ, "OrigUPPER", "........", strings.Repeat("a", 200)}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		sdk := &sdkClient{sanitizeNames: true}
		out := sdk.sanitizeName(input)
		if out == "container" {
			return
		}
		re := regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]+$`)
		if !re.MatchString(out) {
			t.Fatalf("bad sanitized output %q from input %q", out, input)
		}
		if len(out) > maxNameLen {
			t.Fatalf("sanitized name too long: %d", len(out))
		}
	})
}
