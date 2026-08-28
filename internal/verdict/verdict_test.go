package verdict

import (
	"strings"
	"testing"

	"github.com/mayankpande88/licence-to-patch/internal/apidiff"
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
		Module:           "example.com/x",
		RemovedFunctions: []string{"OldClient", "LegacyDo"},
	}
	v := Classify(rep)
	if v.Level != Caution {
		t.Fatalf("expected CAUTION, got %s", v.Level)
	}
}

func TestClassify_ContractChangeOutranksRemovedSymbol(t *testing.T) {
	rep := depdiff.Report{
		APIVersionChanges: []depdiff.APIVersionChange{{File: "c.go", From: "2021-01-01", To: "2026-01-01"}},
		RemovedFunctions:  []string{"OldClient"},
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

// TestClassify_ChangedExportedConstantIsCaution (Qodo #1): a changed exported
// constant (non-api-version) must lift a clean bump to at least CAUTION rather
// than being silently ACCEPTed.
func TestClassify_ChangedExportedConstantIsCaution(t *testing.T) {
	rep := depdiff.Report{
		Module: "example.com/x", FromVersion: "v1.0.0", ToVersion: "v1.1.0",
		ChangedConstants: []apidiff.ConstChange{
			{Name: "DefaultTimeout", From: "30", To: "60", Kind: "constant", Exported: true},
		},
	}
	if got := Classify(rep); got.Level != Caution {
		t.Errorf("changed exported constant should be CAUTION, got %s (%v)", got.Level, got.Reasons)
	}
}

// TestClassify_ChangedAPIVersionConstantIsHold: an api-version-shaped changed
// constant not already surfaced by the api-version detector is a HOLD.
func TestClassify_ChangedAPIVersionConstantIsHold(t *testing.T) {
	rep := depdiff.Report{
		Module: "example.com/x", FromVersion: "v1.0.0", ToVersion: "v1.1.0",
		ChangedConstants: []apidiff.ConstChange{
			{Name: "apiVersion", From: "2024-03-01", To: "2026-01-01", Kind: "api-version", Exported: false},
		},
	}
	if got := Classify(rep); got.Level != Hold {
		t.Errorf("changed api-version constant should be HOLD, got %s (%v)", got.Level, got.Reasons)
	}
}

// TestContractConstants_DedupAndScope (Qodo #1): an api-version constant already
// reported by the api-version detector is not double-counted, and an unexported
// non-api-version constant is treated as internal noise (excluded).
func TestContractConstants_DedupAndScope(t *testing.T) {
	rep := depdiff.Report{
		APIVersionChanges: []depdiff.APIVersionChange{
			{File: "client.go", From: "2024-03-01", To: "2026-01-01"},
		},
		ChangedConstants: []apidiff.ConstChange{
			{Name: "apiVersion", From: "2024-03-01", To: "2026-01-01", Kind: "api-version", Exported: false}, // dup -> excluded
			{Name: "internalRetry", From: "3", To: "5", Kind: "constant", Exported: false},                   // unexported -> excluded
			{Name: "DefaultTimeout", From: "30", To: "60", Kind: "constant", Exported: true},                 // kept
		},
	}
	got := ContractConstants(rep)
	if len(got) != 1 || got[0].Name != "DefaultTimeout" {
		t.Errorf("expected only DefaultTimeout to survive dedup+scope, got %+v", got)
	}
	// The overall verdict stays HOLD from the api-version detector, and the
	// changed constant must not downgrade it.
	if v := Classify(rep); v.Level != Hold {
		t.Errorf("verdict should remain HOLD, got %s", v.Level)
	}
}
