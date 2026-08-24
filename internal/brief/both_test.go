package brief

import (
	"strings"
	"testing"

	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
	"github.com/mayankpande88/licence-to-patch/internal/verdict"
)

// A bump can both change an api-version and remove a symbol; the explanation
// must render BOTH sections, not just the first.
func TestExplain_RendersEveryApplicableSection(t *testing.T) {
	rep := depdiff.Report{
		Module:            "example.com/x",
		FromVersion:       "v1.0.0",
		ToVersion:         "v2.0.0",
		APIVersionChanges: []depdiff.APIVersionChange{{File: "c.go", From: "2021-01-01", To: "2026-01-01"}},
		RemovedSymbols:    []string{"OldClient"},
	}
	md := Explain(rep, verdict.Classify(rep))
	if !strings.Contains(md, "api-version") {
		t.Errorf("missing api-version section:\n%s", md)
	}
	if !strings.Contains(md, "OldClient") {
		t.Errorf("missing removed-symbols section:\n%s", md)
	}
}
