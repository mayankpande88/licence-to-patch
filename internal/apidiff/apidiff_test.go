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

// writeFiles lays out a multi-file module source tree in a temp dir.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestBuildVariantsNotCollided (Qodo #2): the same constant name declared in two
// mutually-exclusive build variants must not overwrite each other, so a change
// in the non-lexically-first variant file is still detected.
func TestBuildVariantsNotCollided(t *testing.T) {
	from := writeFiles(t, map[string]string{
		"host_linux.go":   "package p\nconst Host = \"linux-a\"\n",
		"host_windows.go": "package p\nconst Host = \"win-a\"\n",
	})
	to := writeFiles(t, map[string]string{
		"host_linux.go":   "package p\nconst Host = \"linux-a\"\n",     // unchanged
		"host_windows.go": "package p\nconst Host = \"win-CHANGED\"\n", // changed variant
	})
	got := Changed(from, to)
	if len(got) != 1 || got[0].From != "win-a" || got[0].To != "win-CHANGED" {
		t.Errorf("windows-variant change should be detected exactly once, got %+v", got)
	}
}

// TestInheritedConstant (Qodo #3): a const with an omitted expression list
// inherits the previous spec's literal; a transition from inherited to explicit
// must be caught.
func TestInheritedConstant(t *testing.T) {
	from := write(t, "package p\nconst (\n\tBase  = \"v1\"\n\tAlias\n)\n") // Alias inherits "v1"
	to := write(t, "package p\nconst (\n\tBase  = \"v1\"\n\tAlias = \"v2\"\n)\n")
	got := Changed(from, to)
	var alias *ConstChange
	for i := range got {
		if got[i].Name == "Alias" {
			alias = &got[i]
		}
	}
	if alias == nil || alias.From != "v1" || alias.To != "v2" {
		t.Errorf("inherited-then-explicit Alias change should be detected v1->v2, got %+v", got)
	}
}

// TestLiteralNormalization (Qodo #5): spelling-only differences in numeric and
// rune literals must not be reported as value changes.
func TestLiteralNormalization(t *testing.T) {
	from := write(t, "package p\nconst (\n\tN = 1\n\tF = 1.0\n\tC = 'A'\n\tNeg = -1\n)\n")
	to := write(t, "package p\nconst (\n\tN = 01\n\tF = 1.00\n\tC = '\\x41'\n\tNeg = -1\n)\n")
	if got := Changed(from, to); len(got) != 0 {
		t.Errorf("semantically-equal literals must yield no changes, got %+v", got)
	}
}

// TestExportedFlag records whether the identifier is exported, so the verdict can
// scope which changed constants it escalates on.
func TestExportedFlag(t *testing.T) {
	from := write(t, "package p\nconst (\n\tPublic = \"a\"\n\tprivate = \"a\"\n)\n")
	to := write(t, "package p\nconst (\n\tPublic = \"b\"\n\tprivate = \"b\"\n)\n")
	got := Changed(from, to)
	byName := map[string]ConstChange{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if c, ok := byName["Public"]; !ok || !c.Exported {
		t.Errorf("Public should be reported as exported, got %+v", byName["Public"])
	}
	if c, ok := byName["private"]; !ok || c.Exported {
		t.Errorf("private should be reported as unexported, got %+v", byName["private"])
	}
}
