package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zwebshell/webshell"
)

// fakeShellRunner stands in for the enrollment service: it records what would
// have been queued on a node and answers however the test needs.
type fakeShellRunner struct {
	mu     sync.Mutex
	tasks  []enroll.ShellTask
	nodes  []string
	called int
	reply  func(task enroll.ShellTask) (webshell.Result, error)
}

func (f *fakeShellRunner) ShellDirectiveNode(nodeID string) bool {
	// The fixture's tunnel nodes; lab is a direct AOP node, local is the
	// console's own host, anything else does not exist.
	switch nodeID {
	case "node-1", "node-2", "node-9", "n":
		return true
	}
	return false
}

func (f *fakeShellRunner) RunShell(_ context.Context, nodeID string, task enroll.ShellTask) (webshell.Result, error) {
	f.mu.Lock()
	f.tasks, f.nodes = append(f.tasks, task), append(f.nodes, nodeID)
	f.called++
	reply := f.reply
	f.mu.Unlock()
	if reply != nil {
		return reply(task)
	}
	return webshell.Result{}, nil
}

func (f *fakeShellRunner) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called
}

func shellServer(t *testing.T, reply func(enroll.ShellTask) (webshell.Result, error)) (*Server, *fakeShellRunner) {
	t.Helper()
	s, _ := newBindingServer(t)
	s.ShellSealer = webshell.NewSealer([]byte("instance-secret"))
	runner := &fakeShellRunner{reply: reply}
	s.ShellRunner = runner
	return s, runner
}

type registration struct {
	Shell   shellView     `json:"shell"`
	Payload *shellPayload `json:"payload"`
}

func mustRegisterShell(t *testing.T, s *Server, body map[string]any) registration {
	t.Helper()
	rec := call(t, s, http.MethodPost, "/api/shells", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %v = %d %s", body, rec.Code, rec.Body)
	}
	var out registration
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A minted registration hands the operator the file to deploy once and keeps
// only the sealed credential: the response never carries the sealed blob, and
// the key it does carry is the one baked into the file.
func TestShellRegisterMintsTheFileAndKeepsOnlyTheSealedKey(t *testing.T) {
	s, _ := shellServer(t, nil)

	out := mustRegisterShell(t, s, map[string]any{"url": "http://target.example/shell.php", "node_id": "node-1", "name": "edge shell"})
	rec := call(t, s, http.MethodPost, "/api/shells", map[string]any{"url": "http://target.example/other.php"})
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a registration response is cacheable")
	}
	sh := out.Shell
	if sh.Status != model.ShellStatusUnknown || sh.Protocol != model.ShellProtocolV2 || sh.Type != model.ShellTypePHP ||
		sh.NodeID != "node-1" || sh.Source != model.ShellSourceOperator || sh.Target != sh.URL {
		t.Fatalf("registered = %+v", sh)
	}
	if out.Payload == nil || out.Payload.Filename != "shell.php" || out.Payload.Content == "" || len(out.Payload.SHA256) != 64 {
		t.Fatalf("payload = %+v", out.Payload)
	}

	row, ok := s.Store.GetShell(sh.ID)
	if !ok || row.Secret == "" || row.Carrier == "" || row.PayloadSHA256 != out.Payload.SHA256 {
		t.Fatalf("stored row = %+v", row)
	}
	cred, err := openCredential(s.ShellSealer, row)
	if err != nil || len(cred.Key) != 32 || cred.Carrier != row.Carrier {
		t.Fatalf("the sealed credential does not open: %+v, %v", cred, err)
	}
	if !strings.Contains(out.Payload.Content, base64.StdEncoding.EncodeToString(cred.Key)) {
		t.Fatal("the deployed file does not carry the sealed key")
	}
}

