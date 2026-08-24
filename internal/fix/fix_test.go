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

func TestRevertBump_AcceptsIncompatibleVersion(t *testing.T) {
	// +incompatible is a valid Go module version; it must pass validation and fail
	// later (on the network clone) rather than being rejected up front.
	f := New("tok")
	_, err := f.RevertBump(context.Background(), "o", "r", "b", "example.com/m", "v2.0.0+incompatible")
	if err != nil && strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("+incompatible version must not be rejected by validation: %v", err)
	}
}
