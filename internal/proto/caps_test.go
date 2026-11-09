package proto

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestEveryCapIsAnnounced: a Cap* constant that never reaches Capabilities
// is a feature every client refuses as "server too old" — session.star.v1
// was defined and served, and starring never worked. CapWorktreeWatch is in
// the list too; the server drops it at run time when it has no watcher
// (internal/server), which is a different matter from never announcing it.
func TestEveryCapIsAnnounced(t *testing.T) {
	// Deliberately not in Capabilities, with the reason. Empty today.
	notAnnounced := map[string]string{}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Cap") || !name.IsExported() || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Errorf("%s: not a string literal; the test cannot read it", name.Name)
						continue
					}
					v, _ := strconv.Unquote(lit.Value)
					found++
					if _, ok := notAnnounced[name.Name]; ok {
						if slices.Contains(Capabilities, v) {
							t.Errorf("%s (%s) is announced; take it off the not-announced list", name.Name, v)
						}
						continue
					}
					if !slices.Contains(Capabilities, v) {
						t.Errorf("%s (%s) is missing from Capabilities: clients will refuse it as an old server", name.Name, v)
					}
				}
			}
		}
	}
	if found < 10 {
		t.Fatalf("found only %d Cap constants; the parse is not seeing them", found)
	}
}

func TestCapabilitiesHaveNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Capabilities {
		if c == "" {
			t.Error("empty capability")
		}
		if seen[c] {
			t.Errorf("%s announced twice", c)
		}
		seen[c] = true
	}
}
