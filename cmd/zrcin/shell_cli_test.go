package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zwebshell/webshell"
)

// fakeCLIShell is a deployed legacy JSP shell for the CLI tests.
func fakeCLIShell(t *testing.T) (*httptest.Server, *sync.Mutex, func() url.Values) {
	t.Helper()
	var mu sync.Mutex
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		got = r.PostForm
		mu.Unlock()
		if r.PostForm.Get("pwd") != "s3cret" {
			_, _ = w.Write([]byte("usage"))
			return
		}
		_, _ = w.Write([]byte("uid=0(root)\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, &mu, func() url.Values { mu.Lock(); defer mu.Unlock(); return got }
}

func cliTask(t *testing.T, dir string, mutate func(*enroll.ShellTask)) string {
	t.Helper()
	task := enroll.ShellTask{
		RequestID:  "req-1",
		URL:        "http://target.example/s.jsp",
		Credential: webshell.Credential{Type: "jsp", Protocol: webshell.ShellProtocolLegacy, Password: "s3cret"},
		Op:         webshell.Op{Kind: webshell.OpExec, Command: "id"},
	}
	if mutate != nil {
		mutate(&task)
	}
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "task.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The happy path: the receipt is one JSON object with the operation's output,
// and the ticket is gone afterwards.
func TestRunShellCLIExecutesTheTaskAndBurnsTheTicket(t *testing.T) {
	shell, _, form := fakeCLIShell(t)
	taskFile := cliTask(t, t.TempDir(), func(task *enroll.ShellTask) { task.URL = shell.URL + "/s.jsp" })

	var stdout, stderr bytes.Buffer
	if code := runShellCLI(taskFile, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, stderr.String())
	}
	var receipt enroll.ShellResult
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("receipt %q: %v", stdout.String(), err)
	}
	if receipt.RequestID != "req-1" || receipt.Error != "" || receipt.Result == nil || receipt.Result.Output != "uid=0(root)\n" {
		t.Fatalf("receipt = %+v", receipt)
	}
	seen := form()
	if seen.Get("pwd") != "s3cret" || seen.Get("cmd") != "id" {
		t.Fatalf("the target saw %v", seen)
	}
	if _, err := os.Stat(taskFile); !os.IsNotExist(err) {
		t.Fatalf("the ticket survived the run: %v", err)
	}
	// The receipt never carries the credential.
	if strings.Contains(stdout.String(), "s3cret") {
		t.Fatal("the receipt leaks the credential")
	}
}

// A shell-level failure is still a receipt on stdout with exit 0 — and the
// ticket is burned before the operation runs, so a retry cannot replay it.
func TestRunShellCLIReportsAShellFailureAndStillBurnsTheTicket(t *testing.T) {
	taskFile := cliTask(t, t.TempDir(), func(task *enroll.ShellTask) {
		task.URL = "http://127.0.0.1:1/s.jsp" // nothing listens there
	})

	var stdout, stderr bytes.Buffer
	if code := runShellCLI(taskFile, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, stderr.String())
	}
	var receipt enroll.ShellResult
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("receipt %q: %v", stdout.String(), err)
	}
	if receipt.Error == "" || receipt.Result == nil {
		t.Fatalf("receipt = %+v, want a shell-level error", receipt)
	}
	if _, err := os.Stat(taskFile); !os.IsNotExist(err) {
		t.Fatalf("the ticket survived a failed run: %v", err)
	}
}

// A local failure (unreadable or undecodable task) exits non-zero without
// producing a receipt.
func TestRunShellCLIRejectsABadTask(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ name, body string }{
		{"missing", ""},
		{"garbage", "not json"},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name+".json")
		if c.body != "" {
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		if code := runShellCLI(path, &stdout, &stderr); code == 0 {
			t.Fatalf("%s: exit = 0, stdout %s", c.name, stdout.String())
		}
		// The reason reaches stdout too (the command channel may only capture
		// stdout), but it is plain text, not a receipt JSON.
		if !strings.Contains(stdout.String(), "error=") {
			t.Fatalf("%s: the reason never reached stdout: %q", c.name, stdout.String())
		}
		if strings.Contains(stdout.String(), "request_id") {
			t.Fatalf("%s: produced a receipt: %s", c.name, stdout.String())
		}
	}
}