// A shell already on the target is adopted with what the operator knows about
// it; nothing is minted, and incomplete material is refused.
func TestShellRegisterAdoptsAShellAlreadyOnTheTarget(t *testing.T) {
	s, _ := shellServer(t, nil)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	v2 := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/s.jsp", "type": "JSP", "key": webshell.EncodeShellKey(key), "carrier": "X-abcdef0123",
	})
	if v2.Payload != nil || v2.Shell.Type != model.ShellTypeJSP || v2.Shell.PayloadSHA256 != "" || v2.Shell.Name != "shell.jsp" {
		t.Fatalf("adopted v2 = %+v", v2)
	}
	row, _ := s.Store.GetShell(v2.Shell.ID)
	if cred, err := openCredential(s.ShellSealer, row); err != nil || string(cred.Key) != string(key) {
		t.Fatalf("adopted key = %x, %v", cred.Key, err)
	}

	legacy := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/old.php", "protocol": "legacy", "password": "hunter2",
	})
	if legacy.Payload != nil || legacy.Shell.Protocol != model.ShellProtocolLegacy {
		t.Fatalf("adopted legacy = %+v", legacy)
	}
	row, _ = s.Store.GetShell(legacy.Shell.ID)
	if strings.Contains(row.Secret, "hunter2") {
		t.Fatal("a legacy password is stored in the clear")
	}
	if cred, err := openCredential(s.ShellSealer, row); err != nil || cred.Password != "hunter2" {
		t.Fatalf("adopted password = %q, %v", cred.Password, err)
	}

	refused := []map[string]any{
		{"url": "http://a.example/s.jsp", "type": "jsp", "key": webshell.EncodeShellKey(key)}, // no carrier
		{"url": "http://a.example/old.php", "protocol": "legacy"},                             // legacy is adopt-only
		{"url": "http://a.example/x", "protocol": "v3"},
		{"url": "http://a.example/x", "type": "python"},
		{"url": "ftp://a.example/x"},
		{"url": "http://target.example/old.php", "protocol": "legacy", "password": "other"}, // address taken
	}
	for i, body := range refused {
		if rec := call(t, s, http.MethodPost, "/api/shells", body); rec.Code != http.StatusBadRequest && rec.Code != http.StatusConflict {
			t.Fatalf("case %d: %v = %d %s", i, body, rec.Code, rec.Body)
		}
	}
	if rec := call(t, s, http.MethodPost, "/api/shells", refused[len(refused)-1]); rec.Code != http.StatusConflict {
		t.Fatalf("a second live row for one address = %d", rec.Code)
	}
	if rows := s.Store.ListShells(); len(rows) != 2 {
		t.Fatalf("a refused registration landed anyway: %d rows", len(rows))
	}
}

