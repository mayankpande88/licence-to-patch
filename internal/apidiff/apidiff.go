// Package apidiff performs an AST-level semantic diff of a Go module's source
// between two versions. Where the sibling regex detector only grepped for
// date-shaped api-version strings, this parses every .go file and compares the
// declared VALUE of each package-level constant — so it surfaces any baked-in
// contract value a bump silently changed: a REST api-version, a default
// timeout, an endpoint host, a retry count, an enum value. These are the
// changes that compile cleanly and pass mocked tests, then shift behavior at
// runtime — exactly what a changelog tends to omit.
package apidiff

import (
	"go/ast"
	"go/build/constraint"
	"go/constant"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ConstChange is a package-level constant whose literal value changed between
// two module versions.
type ConstChange struct {
	Name     string `json:"name"`     // package-qualified: <pkg-dir>.<Ident>
	File     string `json:"file"`     // module-relative path where it is declared
	From     string `json:"from"`     // literal value in the old version
	To       string `json:"to"`       // literal value in the new version
	Kind     string `json:"kind"`     // "api-version" for date/version-shaped values, else "constant"
	Exported bool   `json:"exported"` // whether the identifier is exported (part of the public contract)
}

// apiVersionLike matches values that read as a REST api-version or a date-based
// version stamp (e.g. "2024-03-01", "2024-03-01-preview", "v2023-11-15").
var apiVersionLike = regexp.MustCompile(`^v?[0-9]{4}-[0-9]{2}-[0-9]{2}`)

// Changed parses both source trees and returns the package-level constants
// whose literal value differs. Only simple literal values are compared
// (strings, ints, floats, chars, and negated numbers); computed expressions,
// iota runs, and composite values are skipped so the result is a set of
// unambiguous value changes rather than noise.
func Changed(fromDir, toDir string) []ConstChange {
	from := constValues(fromDir)
	to := constValues(toDir)

	var changes []ConstChange
	for key, oldC := range from {
		newC, ok := to[key]
		if !ok || newC.value == oldC.value {
			continue
		}
		changes = append(changes, ConstChange{
			Name:     newC.name,
			File:     oldC.file,
			From:     oldC.value,
			To:       newC.value,
			Kind:     classify(oldC.value, newC.value),
			Exported: newC.exported,
		})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind < changes[j].Kind // "api-version" sorts before "constant"
		}
		return changes[i].Name < changes[j].Name
	})
	return changes
}

func classify(from, to string) string {
	if apiVersionLike.MatchString(from) || apiVersionLike.MatchString(to) {
		return "api-version"
	}
	return "constant"
}

type constVal struct {
	value    string
	file     string
	name     string // package-qualified display name
	exported bool
}

// constValues walks dir and returns a map of variant-qualified constant key to
// its literal value. The key is <pkg-dir>|<build-variant>.<Ident>: keying by the
// constant's package directory keeps names from distinct sub-packages from
// colliding, and folding in the build variant (GOOS/GOARCH suffix + //go:build
// constraint) keeps mutually-exclusive platform declarations of the same name
// from overwriting each other. The same variant in both module versions maps to
// the same key, so a per-variant value change is still detected.
func constValues(dir string) map[string]constVal {
	out := map[string]constVal{}
	fset := token.NewFileSet()
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return nil // skip unparseable files rather than failing the whole diff
		}
		rel, _ := filepath.Rel(dir, p)
		pkgKey := filepath.Dir(rel)
		variant := fileVariant(filepath.Base(p), file)
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			// Within one const block, a spec with no expression list inherits the
			// previous spec's list (Go's iota/carry-forward rule). Track it so an
			// inherited literal is captured rather than skipped.
			var last []ast.Expr
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				values := vs.Values
				if len(values) == 0 {
					values = last
				} else {
					last = values
				}
				for i, name := range vs.Names {
					if i >= len(values) {
						continue // no value to compare (e.g. a bare iota continuation)
					}
					lit, ok := literalValue(values[i])
					if !ok {
						continue
					}
					display := name.Name
					if pkgKey != "" && pkgKey != "." {
						display = pkgKey + "." + name.Name
					}
					key := pkgKey + "|" + variant + "." + name.Name
					// First declaration of a given (variant, name) wins; a package
					// declares each const once per build variant.
					if _, seen := out[key]; !seen {
						out[key] = constVal{value: lit, file: rel, name: display, exported: name.IsExported()}
					}
				}
			}
		}
		return nil
	})
	return out
}

// goosList and goarchList are the recognized platform tokens used to detect a
// GOOS/GOARCH suffix in a filename (e.g. endpoints_windows.go, x_linux_amd64.go).
var (
	goosList = set("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios",
		"js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos")
	goarchList = set("386", "amd64", "amd64p32", "arm", "arm64", "arm64be", "armbe", "loong64",
		"mips", "mips64", "mips64le", "mips64p32", "mips64p32le", "mipsle", "ppc", "ppc64",
		"ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm")
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// fileVariant returns a discriminator that distinguishes build-constrained
// declarations of the same constant: the normalized //go:build (or legacy
// +build) constraint plus any GOOS/GOARCH filename suffix. Files with no
// constraint and no platform suffix return "" and so key identically.
func fileVariant(base string, f *ast.File) string {
	return buildConstraint(f) + "#" + filenameSuffix(base)
}

func buildConstraint(f *ast.File) string {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if constraint.IsGoBuild(c.Text) {
				if expr, err := constraint.Parse(c.Text); err == nil {
					return expr.String()
				}
			}
			if constraint.IsPlusBuild(c.Text) {
				if expr, err := constraint.Parse(c.Text); err == nil {
					return expr.String()
				}
			}
		}
	}
	return ""
}

func filenameSuffix(base string) string {
	name := strings.TrimSuffix(base, ".go")
	parts := strings.Split(name, "_")
	if len(parts) < 2 {
		return ""
	}
	last := parts[len(parts)-1]
	if len(parts) >= 3 {
		if prev := parts[len(parts)-2]; goosList[prev] && goarchList[last] {
			return prev + "_" + last
		}
	}
	if goosList[last] || goarchList[last] {
		return last
	}
	return ""
}

// literalValue extracts a comparable, normalized string from a simple literal
// expression. Numeric and rune literals are canonicalized via go/constant so
// spelling-only differences (1 vs 01, 1.0 vs 1.00, 'A' vs '\x41') are not
// reported as value changes. It returns ok=false for anything that isn't a bare
// literal (identifiers, calls, binary/composite expressions).
func literalValue(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if unq, err := strconv.Unquote(e.Value); err == nil {
				return unq, true
			}
			return e.Value, true
		}
		if v := constant.MakeFromLiteral(e.Value, e.Kind, 0); v.Kind() != constant.Unknown {
			return v.ExactString(), true
		}
		return e.Value, true // fall back to source form if it won't parse
	case *ast.UnaryExpr:
		// Signed numeric literal, e.g. -1 or +2.
		inner, ok := e.X.(*ast.BasicLit)
		if !ok || (inner.Kind != token.INT && inner.Kind != token.FLOAT) {
			return "", false
		}
		v := constant.MakeFromLiteral(inner.Value, inner.Kind, 0)
		if v.Kind() == constant.Unknown {
			return e.Op.String() + inner.Value, true
		}
		switch e.Op {
		case token.SUB:
			return constant.UnaryOp(token.SUB, v, 0).ExactString(), true
		case token.ADD:
			return v.ExactString(), true
		}
	}
	return "", false
}
