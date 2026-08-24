// Package verdict turns a depdiff report into a preliminary, deterministic risk
// signal for a single dependency bump.
//
// This is intentionally a PRE-SCREEN, not the final decision. It flags the
// mechanical facts a diff can prove — a changed REST api-version, a removed
// exported symbol — and leaves the judgment call (does this matter for THIS
// repo? accept, hold, or ask a human?) to the agent and, ultimately, the person
// approving the action. Deterministic where we can be; the model and the human
// for everything else.
package verdict

import (
	"fmt"
	"strings"

	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
)

// Level is the preliminary recommendation for a single bump.
type Level string

const (
	// Accept: the diff shows nothing that would break a caller.
	Accept Level = "ACCEPT"
	// Caution: a change that may break callers but is usually caught by CI
	// (e.g. a removed exported symbol → compile error).
	Caution Level = "CAUTION"
	// Hold: a change that can pass green CI and still break at runtime
	// (e.g. a silently changed REST api-version). This is the dangerous class.
	Hold Level = "HOLD"
)

// Verdict is the pre-screen for one dependency bump.
type Verdict struct {
	Module  string   `json:"module"`
	From    string   `json:"from_version"`
	To      string   `json:"to_version"`
	Level   Level    `json:"level"`
	Reasons []string `json:"reasons"`
}

// Classify computes the preliminary verdict for a depdiff report.
//
// Precedence: a runtime-contract change (Hold) outranks a compile-breaking
// change (Caution), which outranks a clean bump (Accept). A contract change is
// treated as the most dangerous because neither the compiler nor a mocked test
// suite will surface it.
func Classify(rep depdiff.Report) Verdict {
	v := Verdict{Module: rep.Module, From: rep.FromVersion, To: rep.ToVersion, Level: Accept}

	// One bounded reason regardless of how many files carry the change — the
	// per-file detail already lives in rep.APIVersionChanges, so we cite a single
	// example rather than duplicating every entry into the payload.
	if n := len(rep.APIVersionChanges); n > 0 {
		v.Level = Hold
		ex := rep.APIVersionChanges[0]
		v.Reasons = append(v.Reasons, fmt.Sprintf(
			"%d REST api-version change(s), e.g. %s: %s → %s — a runtime contract change that compiles and passes mocked tests but can be rejected by the live service",
			n, ex.File, ex.From, ex.To))
	}

	if len(rep.RemovedSymbols) > 0 {
		if v.Level == Accept {
			v.Level = Caution
		}
		v.Reasons = append(v.Reasons, fmt.Sprintf(
			"removes %d exported symbol(s), e.g. %s — callers that use them will fail to compile",
			len(rep.RemovedSymbols), preview(rep.RemovedSymbols)))
	}

	if len(v.Reasons) == 0 {
		v.Reasons = append(v.Reasons, "no api-version change and no removed exported symbols in the diff")
	}
	return v
}

func preview(names []string) string {
	const max = 3
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + ", …"
}
