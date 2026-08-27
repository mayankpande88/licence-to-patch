package apidiff

import (
	"os"
	"path/filepath"
	"testing"
)

// write lays out a single-file module source tree in a temp dir.
func write(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestChanged(t *testing.T) {
	from := write(t, `package client

const (
	apiVersion   = "2024-03-01-preview"
	DefaultRetry = 3
	Endpoint     = "https://mgmt.example.com"
	Unchanged    = "same"
)

const iotaOne = iota // computed, must be ignored
`)
	to := write(t, `package client

const (
	apiVersion   = "2026-01-01"
	DefaultRetry = 5
	Endpoint     = "https://mgmt.example.com"
	Unchanged    = "same"
)

const iotaOne = iota
`)

	got := Changed(from, to)

	byName := map[string]ConstChange{}
	for _, c := range got {
		byName[c.Name] = c
	}

	// api-version const: value changed, classified as api-version.
	av, ok := byName["apiVersion"]
	if !ok {
		t.Fatalf("expected apiVersion change, got %+v", got)
	}
	if av.From != "2024-03-01-preview" || av.To != "2026-01-01" || av.Kind != "api-version" {
		t.Errorf("apiVersion change wrong: %+v", av)
	}

	// A non-date constant (a default retry count) must be caught too — this is
	// the generalization beyond api-versions.
	retry, ok := byName["DefaultRetry"]
	if !ok {
		t.Fatalf("expected DefaultRetry change, got %+v", got)
	}
	if retry.From != "3" || retry.To != "5" || retry.Kind != "constant" {
		t.Errorf("DefaultRetry change wrong: %+v", retry)
	}

	// Unchanged and equal-valued constants must not be reported.
	if _, ok := byName["Unchanged"]; ok {
		t.Errorf("Unchanged should not be reported")
	}
	if _, ok := byName["Endpoint"]; ok {
		t.Errorf("Endpoint (equal value) should not be reported")
	}

	if len(got) != 2 {
		t.Errorf("expected exactly 2 changes (apiVersion, DefaultRetry), got %d: %+v", len(got), got)
	}
}

// TestNoFalsePositiveOnComputed ensures iota/computed const runs never produce
// spurious changes even when their positional index shifts.
func TestNoFalsePositiveOnComputed(t *testing.T) {
	from := write(t, `package p
const ( A = iota; B; C )
`)
	to := write(t, `package p
const ( A = iota; B; C; D )
`)
	if got := Changed(from, to); len(got) != 0 {
		t.Errorf("computed iota consts should yield no changes, got %+v", got)
	}
}
