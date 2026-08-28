// Package verdict turns a depdiff report into a preliminary, deterministic risk
// signal for a single dependency bump.
//
// This is intentionally a PRE-SCREEN, not the final decision. It flags the
// mechanical facts a diff can prove — a changed REST api-version, a removed
// exported function — and leaves the judgment call (does this matter for THIS
// repo? accept, hold, or ask a human?) to the agent and, ultimately, the person
// approving the action. Deterministic where we can be; the model and the human
// for everything else.
package verdict

import (
	"fmt"
	"strings"

	"github.com/mayankpande88/licence-to-patch/internal/apidiff"
	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
)

// Level is the preliminary recommendation for a single bump.
type Level string

const (
	// Accept: the diff shows nothing that would break a caller.
	Accept Level = "ACCEPT"
	// Caution: a change that may break callers but is usually caught by CI
	// (e.g. a removed exported function → compile error).
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

	if len(rep.RemovedFunctions) > 0 {
		if v.Level == Accept {
			v.Level = Caution
		}
		v.Reasons = append(v.Reasons, fmt.Sprintf(
			"removes %d exported function(s), e.g. %s — callers that use them will fail to compile",
			len(rep.RemovedFunctions), preview(rep.RemovedFunctions)))
	}

	// A baked-in constant whose value changed is a runtime behavioral change:
	// it compiles and passes mocked tests, yet shifts what the code does. An
	// api-version-shaped change is the dangerous, service-rejected class (Hold);
	// any other changed exported constant is worth a look (at least Caution).
	if cc := ContractConstants(rep); len(cc) > 0 {
		apiVer := false
		for _, c := range cc {
			if c.Kind == "api-version" {
				apiVer = true
				break
			}
		}
		if apiVer {
			v.Level = Hold
		} else if v.Level == Accept {
			v.Level = Caution
		}
		ex := cc[0]
		v.Reasons = append(v.Reasons, fmt.Sprintf(
			"%d changed constant value(s), e.g. %s: %q → %q — a baked-in value the bump silently changed; compiles and passes mocked tests but can shift runtime behavior",
			len(cc), ex.Name, ex.From, ex.To))
	}

	if len(v.Reasons) == 0 {
		v.Reasons = append(v.Reasons, "no api-version change, no removed exported functions, and no changed contract constants in the diff")
	}
	return v
}

// ContractConstants returns the changed constants that should influence the
// review, shared by the verdict and the brief so both agree. It drops:
//   - api-version constants already surfaced by the api-version detector (same
//     from→to), to avoid double-reporting the same change, and
//   - unexported, non-api-version constants, which are internal implementation
//     detail and too noisy to flag (they stay in the JSON report as evidence).
func ContractConstants(rep depdiff.Report) []apidiff.ConstChange {
	seen := map[string]bool{}
	for _, a := range rep.APIVersionChanges {
		seen[a.From+"\x00"+a.To] = true
	}
	var out []apidiff.ConstChange
	for _, c := range rep.ChangedConstants {
		if c.Kind == "api-version" && seen[c.From+"\x00"+c.To] {
			continue
		}
		if c.Kind != "api-version" && !c.Exported {
			continue
		}
		out = append(out, c)
	}
	return out
}

func preview(names []string) string {
	const max = 3
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + ", …"
}
