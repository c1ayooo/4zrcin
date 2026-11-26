package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Only the operator's HTTP handlers may write a durable instruction. CreatedBy
// is a field the caller fills in, so a runtime check would only stop honest
// mistakes: a model calling CreateInstruction in-process can stamp the same
// constant. The real boundary is the call-site set, and this test is it.
//
// Falsification: add a CreateInstruction(...) call anywhere outside internal/api
// (e.g. from the decider) and this test fails; delete the handler call and it
// fails the other way, so the expected set cannot quietly shrink either.
func TestOnlyOperatorHandlersWriteInstructions(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(root, "**", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := filepath.Glob(filepath.Join(root, "*", "*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, all...)

	// The fence flows no longer write instructions at all: the pending flow is
	// quiet (the asset row is the record; the board's fence block carries the
	// standing), and every durable guidance is the operator's own words through
	// the two surfaces below. The guard keeps the set at exactly two so a model
	// path cannot quietly grow a third.
	want := []string{
		"api/directives.go:writePersistentDirective",
		"api/instructions.go:createInstruction",
	}
	got := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("%s: %v", path, perr)
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			t.Fatal(rerr)
		}
		rel = filepath.ToSlash(rel)
		// Only packages that can reach the store matter; parsing them all keeps the
		// rule independent of an import graph that changes shape.
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "CreateInstruction" {
					return true
				}
				got[rel+":"+fn.Name.Name] = true
				return true
			})
		}
	}

	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("CreateInstruction call sites = %v, want exactly %v", keys, want)
	}
}
