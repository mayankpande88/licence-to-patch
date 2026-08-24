package brief

import (
	"strings"
	"testing"

	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
	"github.com/mayankpande88/licence-to-patch/internal/verdict"
)

func TestExplain_APIVersionChange(t *testing.T) {
	rep := depdiff.Report{
		Module:      "example.com/armmonitor",
		FromVersion: "v0.12.0",
		ToVersion:   "v0.13.0",
		APIVersionChanges: []depdiff.APIVersionChange{
			{File: "metricalerts_client.go", From: "2024-03-01-preview", To: "2026-01-01"},
		},
		FilesChanged: 29,
	}
	md := Explain(rep, verdict.Classify(rep))

	for _, want := range []string{"🛑", "HOLD", "metricalerts_client.go", "2024-03-01-preview", "2026-01-01",
		"What changed", "Why it matters", "Recommendation", "runtime contract change"} {
		if !strings.Contains(md, want) {
			t.Errorf("explanation missing %q\n---\n%s", want, md)
		}
	}
}

func TestExplain_RemovedSymbols(t *testing.T) {
	rep := depdiff.Report{Module: "example.com/x", RemovedSymbols: []string{"OldClient", "LegacyDo"}}
	md := Explain(rep, verdict.Classify(rep))
	if !strings.Contains(md, "⚠️") || !strings.Contains(md, "CAUTION") || !strings.Contains(md, "OldClient") {
		t.Errorf("unexpected removed-symbols explanation:\n%s", md)
	}
	if !strings.Contains(md, "fail to compile") {
		t.Errorf("should explain the compile-break consequence:\n%s", md)
	}
}

func TestExplain_Clean(t *testing.T) {
	rep := depdiff.Report{Module: "example.com/x", FromVersion: "v4.2.0", ToVersion: "v4.3.0", FilesChanged: 3}
	md := Explain(rep, verdict.Classify(rep))
	if !strings.Contains(md, "✅") || !strings.Contains(md, "ACCEPT") || !strings.Contains(md, "safe") {
		t.Errorf("unexpected clean explanation:\n%s", md)
	}
}

func TestCode_Truncates(t *testing.T) {
	got := code([]string{"A", "B", "C", "D", "E", "F"})
	if !strings.Contains(got, "…") || !strings.Contains(got, "`A`") {
		t.Fatalf("unexpected code(): %q", got)
	}
}
