package main

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func reqWith(pullNum any) mcp.CallToolRequest {
	var r mcp.CallToolRequest
	r.Params.Arguments = map[string]any{"pull_number": pullNum}
	return r
}

func TestPullNumber(t *testing.T) {
	// JSON numbers decode to float64.
	if n, err := pullNumber(reqWith(float64(7))); err != nil || n != 7 {
		t.Fatalf("whole number: got (%d, %v), want (7, nil)", n, err)
	}
	// The truncation trap Qodo flagged: 7.9 must be rejected, not silently -> 7.
	if _, err := pullNumber(reqWith(float64(7.9))); err == nil {
		t.Fatal("non-integral pull number must be rejected")
	}
	if _, err := pullNumber(reqWith(float64(-3))); err == nil {
		t.Fatal("non-positive pull number must be rejected")
	}
	if _, err := pullNumber(reqWith("abc")); err == nil {
		t.Fatal("non-numeric pull number must be rejected")
	}
	var missing mcp.CallToolRequest
	missing.Params.Arguments = map[string]any{}
	if _, err := pullNumber(missing); err == nil {
		t.Fatal("missing pull number must be rejected")
	}
}
