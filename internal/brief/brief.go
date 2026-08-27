// Package brief renders a dependency-bump finding as a detailed, review-ready
// markdown section — what changed, why it matters, and what to do — so a PR
// review explains itself instead of just stamping ACCEPT/HOLD.
package brief

import (
	"fmt"
	"strings"

	"github.com/mayankpande88/licence-to-patch/internal/apidiff"
	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
	"github.com/mayankpande88/licence-to-patch/internal/verdict"
)

// Explain returns a markdown section for one dependency bump. A single bump can
// carry more than one kind of contract change, so every applicable section is
// rendered — not just the first.
func Explain(rep depdiff.Report, v verdict.Verdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s %s — `%s` %s → %s\n\n", icon(v.Level), v.Level, rep.Module, rep.FromVersion, rep.ToVersion)

	wrote := false
	if len(rep.APIVersionChanges) > 0 {
		writeAPIVersion(&b, rep)
		wrote = true
	}
	if len(rep.RemovedSymbols) > 0 {
		if wrote {
			b.WriteString("\n")
		}
		writeRemovedSymbols(&b, rep)
		wrote = true
	}
	if cc := verdict.ContractConstants(rep); len(cc) > 0 {
		if wrote {
			b.WriteString("\n")
		}
		writeChangedConstants(&b, cc)
		wrote = true
	}
	if !wrote {
		writeClean(&b, rep)
	}
	return b.String()
}

func writeAPIVersion(b *strings.Builder, rep depdiff.Report) {
	b.WriteString("**What changed:** the baked-in REST `api-version` this module sends changed — a value inside the dependency, not in your code:\n")
	for _, c := range rep.APIVersionChanges {
		fmt.Fprintf(b, "- `%s`: `%s` → `%s`\n", c.File, c.From, c.To)
	}
	b.WriteString("\n**Why it matters:** this is a *runtime contract change*, not a code change. Your project still compiles and unit tests still pass — the api-version is a constant inside the dependency, and mocked tests never send it to the real service. But at runtime the client calls the API with the new version; if the target service or resource does not support it, the request fails (commonly a `4xx`, e.g. `404`) — often silently. The changelog typically does **not** mention this; only a source diff between the two versions reveals it.\n\n")
	b.WriteString("**Recommendation:** hold this bump out of the group and verify it against a live or staging environment before merging.\n")
}

func writeRemovedSymbols(b *strings.Builder, rep depdiff.Report) {
	fmt.Fprintf(b, "**What changed:** the new version removes %d exported symbol(s): %s.\n\n", len(rep.RemovedSymbols), code(rep.RemovedSymbols))
	b.WriteString("**Why it matters:** any call site that uses a removed symbol will fail to compile. CI will catch this, but the bump cannot merge without code changes.\n\n")
	b.WriteString("**Recommendation:** update the affected call sites to the new API, or pin the current version until you can.\n")
}

func writeChangedConstants(b *strings.Builder, cc []apidiff.ConstChange) {
	b.WriteString("**What changed:** the value of a baked-in constant this module declares changed — a value inside the dependency, not in your code:\n")
	const max = 8
	for i, c := range cc {
		if i == max {
			fmt.Fprintf(b, "- …and %d more\n", len(cc)-max)
			break
		}
		fmt.Fprintf(b, "- `%s` (`%s`): `%s` → `%s`\n", c.Name, c.File, c.From, c.To)
	}
	b.WriteString("\n**Why it matters:** a changed constant value — a default timeout, an endpoint, a retry count, an enum, an api-version — is a *runtime behavioral change*, not a code change. Your project still compiles and unit tests still pass, but the bumped code now does something different at runtime. The changelog often does **not** mention it; only a source diff between the two versions reveals it.\n\n")
	b.WriteString("**Recommendation:** confirm the changed value is compatible with how this repo uses the module before merging; if it drives behavior you depend on, verify against a live or staging environment.\n")
}

func writeClean(b *strings.Builder, rep depdiff.Report) {
	fmt.Fprintf(b, "**What changed:** %d file(s) changed, with no REST api-version literals altered, no exported symbols removed, and no baked-in constant values changed.\n\n", rep.FilesChanged)
	b.WriteString("**Why it's safe:** nothing in the diff can break a caller at compile time or silently at runtime. This is a routine update.\n")
}

func icon(level verdict.Level) string {
	switch level {
	case verdict.Hold:
		return "🛑"
	case verdict.Caution:
		return "⚠️"
	default:
		return "✅"
	}
}

func code(names []string) string {
	const max = 5
	quoted := make([]string, 0, len(names))
	for i, n := range names {
		if i == max {
			quoted = append(quoted, "…")
			break
		}
		quoted = append(quoted, "`"+n+"`")
	}
	return strings.Join(quoted, ", ")
}