// Without an instance secret there is nothing to seal with, and registering
// under a made-up key would be worse than refusing.
func TestShellRegisterNeedsASealer(t *testing.T) {
	s, _ := newBindingServer(t)
	s.ShellRunner = &fakeShellRunner{}
	if rec := call(t, s, http.MethodPost, "/api/shells", map[string]any{"url": "http://target.example/shell.php"}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("register without a sealer = %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, http.MethodGet, "/api/shells", nil); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"shells":[]}` {
		t.Fatalf("empty table = %d %s", rec.Code, rec.Body)
	}
}

// No view carries the credential: neither the plaintext nor the sealed blob,
// and not by another name either.
func TestShellViewsNeverCarryTheCredential(t *testing.T) {
	s, _ := shellServer(t, nil)
	out := mustRegisterShell(t, s, map[string]any{"url": "http://target.example/old.php", "protocol": "legacy", "password": "hunter2", "node_id": "n"})
	row, _ := s.Store.GetShell(out.Shell.ID)

	responses := map[string]*httptest.ResponseRecorder{
		"list":   call(t, s, http.MethodGet, "/api/shells", nil),
		"update": call(t, s, http.MethodPut, "/api/shells/"+row.ID, map[string]any{"note": "n2"}),
		"op":     call(t, s, http.MethodPost, "/api/shells/"+row.ID+"/op", map[string]any{"op": map[string]any{"kind": "ping"}}),
	}
	for name, rec := range responses {
		body := rec.Body.String()
		if strings.Contains(body, "hunter2") || (row.Secret != "" && strings.Contains(body, row.Secret)) {
			t.Fatalf("the %s response carries the credential: %s", name, body)
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
			continue // an error object is not a view
		}
		if _, ok := probe["secret"]; ok {
			t.Fatalf("the %s response has a secret field", name)
		}
	}
}

func TestShellUpdateAndForget(t *testing.T) {
	s, _ := shellServer(t, nil)
	out := mustRegisterShell(t, s, map[string]any{"url": "http://target.example/shell.php", "note": "first"})

	rec := call(t, s, http.MethodPut, "/api/shells/"+out.Shell.ID, map[string]any{"name": "renamed", "node_id": "node-2", "note": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	row, _ := s.Store.GetShell(out.Shell.ID)
	if row.Name != "renamed" || row.NodeID != "node-2" || row.Note != "" {
		t.Fatalf("updated row = %+v", row)
	}
	if rec := call(t, s, http.MethodPut, "/api/shells/sh_missing", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("updating an unknown shell = %d", rec.Code)
	}
	if rec := call(t, s, http.MethodDelete, "/api/shells/"+out.Shell.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("forget = %d %s", rec.Code, rec.Body)
	}
	if _, ok := s.Store.GetShell(out.Shell.ID); ok {
		t.Fatal("a forgotten shell is still in the ledger")
	}
	if rec := call(t, s, http.MethodDelete, "/api/shells/"+out.Shell.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("forgetting twice = %d", rec.Code)
	}
}

// The operation runs on the shell's node with the unsealed credential, and the
// ledger keeps what the operation proved.
func TestShellOpRunsOnTheBoundNodeAndRecordsWhatItProved(t *testing.T) {
	s, runner := shellServer(t, func(enroll.ShellTask) (webshell.Result, error) {
		return webshell.Result{Output: strings.Repeat("o", 3*model.MaxShellOutput)}, nil
	})
	out := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/old.php", "protocol": "legacy", "password": "hunter2", "node_id": "node-1",
	})
	_ = out

	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "exec", "command": "id"}})
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("exec = %d %s", rec.Code, rec.Body)
	}
	task, node := runner.tasks[0], runner.nodes[0]
	if node != "node-1" || task.URL != "http://target.example/old.php" || task.Op.Command != "id" ||
		task.Credential.Password != "hunter2" || task.Credential.Protocol != webshell.ShellProtocolLegacy {
		t.Fatalf("queued on %s: %+v", node, task)
	}
	row, _ := s.Store.GetShell(out.Shell.ID)
	if row.Status != model.ShellStatusAlive || row.LastChecked == nil || len(row.LastOutput) > model.MaxShellOutput {
		t.Fatalf("ledger after a successful exec = %+v", row)
	}

	// A ping decides liveness both ways, and a request may name another node.
	runner.reply = func(enroll.ShellTask) (webshell.Result, error) { return webshell.Result{Alive: false}, nil }
	rec = call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"node_id": "node-2", "op": map[string]any{"kind": "ping"}})
	if rec.Code != http.StatusOK || runner.nodes[len(runner.nodes)-1] != "node-2" {
		t.Fatalf("ping = %d %s", rec.Code, rec.Body)
	}
	if row, _ = s.Store.GetShell(out.Shell.ID); row.Status != model.ShellStatusDead {
		t.Fatalf("a dead ping left the status at %s", row.Status)
	}
}

