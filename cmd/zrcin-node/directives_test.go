package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testConfigFor(t *testing.T, fp *fakePlatform) (*Config, string) {
	t.Helper()
	root := t.TempDir()
	return &Config{
		Version: configVersion, NodeID: testNodeID, Name: "n",
		PublicURL: fp.srv.URL, Credential: testCredential,
		Root: root, Service: "none",
	}, root
}

// An uninstall directive ends the loop with the typed signal, and the executed
// sequence persists so a redelivery after a restart is a no-op.
func TestDirectiveLoopRunsUninstallOnce(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, root := testConfigFor(t, fp)
	fp.nextMu.Lock()
	fp.next = []map[string]any{{"seq": 7, "op": "uninstall"}}
	fp.nextMu.Unlock()

	err := directiveLoop(context.Background(), cfg)
	if !errors.Is(err, errUninstallRequested) {
		t.Fatalf("directiveLoop = %v, want errUninstallRequested", err)
	}
	if got := loadDirectiveSeq(cfg); got != 7 {
		t.Fatalf("persisted seq = %d, want 7", got)
	}
	if _, err := os.Stat(filepath.Join(root, "var", "directives-seq")); err != nil {
		t.Fatalf("seq file: %v", err)
	}

	// Redelivery of the same sequence (a platform restart replays it) must not
	// execute again: the loop keeps polling instead of returning.
	fp.nextMu.Lock()
	fp.next = []map[string]any{{"seq": 7, "op": "uninstall"}}
	fp.nextMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := directiveLoop(ctx, cfg); err != nil {
		t.Fatalf("redelivered seq executed again: %v", err)
	}
}

func TestDirectiveLoopRevocationEndsIt(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, _ := testConfigFor(t, fp)
	fp.ticketGone.Store(true)
	if err := directiveLoop(context.Background(), cfg); !errors.Is(err, errRevoked) {
		t.Fatalf("directiveLoop on a destroyed node = %v, want errRevoked", err)
	}
}

func TestUnknownDirectiveIsSkippedButSequenced(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, _ := testConfigFor(t, fp)
	fp.nextMu.Lock()
	fp.next = []map[string]any{{"seq": 3, "op": "upgrade"}}
	fp.nextMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := directiveLoop(ctx, cfg); err != nil {
		t.Fatalf("unknown directive killed the loop: %v", err)
	}
	if got := loadDirectiveSeq(cfg); got != 3 {
		t.Fatalf("seq after an unknown op = %d, want 3", got)
	}
}

// The uninstaller must survive this process: it is spawned detached (or via a
// transient unit under systemd) and told that it is the one doing the
// destroying.
func TestSelfUninstallSpawnsADetachedProcess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "uninstalled")
	script := filepath.Join(dir, "zrcin-node")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+marker+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Root: dir, Service: "none"}
	// The launcher looks for bin/zrcin-node under the root.
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(script, filepath.Join(dir, "bin", "zrcin-node")); err != nil {
		t.Fatal(err)
	}
	if err := selfUninstall(cfg); err != nil {
		t.Fatalf("selfUninstall: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(marker); err == nil {
			if got := string(raw); got != "uninstall --root "+dir+"\n" {
				t.Fatalf("uninstaller argv = %q", got)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the detached uninstaller never ran")
}
