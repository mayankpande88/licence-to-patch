package fix

import (
	"context"
	"strings"
	"testing"
)

func TestRevertBump_RequiresToken(t *testing.T) {
	f := New("")
	if _, err := f.RevertBump(context.Background(), "o", "r", "b", "example.com/m", "v1.0.0"); err == nil {
		t.Fatal("expected error when token is empty")
	}
}

func TestRevertBump_RejectsMalformedInputs(t *testing.T) {
	f := New("tok")
	cases := []struct{ owner, repo, branch, module, version string }{
		{"o", "r", "b", "mod", "v1 && rm -rf /"}, // shell metachars
		{"o;rm", "r", "b", "mod", "v1.0.0"},      // owner injection
		{"o", "r", "b", "", "v1.0.0"},            // empty module
	}
	for _, c := range cases {
		_, err := f.RevertBump(context.Background(), c.owner, c.repo, c.branch, c.module, c.version)
		if err == nil {
			t.Errorf("expected rejection for %+v", c)
		}
	}
}

func TestRedact(t *testing.T) {
	if got := redact("clone https://x-access-token:SECRET@github.com/o/r.git failed", "SECRET"); strings.Contains(got, "SECRET") {
		t.Fatalf("token not redacted: %q", got)
	}
}