// A failed command says nothing about liveness; its output is still kept.
func TestShellOpKeepsAFailedExecOutputWithoutJudgingLiveness(t *testing.T) {
	s, _ := shellServer(t, func(enroll.ShellTask) (webshell.Result, error) {
		return webshell.Result{Output: "sh: id: not found"}, errors.New("shell 返回 HTTP 500")
	})
	out := mustRegisterShell(t, s, map[string]any{"url": "http://target.example/shell.php", "node_id": "node-1"})

	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "exec", "command": "id"}})
	var body struct {
		Result webshell.Result `json:"result"`
		Error  string          `json:"error"`
		Shell  shellView       `json:"shell"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("exec = %d %s (%v)", rec.Code, rec.Body, err)
	}
	if body.Error != "shell 返回 HTTP 500" || body.Result.Output != "sh: id: not found" {
		t.Fatalf("response = %+v", body)
	}
	if row, _ := s.Store.GetShell(out.Shell.ID); row.Status != model.ShellStatusUnknown || row.LastOutput != "sh: id: not found" {
		t.Fatalf("a failed exec changed liveness: %+v", row)
	}
}

func TestShellRemoveRecordsWhatTheVerifyProbeSaid(t *testing.T) {
	var reply func(enroll.ShellTask) (webshell.Result, error)
	s, _ := shellServer(t, func(task enroll.ShellTask) (webshell.Result, error) { return reply(task) })
	op := map[string]any{"op": map[string]any{"kind": "remove"}}

	stillThere := mustRegisterShell(t, s, map[string]any{"url": "http://a.example/shell.php", "node_id": "n"})
	reply = func(enroll.ShellTask) (webshell.Result, error) {
		return webshell.Result{Message: "shell 仍响应协议请求"}, nil
	}
	call(t, s, http.MethodPost, "/api/shells/"+stillThere.Shell.ID+"/op", op)
	if row, _ := s.Store.GetShell(stillThere.Shell.ID); row.Status != model.ShellStatusUnknown {
		t.Fatalf("a shell that still answers reads %s", row.Status)
	}

	failed := mustRegisterShell(t, s, map[string]any{"url": "http://b.example/shell.php", "node_id": "n"})
	reply = func(enroll.ShellTask) (webshell.Result, error) { return webshell.Result{}, errors.New("boom") }
	call(t, s, http.MethodPost, "/api/shells/"+failed.Shell.ID+"/op", op)
	if row, _ := s.Store.GetShell(failed.Shell.ID); row.Status != model.ShellStatusRemoveFailed {
		t.Fatalf("a failed removal reads %s", row.Status)
	}

	gone := mustRegisterShell(t, s, map[string]any{"url": "http://c.example/shell.php", "node_id": "n"})
	reply = func(enroll.ShellTask) (webshell.Result, error) { return webshell.Result{Removed: true}, nil }
	call(t, s, http.MethodPost, "/api/shells/"+gone.Shell.ID+"/op", op)
	if row, _ := s.Store.GetShell(gone.Shell.ID); row.Status != model.ShellStatusRemoved {
		t.Fatalf("a verified removal reads %s", row.Status)
	}
	// A removed shell frees its address for a new deployment.
	again := mustRegisterShell(t, s, map[string]any{"url": "http://c.example/shell.php", "node_id": "n"})
	if again.Shell.ID == gone.Shell.ID {
		t.Fatal("the new deployment reused the removed shell's row")
	}
}

// A request no node took is an error, and it must leave the ledger exactly as
// it was: an operation that never ran proves nothing.
func TestShellOpRefusalsLeaveTheLedgerAlone(t *testing.T) {
	var reply func(enroll.ShellTask) (webshell.Result, error)
	s, runner := shellServer(t, func(task enroll.ShellTask) (webshell.Result, error) { return reply(task) })
	bound := mustRegisterShell(t, s, map[string]any{"url": "http://a.example/shell.php", "node_id": "n"})
	unbound := mustRegisterShell(t, s, map[string]any{"url": "http://b.example/shell.php"})
	op := map[string]any{"op": map[string]any{"kind": "ping"}}
	boundPath, unboundPath := "/api/shells/"+bound.Shell.ID+"/op", "/api/shells/"+unbound.Shell.ID+"/op"
	before, _ := s.Store.GetShell(bound.Shell.ID)

	refusals := []struct {
		path string
		body any
		code int
	}{
		{"/api/shells/sh_missing/op", op, http.StatusNotFound},
		{boundPath, map[string]any{"op": map[string]any{"kind": "exec"}}, http.StatusBadRequest},
		{boundPath, map[string]any{"op": map[string]any{"kind": "detonate"}}, http.StatusBadRequest},
		{unboundPath, op, http.StatusBadRequest},
	}
	for i, c := range refusals {
		if rec := call(t, s, http.MethodPost, c.path, c.body); rec.Code != c.code {
			t.Fatalf("case %d: %s %v = %d %s", i, c.path, c.body, rec.Code, rec.Body)
		}
	}
	if runner.calls() != 0 {
		t.Fatalf("a refused request reached a node %d times", runner.calls())
	}

	for _, err := range []error{
		enroll.ErrShellOffline, enroll.ErrShellBusy, enroll.ErrShellTimeout,
		fmt.Errorf("%w（ping）", enroll.ErrShellTimeout),
	} {
		reply = func(enroll.ShellTask) (webshell.Result, error) { return webshell.Result{}, err }
		if rec := call(t, s, http.MethodPost, boundPath, op); rec.Code != http.StatusBadGateway {
			t.Fatalf("%v answered %d %s", err, rec.Code, rec.Body)
		}
	}
	if row, _ := s.Store.GetShell(bound.Shell.ID); row != before {
		t.Fatalf("an operation no node ran changed the row: %+v", row)
	}
}

// Without an enrollment service there is no node to run on; the route says so
// instead of running the operation on the console.
func TestShellOpWithoutARunnerRefuses(t *testing.T) {
	s, runner := shellServer(t, nil)
	_ = runner
	s.ShellRunner = nil
	out := mustRegisterShell(t, s, map[string]any{"url": "http://a.example/shell.php", "node_id": "n"})
	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "ping"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "节点 n 不存在") {
		t.Fatalf("op without a runner = %d %s", rec.Code, rec.Body)
	}
	if row, _ := s.Store.GetShell(out.Shell.ID); row.Status != model.ShellStatusUnknown {
		t.Fatalf("the ledger changed: %+v", row)
	}
}

// The credential route hands the harness tools the plaintext connection
// material of the shell they are about to run, and nothing else.
func TestShellCredentialHandsOutTheConnectionMaterial(t *testing.T) {
	s, _ := shellServer(t, nil)
	out := mustRegisterShell(t, s, map[string]any{"url": "http://target.example/old.php", "protocol": "legacy", "password": "hunter2"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/shells/{id}/credential", s.shellCredential)
	rec := callMux(t, mux, http.MethodGet, "/api/shells/"+out.Shell.ID+"/credential", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("credential = %d %s", rec.Code, rec.Body)
	}
	var cred webshell.Credential
	if err := json.Unmarshal(rec.Body.Bytes(), &cred); err != nil || cred.Password != "hunter2" {
		t.Fatalf("credential = %s (%v)", rec.Body, err)
	}
	if rec := callMux(t, mux, http.MethodGet, "/api/shells/sh_missing/credential", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown shell's credential = %d", rec.Code)
	}
	// A rotated instance secret cannot open the stored credential: the route
	// says so instead of handing out garbage.
	s.ShellSealer = webshell.NewSealer([]byte("rotated-secret"))
	if rec := callMux(t, mux, http.MethodGet, "/api/shells/"+out.Shell.ID+"/credential", nil); rec.Code != http.StatusConflict {
		t.Fatalf("an unopenable credential = %d %s", rec.Code, rec.Body)
	}
}

// callMux routes one request through a mux the test built itself, for a
// handler that is not on the operator surface.
func callMux(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	return rec
}

// ---- local vantage (B route) ----

// fakeShellHost plays the AOP host for real enough: it opens sessions, fails
// the stale ones, and answers a command by reading the task file the console
// wrote and echoing the receipt the executor would print.
type fakeShellHost struct {
	mu        sync.Mutex
	dials     int
	opened    []string
	lines     []string
	watchFail string
}

func (f *fakeShellHost) NodeID(context.Context, string) (string, error) { return "agent-1", nil }

func (f *fakeShellHost) OpenSession(_ context.Context, _, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, "sess")
	return fmt.Sprintf("sess-%d", len(f.opened)), nil
}

func (f *fakeShellHost) Watch(_ context.Context, sessionID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sessionID == f.watchFail {
		return errors.New("session not found")
	}
	return nil
}

func (f *fakeShellHost) Command(_ context.Context, _, line string) (string, error) {
	f.mu.Lock()
	f.lines = append(f.lines, line)
	f.mu.Unlock()
	start := strings.Index(line, "--task '")
	if start < 0 {
		return "", fmt.Errorf("command without a task: %s", line)
	}
	path := line[start+len("--task '") : strings.LastIndex(line, "'")]
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var task enroll.ShellTask
	if err := json.Unmarshal(raw, &task); err != nil {
		return "", err
	}
	receipt := enroll.ShellResult{RequestID: task.RequestID, Result: &webshell.Result{Output: "uid=0(fake)"}}
	out, _ := json.Marshal(receipt)
	return string(out), nil
}

func (f *fakeShellHost) Cursor() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprintf("c%d", len(f.lines))
}

func (f *fakeShellHost) CloseSession(context.Context, string, string) error { return nil }
func (f *fakeShellHost) Close() error                                       { return nil }

func (f *fakeShellHost) stats() (dials int, lines []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dials, append([]string(nil), f.lines...)
}

func stubShellHost(t *testing.T, host *fakeShellHost) {
	t.Helper()
	previous := newShellSessionHost
	newShellSessionHost = func(context.Context, string, string) (shellSessionHost, error) {
		host.mu.Lock()
		host.dials++
		host.mu.Unlock()
		return host, nil
	}
	t.Cleanup(func() { newShellSessionHost = previous })
}

// An unbound shell defaults to the console's own host: the operation goes out
// as a "!zrcin shell run" command on the control session, the executor reads
// the task file, and the ticket is burned afterwards.
func TestShellOpOnTheLocalNodeRunsThroughTheCommandChannel(t *testing.T) {
	host := &fakeShellHost{}
	stubShellHost(t, host)
	s, runner := shellServer(t, nil)
	s.LocalNode = "local"
	out := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/old.php", "protocol": "legacy", "password": "hunter2",
	})

	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "exec", "command": "id"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("op = %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Result webshell.Result `json:"result"`
		Shell  shellView       `json:"shell"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Result.Output != "uid=0(fake)" {
		t.Fatalf("result = %+v", body.Result)
	}
	if row, _ := s.Store.GetShell(out.Shell.ID); row.Status != model.ShellStatusAlive || row.LastOutput != "uid=0(fake)" {
		t.Fatalf("ledger = %+v", row)
	}
	dials, lines := host.stats()
	if dials != 1 || len(lines) != 1 {
		t.Fatalf("dials %d, lines %d", dials, len(lines))
	}
	if !strings.Contains(lines[0], " shell run --task '") || !strings.HasPrefix(lines[0], "!") {
		t.Fatalf("line = %q", lines[0])
	}
	// The ticket is gone after the command.
	start := strings.Index(lines[0], "--task '")
	path := lines[0][start+len("--task '") : strings.LastIndex(lines[0], "'")]
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the task file survived: %v", err)
	}
	// The control session persists for the next call.
	var control struct {
		Session string `json:"session"`
		Cursor  string `json:"cursor"`
	}
	raw, err := os.ReadFile(filepath.Join(s.CaseRoot, ".shells", "control-session-local.json"))
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	if err := json.Unmarshal(raw, &control); err != nil || control.Session == "" || control.Cursor == "" {
		t.Fatalf("marker = %s (%v)", raw, err)
	}
	if runner.calls() != 0 {
		t.Fatal("the directive channel was engaged for a local shell")
	}
}

