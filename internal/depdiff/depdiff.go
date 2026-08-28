// Package depdiff downloads two versions of a Go module and reports the
// contract-relevant differences between them — the changes a changelog often
// omits and that neither the compiler nor a mocked test suite will catch.
package depdiff

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mayankpande88/licence-to-patch/internal/apidiff"
)

// APIVersionChange is a REST api-version literal that a module file changed
// between versions. A changed api-version is a runtime contract change: the
// code compiles and mocked tests pass, but the live service may reject it.
type APIVersionChange struct {
	File string `json:"file"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Report is the structured result of diffing two module versions.
type Report struct {
	Module            string                `json:"module"`
	FromVersion       string                `json:"from_version"`
	ToVersion         string                `json:"to_version"`
	APIVersionChanges []APIVersionChange    `json:"api_version_changes"`
	ChangedConstants  []apidiff.ConstChange `json:"changed_constants"`
	RemovedFunctions  []string              `json:"removed_exported_functions"`
	FilesChanged      int                   `json:"files_changed"`
}

// Diff downloads module@from and module@to and reports their contract diffs.
func Diff(module, from, to string) (Report, error) {
	fromDir, err := downloadModule(module, from)
	if err != nil {
		return Report{}, fmt.Errorf("download %s@%s: %w", module, from, err)
	}
	toDir, err := downloadModule(module, to)
	if err != nil {
		return Report{}, fmt.Errorf("download %s@%s: %w", module, to, err)
	}
	rep := Report{
		Module:            module,
		FromVersion:       from,
		ToVersion:         to,
		APIVersionChanges: diffAPIVersions(fromDir, toDir),
		ChangedConstants:  dropVersionStamps(apidiff.Changed(fromDir, toDir), from, to),
		RemovedFunctions:  removedExportedFunctions(fromDir, toDir),
		FilesChanged:      countChangedFiles(fromDir, toDir),
	}
	if rep.APIVersionChanges == nil {
		rep.APIVersionChanges = []APIVersionChange{}
	}
	if rep.ChangedConstants == nil {
		rep.ChangedConstants = []apidiff.ConstChange{}
	}
	if rep.RemovedFunctions == nil {
		rep.RemovedFunctions = []string{}
	}
	return rep, nil
}

func downloadModule(module, version string) (string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", module+"@"+version)
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr(err))
	}
	var meta struct{ Dir string }
	if err := json.Unmarshal(out, &meta); err != nil {
		return "", err
	}
	if meta.Dir == "" {
		return "", fmt.Errorf("no Dir in go mod download output")
	}
	return meta.Dir, nil
}

var (
	constRe    = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)(?:\s+[A-Za-z0-9_.\[\]]+)?\s*=\s*"([0-9]{4}-[0-9]{2}-[0-9]{2}[0-9A-Za-z-]*)"`)
	useConstRe = regexp.MustCompile(`api-version",\s*([A-Za-z0-9_]+)\b`)
	useLitRe   = regexp.MustCompile(`api-version",\s*"([0-9]{4}-[0-9]{2}-[0-9]{2}[0-9A-Za-z-]*)"`)
	exportedRe = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Z][A-Za-z0-9_]*)\s*\(`)
)

func apiVersionByFile(dir string) map[string]string {
	consts := map[string]string{}
	files := map[string][]byte{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		files[rel] = b
		for _, m := range constRe.FindAllStringSubmatch(string(b), -1) {
			consts[m[1]] = m[2]
		}
		return nil
	})

	result := map[string]string{}
	for rel, b := range files {
		s := string(b)
		if m := useLitRe.FindStringSubmatch(s); m != nil {
			result[rel] = m[1]
			continue
		}
		if m := useConstRe.FindStringSubmatch(s); m != nil {
			if lit, ok := consts[m[1]]; ok {
				result[rel] = lit
			}
		}
	}
	return result
}

// versionStampName matches identifiers that name a module/SDK self-version
// stamp (Version, moduleVersion, sdkVersion, pkgVersion, semVer, …). Requiring a
// name match — not just a value that happens to equal the release pair — keeps
// an unrelated contract constant (e.g. ProtocolVersion) that changes alongside a
// release from being silently dropped.
var versionStampName = regexp.MustCompile(`(?i)(^|_)(module|sdk|pkg|lib|semantic)?_?(version|semver)$`)

// dropVersionStamps removes the module's own self-version constant (e.g.
// moduleVersion "v0.12.0" -> "v0.13.0") from the changed-constant list. That
// bump is not a contract change — every release changes it — and reporting it
// is pure noise. A constant is treated as a self-stamp only when BOTH its value
// goes from the module's own from-version to its to-version (ignoring a leading
// "v") AND its identifier reads like a version stamp.
func dropVersionStamps(changes []apidiff.ConstChange, from, to string) []apidiff.ConstChange {
	trim := func(s string) string { return strings.TrimPrefix(s, "v") }
	f, t := trim(from), trim(to)
	kept := changes[:0]
	for _, c := range changes {
		ident := c.Name
		if i := strings.LastIndex(ident, "."); i >= 0 {
			ident = ident[i+1:]
		}
		if trim(c.From) == f && trim(c.To) == t && versionStampName.MatchString(ident) {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

func diffAPIVersions(fromDir, toDir string) []APIVersionChange {
	fromAV := apiVersionByFile(fromDir)
	toAV := apiVersionByFile(toDir)
	var changes []APIVersionChange
	for file, oldV := range fromAV {
		if newV, ok := toAV[file]; ok && newV != oldV {
			changes = append(changes, APIVersionChange{File: file, From: oldV, To: newV})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].File < changes[j].File })
	return changes
}

func exportedFunctions(dir string) map[string]bool {
	set := map[string]bool{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, m := range exportedRe.FindAllStringSubmatch(string(b), -1) {
			set[m[1]] = true
		}
		return nil
	})
	return set
}

func removedExportedFunctions(fromDir, toDir string) []string {
	fromSet := exportedFunctions(fromDir)
	toSet := exportedFunctions(toDir)
	var removed []string
	for name := range fromSet {
		if !toSet[name] {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	return removed
}

func countChangedFiles(fromDir, toDir string) int {
	from := readGoFiles(fromDir)
	to := readGoFiles(toDir)
	changed := 0
	seen := map[string]bool{}
	for rel, b := range from {
		seen[rel] = true
		if tb, ok := to[rel]; !ok || string(tb) != string(b) {
			changed++
		}
	}
	for rel := range to {
		if !seen[rel] {
			changed++
		}
	}
	return changed
}

func readGoFiles(dir string) map[string][]byte {
	files := map[string][]byte{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil {
			rel, _ := filepath.Rel(dir, p)
			files[rel] = b
		}
		return nil
	})
	return files
}

func stderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return ""
}
