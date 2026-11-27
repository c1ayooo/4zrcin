package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// A proxy change restarts scan only once the node is idle: a busy node keeps
// the new default for its next idle answer, so a direction in flight is never
// interrupted, and the platform is told the old one still applies until then.
func TestScanProxyRestartsOnlyWhenIdle(t *testing.T) {
	t.Setenv("CYBER_PROXY", "")
	p := newScanProxy()

	// Nothing runs yet: the first default is what scan starts with.
	if p.offer("socks5://a.test:1080", false) {
		t.Fatal("a restart was asked before scan ever ran")
	}
	p.launched()
	if got := envValue(p.env(), "CYBER_PROXY"); got != "socks5://a.test:1080" {
		t.Fatalf("scan env CYBER_PROXY = %q", got)
	}

	if p.offer("socks5://b.test:1080", true) {
		t.Fatal("a busy node was asked to restart")
	}
	if got := p.digest(); got != enroll.ScanProxyDigest("socks5://a.test:1080") {
		t.Fatal("the platform was told a default applies before scan restarted for it")
	}
	if !p.offer("socks5://b.test:1080", false) {
		t.Fatal("an idle node was not asked to restart")
	}
	select {
	case <-p.due():
	default:
		t.Fatal("the restart signal did not reach the supervisor")
	}
	p.takeRestart()
	p.launched()
	if got := envValue(p.env(), "CYBER_PROXY"); got != "socks5://b.test:1080" {
		t.Fatalf("scan env CYBER_PROXY after restart = %q", got)
	}
	if got := p.digest(); got != enroll.ScanProxyDigest("socks5://b.test:1080") {
		t.Fatal("the platform was not told the new default applies")
	}
	// Cleared back to direct: no variable, and the fingerprint of "nothing".
	if !p.offer("", false) {
		t.Fatal("clearing the proxy did not restart scan")
	}
	p.takeRestart()
	p.launched()
	if env := p.env(); len(env) != 0 {
		t.Fatalf("direct egress still carries an env: %v", env)
	}
	if got := p.digest(); got != enroll.ScanProxyDigest("") {
		t.Fatal("direct egress was not reported as applied")
	}
}

// CYBER_PROXY in the launcher's own environment is the operator's explicit
// choice and wins (the harness reads it over any file): scan inherits it, no
// restart is ever asked, and the platform is told the node is reconciled.
func TestScanProxyInheritedEnvWins(t *testing.T) {
	t.Setenv("CYBER_PROXY", "http://env-proxy.test:8080")
	p := newScanProxy()
	p.offer("socks5://a.test:1080", false)
	p.launched()
	if env := p.env(); len(env) != 0 {
		t.Fatalf("the inherited CYBER_PROXY was overridden: %v", env)
	}
	if p.offer("socks5://b.test:1080", false) {
		t.Fatal("a node with its own CYBER_PROXY was asked to restart")
	}
	if got := p.digest(); got != enroll.ScanProxyDigest("socks5://b.test:1080") {
		t.Fatal("the platform was not told the node needs no restart")
	}
}

// envValue returns the last definition of key in a "K=V" list ("" when absent).
func envValue(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			value = strings.TrimPrefix(kv, key+"=")
		}
	}
	return value
}

// fakeScanScript is a stand-in scan: it logs the proxy it was started with and
// waits. TERM makes it exit the way a deliberate restart expects.
func fakeScanScript(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "scan")
	body := "#!/bin/sh\ntrap 'exit 0' TERM\nprintf '%s\\n' \"${CYBER_PROXY:-direct}\" >> " + filepath.Join(dir, "starts") +
		"\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func scanStarts(t *testing.T, dir string) []string {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(dir, "starts"))
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// waitForScanStarts polls until cond holds, then reports the scan starts so far.
func waitForScanStarts(t *testing.T, what string, dir string, cond func([]string) bool) []string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		starts := scanStarts(t, dir)
		if cond(starts) {
			return starts
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; scan starts = %q", what, starts)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// End to end on the node: the platform hands out a default on the directive
// channel, scan is started under it, and a later change restarts scan only
// once the platform says the node is idle — without counting as a crash.
func TestSuperviseScanFollowsThePlatformProxy(t *testing.T) {
	t.Setenv("CYBER_PROXY", "")
	fp := newFakePlatform(t)
	dir := t.TempDir()
	cfg, _ := testConfigFor(t, fp)
	cfg.ScanBin = fakeScanScript(t, dir)
	cfg.DataDir = dir
	cfg.HubAddr, cfg.HubKey = "127.0.0.1:1", "k"
	state := &stateWriter{path: filepath.Join(dir, "run-state.json")}
	proxies := newScanProxy()
	fp.setScanProxy("socks5://a.test:1080", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := seedScanProxy(ctx, cfg, proxies)

	done := make(chan struct{})
	go func() {
		superviseScan(ctx, cfg, state, proxies)
		close(done)
	}()
	go directiveLoop(ctx, cfg, proxies, first)
	starts := waitForScanStarts(t, "the first start under the platform default", dir, func(s []string) bool { return len(s) >= 1 })
	if starts[0] != "socks5://a.test:1080" {
		t.Fatalf("first start egress = %q, want the platform default", starts[0])
	}

	// Busy: the node keeps the old egress.
	fp.setScanProxy("socks5://b.test:1080", true)
	time.Sleep(700 * time.Millisecond)
	if got := scanStarts(t, dir); len(got) != 1 {
		t.Fatalf("scan restarted while the node was busy: %q", got)
	}
	// Idle: one restart, under the new default.
	fp.setScanProxy("socks5://b.test:1080", false)
	starts = waitForScanStarts(t, "the idle restart", dir, func(s []string) bool { return len(s) >= 2 })
	if starts[1] != "socks5://b.test:1080" {
		t.Fatalf("restarted scan egress = %q", starts[1])
	}
	time.Sleep(300 * time.Millisecond)
	if got := scanStarts(t, dir); len(got) != 2 {
		t.Fatalf("scan restarted more than once: %q", got)
	}
	if n := state.restartCount(); n != 0 {
		t.Fatalf("a deliberate restart was counted as a crash: %d", n)
	}
	if got := proxies.digest(); got != enroll.ScanProxyDigest("socks5://b.test:1080") {
		t.Fatal("the heartbeat would not report the new default applied")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("superviseScan did not stop with ctx")
	}
}