// A stale control session (the host restarted under a new id) is reopened once
// and the fresh session takes over the marker.
func TestShellOpReopensAStaleControlSession(t *testing.T) {
	host := &fakeShellHost{watchFail: "stale"}
	stubShellHost(t, host)
	s, _ := shellServer(t, nil)
	s.LocalNode = "local"
	if err := os.MkdirAll(filepath.Join(s.CaseRoot, ".shells"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.CaseRoot, ".shells", "control-session-local.json"),
		[]byte(`{"endpoint":"http://old","session":"stale","cursor":"c9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/shell.php", "node_id": "",
	})

	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "ping"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("op = %d %s", rec.Code, rec.Body)
	}
	dials, _ := host.stats()
	if dials != 2 {
		t.Fatalf("dials = %d, want the stale attempt and the fresh one", dials)
	}
	var control struct {
		Session string `json:"session"`
		Cursor  string `json:"cursor"`
	}
	raw, _ := os.ReadFile(filepath.Join(s.CaseRoot, ".shells", "control-session-local.json"))
	_ = json.Unmarshal(raw, &control)
	if control.Session == "stale" || control.Session == "" {
		t.Fatalf("marker session = %q", control.Session)
	}
}

// A command the host did not confirm is final: no retry, the ledger stays as
// it was, the ticket is burned and the stale session is dropped.
func TestShellOpLocalTransportFailureLeavesTheLedgerAlone(t *testing.T) {
	host := &fakeShellHost{}
	stubShellHost(t, host)
	s, _ := shellServer(t, nil)
	s.LocalNode = "local"
	out := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/shell.php", "node_id": "",
	})
	previous := newShellSessionHost
	newShellSessionHost = func(ctx context.Context, base, token string) (shellSessionHost, error) {
		host.mu.Lock()
		host.dials++
		host.mu.Unlock()
		return failingHost{host}, nil
	}
	t.Cleanup(func() { newShellSessionHost = previous })

	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "ping"}})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("op = %d %s", rec.Code, rec.Body)
	}
	if row, _ := s.Store.GetShell(out.Shell.ID); row.Status != model.ShellStatusUnknown || row.LastChecked != nil {
		t.Fatalf("ledger changed on a transport failure: %+v", row)
	}
	if _, err := os.Stat(filepath.Join(s.CaseRoot, ".shells", "control-session-local.json")); !os.IsNotExist(err) {
		t.Fatalf("a failed session was kept: %v", err)
	}
}

type failingHost struct{ *fakeShellHost }

func (failingHost) Command(context.Context, string, string) (string, error) {
	return "", errors.New("宿主断开")
}

// A shell bound to a remote node still goes through the directive channel; the
// local host is never dialed for it.
func TestShellOpDirectivePathForTunnelNodes(t *testing.T) {
	host := &fakeShellHost{}
	stubShellHost(t, host)
	var reply func(enroll.ShellTask) (webshell.Result, error)
	s, runner := shellServer(t, func(task enroll.ShellTask) (webshell.Result, error) { return reply(task) })
	s.LocalNode = "local"
	reply = func(enroll.ShellTask) (webshell.Result, error) { return webshell.Result{Alive: true}, nil }
	out := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/shell.php", "node_id": "node-9",
	})
	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "ping"}})
	if rec.Code != http.StatusOK || runner.calls() != 1 || runner.nodes[0] != "node-9" {
		t.Fatalf("op = %d, runner %+v", rec.Code, runner.nodes)
	}
	if dials, _ := host.stats(); dials != 0 {
		t.Fatalf("the local host was dialed %d times", dials)
	}
}

// A direct AOP node is a valid vantage too: the same command channel, its own
// control session, and the directive channel stays out of it.
func TestShellOpOnADirectNodeRunsThroughItsOwnCommandChannel(t *testing.T) {
	host := &fakeShellHost{}
	stubShellHost(t, host)
	s, runner := shellServer(t, nil)
	s.LocalNode = "local"
	out := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/shell.php", "node_id": "lab",
	})

	if bound := call(t, s, http.MethodPut, "/api/nodes", map[string]any{"nodes": []string{"local", "lab"}, "default": "local"}); bound.Code != http.StatusOK {
		t.Fatalf("bind nodes = %d %s", bound.Code, bound.Body)
	}
	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "ping"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("op = %d %s", rec.Code, rec.Body)
	}
	dials, lines := host.stats()
	if dials != 1 || len(lines) != 1 {
		t.Fatalf("dials %d, lines %d", dials, len(lines))
	}
	var control struct {
		Session string `json:"session"`
	}
	raw, err := os.ReadFile(filepath.Join(s.CaseRoot, ".shells", "control-session-lab.json"))
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	_ = json.Unmarshal(raw, &control)
	if control.Session == "" {
		t.Fatal("the direct node has no control session")
	}
	if _, err := os.Stat(filepath.Join(s.CaseRoot, ".shells", "control-session-local.json")); !os.IsNotExist(err) {
		t.Fatalf("the local host was engaged for a direct node: %v", err)
	}
	if runner.calls() != 0 {
		t.Fatal("the directive channel was engaged for a direct node")
	}
}

// A node that is neither local, nor a tunnel node, nor in the registry is
// refused before anything is queued.
func TestShellOpRefusesAnUnknownNode(t *testing.T) {
	host := &fakeShellHost{}
	stubShellHost(t, host)
	s, runner := shellServer(t, nil)
	s.LocalNode = "local"
	out := mustRegisterShell(t, s, map[string]any{
		"url": "http://target.example/shell.php", "node_id": "ghost",
	})
	rec := call(t, s, http.MethodPost, "/api/shells/"+out.Shell.ID+"/op", map[string]any{"op": map[string]any{"kind": "ping"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "节点 ghost 不存在") {
		t.Fatalf("unknown node = %d %s", rec.Code, rec.Body)
	}
	if dials, _ := host.stats(); dials != 0 || runner.calls() != 0 {
		t.Fatalf("an unknown node reached an executor: dials %d, runner %d", dials, runner.calls())
	}
}
