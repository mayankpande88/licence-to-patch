package verdict

import (
	"strings"
	"testing"

	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
)

func TestClassify_ContractChangeIsHold(t *testing.T) {
	rep := depdiff.Report{
		Module:      "example.com/x",
		FromVersion: "v0.12.0",
		ToVersion:   "v0.13.0",
		APIVersionChanges: []depdiff.APIVersionChange{
			{File: "metricalerts_client.go", From: "2024-03-01-preview", To: "2026-01-01"},
		},
	}
	v := Classify(rep)
	if v.Level != Hold {
		t.Fatalf("expected HOLD, got %s", v.Level)
	}
	if len(v.Reasons) == 0 || !strings.Contains(v.Reasons[0], "2026-01-01") {
		t.Fatalf("expected reason to cite the new api-version, got %v", v.Reasons)
	}
}

func TestClassify_RemovedSymbolIsCaution(t *testing.T) {
	rep := depdiff.Report{
		Module:         "example.com/x",
		RemovedSymbols: []string{"OldClient", "LegacyDo"},
	}
	v := Classify(rep)
	if v.Level != Caution {
		t.Fatalf("expected CAUTION, got %s", v.Level)
	}
}

func TestClassify_ContractChangeOutranksRemovedSymbol(t *testing.T) {
	rep := depdiff.Report{
		APIVersionChanges: []depdiff.APIVersionChange{{File: "c.go", From: "2021-01-01", To: "2026-01-01"}},
		RemovedSymbols:    []string{"OldClient"},
	}
	if got := Classify(rep).Level; got != Hold {
		t.Fatalf("contract change must outrank removed symbol; got %s", got)
	}
}

func TestClassify_CleanBumpIsAccept(t *testing.T) {
	v := Classify(depdiff.Report{Module: "example.com/x", FromVersion: "v4.2.0", ToVersion: "v4.3.0"})
	if v.Level != Accept {
		t.Fatalf("expected ACCEPT, got %s", v.Level)
	}
}

func TestPreview_TruncatesLongLists(t *testing.T) {
	got := preview([]string{"A", "B", "C", "D"})
	if !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "A, B, C") {
		t.Fatalf("unexpected preview: %q", got)
	}
}
