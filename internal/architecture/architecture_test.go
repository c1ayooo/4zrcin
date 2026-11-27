// Package architecture enforces the layer table docs/19 §4 froze: an
// internal package may import only packages in its own or a lower layer, so
// the boundary an AI (or a person) crosses is machine-checked instead of
// folk knowledge. Pure AST analysis — no build, no network. Test files are
// exempt: tests exercise other packages by design.
package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// layerTable is docs/19 §4's table, lowest layer first. A package may import
// only packages in its own or an EARLIER layer. Subpackages inherit their
// first segment's layer (aop/* protocol, host/inproc orchestration,
// web/bundle entry).
var layerTable = []struct {
	layer    int
	packages []string
}{
	{0, []string{"model", "collab", "contract", "istate"}},
	{1, []string{"aop", "ws", "connectretry", "ioa"}},
	{2, []string{"executor", "aopexec", "enroll", "noderes", "platform", "tunnel", "relay", "deploy"}},
	{3, []string{"store", "ledger", "state", "project", "artifact", "evidence", "access"}},
	{4, []string{"board", "retrieval", "bundle", "traffic", "metrics", "report", "assets", "kb", "pocs", "skills", "vulnlib", "resource"}},
	{5, []string{"decider", "verifier", "lifecycle", "modelclient"}},
	{6, []string{"host", "node", "fleet"}},
	{7, []string{"api", "web", "settings", "account", "notify"}},
	{8, []string{"buildinfo", "testutil"}},
}

var layerOf = func() map[string]int {
	m := make(map[string]int)
	for _, row := range layerTable {
		for _, name := range row.packages {
			m[name] = row.layer
		}
	}
	return m
}()

// layer resolves one internal package's layer by its first path segment.
// Unknown segments are layer 0 (types) — a new leaf package is the common
// case, and a leaf belongs at the bottom; putting it higher is a decision
// that belongs in this table, in the open.
func layer(pkg string) int {
	if l, ok := layerOf[pkg]; ok {
		return l
	}
	return 0
}

func TestInternalPackageLayers(t *testing.T) {
	repository := repositoryRoot(t)
	entries, err := os.ReadDir(filepath.Join(repository, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkg := entry.Name()
		own := layer(pkg)
		dir := filepath.Join(repository, "internal", pkg)
		if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			// Generator scripts (package main under internal/, e.g.
			// internal/web/generate) are build tools run by go:generate and the
			// gate, not part of the runtime graph.
			if f.Name.Name == "main" {
				return nil
			}
			for _, imp := range f.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(path, "github.com/c1ayooo/4zrcin/internal/") {
					continue
				}
				dep := strings.TrimPrefix(path, "github.com/c1ayooo/4zrcin/internal/")
				depLayer := layer(strings.SplitN(dep, "/", 2)[0])
				if depLayer > own {
					return fmt.Errorf("层级越界：%s (第 %d 层) import 了 %s (第 %d 层)——只能 import 同层或更低；%s:%d",
						pkg, own, dep, depLayer, path, f.Imports[0].Pos())
				}
			}
			return nil
		}); err != nil {
			t.Errorf("%s: %v", pkg, err)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}
