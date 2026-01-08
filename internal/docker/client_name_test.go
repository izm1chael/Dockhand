package docker

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
)

const (
	testOrigTZ     = "orig+01:00"
	testOldIDCN    = "old-id"
	testMaxLen     = 200
	fallbackName   = "container"
	okExitCode     = 0
	verifyTimeout  = 5 * time.Second
	verifyInterval = 100 * time.Millisecond
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]struct {
		expectFallback  bool
		expectLowercase bool
		expectMaxLen    bool
	}{
		"orig":                          {false, true, false},
		testOrigTZ:                      {false, true, false},
		"orig:zone":                     {false, true, false},
		"+:+":                           {true, false, false},
		".hidden":                       {false, true, false},
		"UPPERCASE":                     {false, true, false},
		strings.Repeat("a", testMaxLen): {false, true, true},
	}
	re := regexp.MustCompile(
		`^[a-z0-9][a-z0-9_.-]+$`,
	)

	for input, info := range cases {
		t.Run(input, func(t *testing.T) {
			validateSanitizedOutput(t, input, info, re)
		})
	}
}

func validateSanitizedOutput(t *testing.T, input string, info struct {
	expectFallback  bool
	expectLowercase bool
	expectMaxLen    bool
}, re *regexp.Regexp) {
	t.Helper()
	sdk := &sdkClient{sanitizeNames: true}
	s := sdk.sanitizeName(input)
	if info.expectFallback {
		if s != fallbackName {
			t.Fatalf("expected fallback %q for input %q, got %q", fallbackName, input, s)
		}
		return
	}
	if !re.MatchString(s) {
		t.Fatalf("sanitized name %q does not match safe regex for input %q", s, input)
	}
	if info.expectMaxLen && len(s) > maxNameLen {
		t.Fatalf("sanitized name %q exceeds max length %d", s, maxNameLen)
	}
}

func TestRecreateSanitizesOriginalName(t *testing.T) {
	// Make a fake that reports a bad original name
	fake := &fakeDockerAPI{
		execInspects: map[string]containertypes.ExecInspect{"exec1": {ExitCode: okExitCode, Running: false}},
		inspectNames: map[string]string{testOldIDCN: testOrigTZ},
	}
	s := &sdkClient{cli: fake, sanitizeNames: true}
	ctx := context.Background()
	opts := RecreateOptions{VerifyTimeout: verifyTimeout, VerifyInterval: verifyInterval, HealthcheckCmd: []string{"/bin/true"}}
	err := s.RecreateContainer(ctx, Container{ID: testOldIDCN}, "new-image", opts)
	if err != nil {
		t.Fatalf("expected recreate success, got error: %v", err)
	}
	// Check renamed map for the sanitized tmp name
	sanit := s.sanitizeName(testOrigTZ)
	pattern := fmt.Sprintf("^%s-old-\\d+$", regexp.QuoteMeta(sanit))
	assertRenamedMatches(t, fake, testOldIDCN, pattern)
}

func assertRenamedMatches(t *testing.T, fake *fakeDockerAPI, key, pattern string) {
	ren, ok := fake.renamed[key]
	if !ok {
		t.Fatalf("expected rename to be called, renamed=%v", fake.renamed)
	}
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
