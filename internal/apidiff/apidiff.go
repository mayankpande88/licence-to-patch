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
	Name string `json:"name"` // package-qualified: <pkg-dir>.<Ident>
	File string `json:"file"` // module-relative path where it is declared
	From string `json:"from"` // literal value in the old version
	To   string `json:"to"`   // literal value in the new version
	Kind string `json:"kind"` // "api-version" for date/version-shaped values, else "constant"
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
			Name: key,
			File: oldC.file,
			From: oldC.value,
			To:   newC.value,
			Kind: classify(oldC.value, newC.value),
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
	value string
	file  string
}

// constValues walks dir and returns a map of package-qualified constant name to
// its literal value. Keying by the constant's package directory keeps names
// from distinct sub-packages of the same module from colliding.
func constValues(dir string) map[string]constVal {
	out := map[string]constVal{}
	fset := token.NewFileSet()
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil // skip unparseable files rather than failing the whole diff
		}
		rel, _ := filepath.Rel(dir, p)
		pkgKey := filepath.Dir(rel)
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue // no explicit value (e.g. an iota continuation)
					}
					lit, ok := literalValue(vs.Values[i])
					if !ok {
						continue
					}
					key := name.Name
					if pkgKey != "" && pkgKey != "." {
						key = pkgKey + "." + name.Name
					}
					// First declaration wins; a package declares each const once.
					if _, seen := out[key]; !seen {
						out[key] = constVal{value: lit, file: rel}
					}
				}
			}
		}
		return nil
	})
	return out
}

// literalValue extracts a comparable string from a simple literal expression.
// It returns ok=false for anything that isn't a bare literal (identifiers,
// calls, binary/composite expressions) so only unambiguous values are compared.
func literalValue(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if unq, err := strconv.Unquote(e.Value); err == nil {
				return unq, true
			}
			return e.Value, true
		}
		return e.Value, true // INT, FLOAT, CHAR, IMAG — compare source form
	case *ast.UnaryExpr:
		// Negated numeric literal, e.g. -1.
		if inner, ok := e.X.(*ast.BasicLit); ok && (inner.Kind == token.INT || inner.Kind == token.FLOAT) {
			return e.Op.String() + inner.Value, true
		}
	}
	return "", false
}
