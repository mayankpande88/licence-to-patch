// Command depdiff downloads two versions of a Go module and reports the
// contract-relevant differences between them — the changes that a changelog
// often omits and that neither the compiler nor a mocked test suite will catch.
//
// It is the detection core of the "Licence to Patch" agent. Today it surfaces:
//
//   - api-version changes: REST api-version literals the module bakes into
//     requests (e.g. Azure ARM clients). A changed api-version is a runtime
//     contract change: your code compiles and your tests pass, but the live
//     service may reject the new version.
//   - removed exported symbols: exported funcs/methods present in the old
//     version but gone in the new one (a source of compile breaks / behavior loss).
//
// Usage:
//
//	depdiff <module-path> <from-version> <to-version>
//
// Example:
//
//	depdiff github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.12.0 v0.13.0
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type apiVersionChange struct {
	File string `json:"file"`
	From string `json:"from"`
	To   string `json:"to"`
}

type report struct {
	Module            string             `json:"module"`
	FromVersion       string             `json:"from_version"`
	ToVersion         string             `json:"to_version"`
	APIVersionChanges []apiVersionChange `json:"api_version_changes"`
	RemovedSymbols    []string           `json:"removed_exported_symbols"`
	FilesChanged      int                `json:"files_changed"`
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: depdiff <module-path> <from-version> <to-version>")
		os.Exit(2)
	}
	module, from, to := os.Args[1], os.Args[2], os.Args[3]

	fromDir, err := downloadModule(module, from)
	if err != nil {
		fatal("download %s@%s: %v", module, from, err)
	}
	toDir, err := downloadModule(module, to)
	if err != nil {
		fatal("download %s@%s: %v", module, to, err)
	}

	rep := report{Module: module, FromVersion: from, ToVersion: to}
	rep.APIVersionChanges = diffAPIVersions(fromDir, toDir)
	rep.RemovedSymbols = removedExportedSymbols(fromDir, toDir)
	rep.FilesChanged = countChangedFiles(fromDir, toDir)

	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
}

// downloadModule fetches module@version into the module cache and returns its dir.
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
	// const versionXxx = "2024-03-01-preview"  (with optional type: versionXxx string = "...")
	constRe = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)(?:\s+[A-Za-z0-9_.\[\]]+)?\s*=\s*"([0-9]{4}-[0-9]{2}-[0-9]{2}[0-9A-Za-z-]*)"`)
	// reqQP.Set("api-version", versionXxx)  OR  "api-version", "2024-03-01-preview"
	useConstRe = regexp.MustCompile(`api-version",\s*([A-Za-z0-9_]+)\b`)
	useLitRe   = regexp.MustCompile(`api-version",\s*"([0-9]{4}-[0-9]{2}-[0-9]{2}[0-9A-Za-z-]*)"`)
	exportedRe = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Z][A-Za-z0-9_]*)\s*\(`)
)

// apiVersionByFile maps each .go file (relative path) to the api-version literal it sends.
func apiVersionByFile(dir string) map[string]string {
	consts := map[string]string{} // const name -> literal (module-wide)
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

func diffAPIVersions(fromDir, toDir string) []apiVersionChange {
	fromAV := apiVersionByFile(fromDir)
	toAV := apiVersionByFile(toDir)
	var changes []apiVersionChange
	for file, oldV := range fromAV {
		if newV, ok := toAV[file]; ok && newV != oldV {
			changes = append(changes, apiVersionChange{File: file, From: oldV, To: newV})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].File < changes[j].File })
	return changes
}

// exportedSymbols returns the set of exported func/method names in a module dir.
func exportedSymbols(dir string) map[string]bool {
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

func removedExportedSymbols(fromDir, toDir string) []string {
	fromSet := exportedSymbols(fromDir)
	toSet := exportedSymbols(toDir)
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
	// Best-effort: count .go files whose content differs or that were added/removed.
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

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "depdiff: "+format+"\n", a...)
	os.Exit(1)
}
