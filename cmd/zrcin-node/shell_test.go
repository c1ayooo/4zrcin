package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zwebshell/webshell"
)

// fakeLegacyShell stands in for a deployed legacy JSP shell. It records what
// reached it, can hold a request open, and tracks how many were open at once.
type fakeLegacyShell struct {
	srv   *httptest.Server
	delay time.Duration
	hits  atomic.Int32

	mu      sync.Mutex
	got     []url.Values
	open    int32
	maxOpen int32
}

func newFakeLegacyShell(t *testing.T, delay time.Duration) *fakeLegacyShell {
	t.Helper()
	f := &fakeLegacyShell{delay: delay}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		_ = r.ParseForm()
		f.mu.Lock()
		f.got = append(f.got, r.PostForm)
		f.open++
		if f.open > f.maxOpen {
			f.maxOpen = f.open
		}
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.open--
			f.mu.Unlock()
		}()
		if f.delay > 0 {
			select {
			case <-time.After(f.delay):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte("uid=0(root)"))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLegacyShell) requests() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.got...)
}

func (f *fakeLegacyShell) peakOpen() int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxOpen
}

func execTask(id, url string, notAfter time.Time) enroll.ShellTask {
	return enroll.ShellTask{
		RequestID:  id,
		URL:        url,
		Credential: webshell.Credential{Type: "jsp", Protocol: webshell.ShellProtocolLegacy, Password: "s3cret"},
		Op:         webshell.Op{Kind: webshell.OpExec, Command: "id"},
		NotAfter:   notAfter,
	}
}

// shellDirective renders a task the way the platform's JSON does, so the
// launcher decodes the real wire shape.
func shellDirective(t *testing.T, seq int, task enroll.ShellTask) map[string]any {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"seq": seq, "op": enroll.OpShell, "shell": m}
}

func queueDirectives(fp *fakePlatform, directives ...map[string]any) {
	fp.nextMu.Lock()
	fp.next = directives
	fp.nextMu.Unlock()
}

func waitResult(t *testing.T, fp *fakePlatform) enroll.ShellResult {
	t.Helper()
	select {
	case res := <-fp.results:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher never posted a result")
		return enroll.ShellResult{}
	}
}

// runLoopUntilDone runs the directive loop for a moment, so a goroutine it
// started is never left polling a server the test is tearing down.
func runLoopUntilDone(t *testing.T, cfg *Config, wait time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	done := make(chan error, 1)
	go func() { done <- directiveLoop(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// The operation runs on this host — the target sees the launcher's request,
// not the console's — and the answer goes back over /node/result.
func TestShellDirectiveRunsHereAndReportsBack(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, _ := testConfigFor(t, fp)
	shell := newFakeLegacyShell(t, 0)
	task := execTask("req-1", shell.srv.URL+"/s.jsp", time.Now().Add(time.Minute))
	queueDirectives(fp, shellDirective(t, 1, task))

	runLoopUntilDone(t, cfg, 2*time.Second)
	res := waitResult(t, fp)
	if res.RequestID != task.RequestID || res.Error != "" || res.Result == nil || res.Result.Output != "uid=0(root)" {
		t.Fatalf("result = %+v", res)
	}
	got := shell.requests()
	if len(got) != 1 || got[0].Get("pwd") != "s3cret" || got[0].Get("cmd") != "id" {
		t.Fatalf("the target saw %+v", got)
	}
	// The executed sequence persists like any directive's.
	deadline := time.Now().Add(2 * time.Second)
	for loadDirectiveSeq(cfg) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("persisted seq = %d, want 1", loadDirectiveSeq(cfg))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An operation whose caller has stopped waiting is not run: it may have been
// a write or a delete. The launcher says so instead of staying silent.
func TestExpiredShellTaskIsNotRun(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, _ := testConfigFor(t, fp)
	shell := newFakeLegacyShell(t, 0)
	queueDirectives(fp, shellDirective(t, 1, execTask("req-1", shell.srv.URL+"/s.jsp", time.Now().Add(-time.Minute))))

	runLoopUntilDone(t, cfg, 300*time.Millisecond)
	res := waitResult(t, fp)
	if !strings.Contains(res.Error, "已过期") || res.Result != nil {
		t.Fatalf("result = %+v, want an expired-task error", res)
	}
	if shell.hits.Load() != 0 {
		t.Fatalf("an expired operation reached the target %d times", shell.hits.Load())
	}
}

// A shell directive with no payload is a platform bug, not a reason to stop
// following instructions.
func TestShellDirectiveWithoutPayloadIsSkippedButSequenced(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, _ := testConfigFor(t, fp)
	queueDirectives(fp, map[string]any{"seq": 4, "op": enroll.OpShell})

	runLoopUntilDone(t, cfg, 300*time.Millisecond)
	select {
	case res := <-fp.results:
		t.Fatalf("a payload-less directive produced a result: %+v", res)
	case <-time.After(300 * time.Millisecond):
	}
	if got := loadDirectiveSeq(cfg); got != 4 {
		t.Fatalf("seq = %d, want 4", got)
	}
}

// A slow target must not hold back the instruction behind it: the uninstall
// in the same batch is acted on while the shell operation is still running.
func TestShellDirectiveDoesNotHoldBackUninstall(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, _ := testConfigFor(t, fp)
	shell := newFakeLegacyShell(t, 3*time.Second)
	queueDirectives(fp,
		shellDirective(t, 1, execTask("req-1", shell.srv.URL+"/s.jsp", time.Now().Add(time.Minute))),
		map[string]any{"seq": 2, "op": "uninstall"},
	)

	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- directiveLoop(ctx, cfg) }()
	select {
	case err := <-done:
		if !errors.Is(err, errUninstallRequested) {
			t.Fatalf("directiveLoop = %v, want errUninstallRequested", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a slow shell operation held back the uninstall directive")
	}
}

// The launcher runs a bounded number of operations at once and answers every
// one of them.
func TestShellTasksRunBoundedAndAllAnswer(t *testing.T) {
	fp := newFakePlatform(t)
	cfg, _ := testConfigFor(t, fp)
	shell := newFakeLegacyShell(t, 300*time.Millisecond)
	const tasks = shellConcurrency + 2
	notAfter := time.Now().Add(time.Minute)
	batch := make([]map[string]any, 0, tasks)
	seen := map[string]bool{}
	for i := 0; i < tasks; i++ {
		// Distinct paths keep the request ids apart.
		id := fmt.Sprintf("req-%d", i+1)
		task := execTask(id, shell.srv.URL+"/"+strings.Repeat("a", i+1)+".jsp", notAfter)
		seen[id] = false
		batch = append(batch, shellDirective(t, i+1, task))
	}
	queueDirectives(fp, batch...)

	runLoopUntilDone(t, cfg, 3*time.Second)
	for range tasks {
		res := waitResult(t, fp)
		if _, ok := seen[res.RequestID]; !ok || res.Error != "" {
			t.Fatalf("unexpected result %+v", res)
		}
		seen[res.RequestID] = true
	}
	for id, answered := range seen {
		if !answered {
			t.Fatalf("operation %s was never answered", id)
		}
	}
	if peak := shell.peakOpen(); peak > shellConcurrency {
		t.Fatalf("%d operations ran at once, want at most %d", peak, shellConcurrency)
	}
}
