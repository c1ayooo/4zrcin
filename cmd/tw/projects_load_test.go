package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/project"
)

// brokenAndHealthy builds a projects root the way a failed delete leaves it: one
// directory with only runs/ and no ledger, next to a healthy project.
func brokenAndHealthy(t *testing.T) (root, healthyID string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "projects")
	seed, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	good, err := seed.Create("正常项目", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := good.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "p_broken", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, good.ID
}

// The startup path must survive a broken project directory: that is the fatal
// exit this fixes. The healthy project still loads, and the skipped directory
// rides to the console through the manager instead of being dropped silently.
func TestLoadProjectsSkipsABrokenDirectory(t *testing.T) {
	root, healthyID := brokenAndHealthy(t)
	mgr, err := loadProjects(root, "", "")
	if err != nil {
		t.Fatalf("启动路径仍然因一个坏项目返回错误（main 会 fatal）：%v", err)
	}
	t.Cleanup(func() {
		if p := mgr.Get(healthyID); p != nil {
			_ = p.Store.Close()
		}
	})
	if mgr.Len() != 1 || mgr.Get(healthyID) == nil {
		t.Fatalf("loaded = %d, want only the healthy project %s", mgr.Len(), healthyID)
	}
	skipped := mgr.Skipped()
	if len(skipped) != 1 || skipped[0].ID != "p_broken" {
		t.Fatalf("skipped = %+v, want exactly p_broken", skipped)
	}
	if !strings.Contains(skipped[0].Err, "ledger") {
		t.Fatalf("skipped error lost the reason: %q", skipped[0].Err)
	}
}

// An instance whose only project failed to load gets no invented default: that
// would paint a healthy empty project over the failure the operator needs to see.
func TestLoadProjectsDoesNotSeedADefaultOverAFailedLoad(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	broken := filepath.Join(root, "p_broken")
	if err := os.MkdirAll(filepath.Join(broken, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, err := loadProjects(root, "", "")
	if err != nil {
		t.Fatalf("broken-only instance = %v, want a degraded start", err)
	}
	if mgr.Len() != 0 {
		t.Fatalf("loaded = %d, want 0: a default project would hide the failure", mgr.Len())
	}
	if len(mgr.Skipped()) != 1 {
		t.Fatalf("skipped = %+v, want the broken directory", mgr.Skipped())
	}
	if _, err := os.Stat(filepath.Join(root, project.DefaultID)); !os.IsNotExist(err) {
		t.Fatalf("a default project was created next to the failed load (err=%v)", err)
	}
}

// A fresh instance still gets its default engagement.
func TestLoadProjectsSeedsTheDefaultOnAFreshInstance(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	mgr, err := loadProjects(root, "", "起点原文")
	if err != nil {
		t.Fatal(err)
	}
	p := mgr.Get(project.DefaultID)
	if p == nil {
		t.Fatalf("fresh instance has no default project (loaded=%d)", mgr.Len())
	}
	t.Cleanup(func() { _ = p.Store.Close() })
	if p.ScopeText() != "起点原文" {
		t.Fatalf("default project scope = %q, want the operator's --scope text", p.ScopeText())
	}
}

// Asking for the broken project by id stays an error, but it names the load
// failure instead of claiming the project never existed.
func TestLoadProjectsNamesTheLoadFailureForTheRequestedProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	if err := os.MkdirAll(filepath.Join(root, "p_broken", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := loadProjects(root, "p_broken", "")
	if err == nil {
		t.Fatal("requesting a project that did not load must fail: the operator asked for it")
	}
	for _, want := range []string{"没能加载", "p_broken", "ledger"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "没有这个项目") {
		t.Fatalf("a skipped project must not be reported as missing: %q", err)
	}
	if _, err := loadProjects(root, "p_nope", ""); err == nil || !strings.Contains(err.Error(), "没有这个项目") {
		t.Fatalf("a truly unknown id = %v, want the missing-project answer", err)
	}
}
