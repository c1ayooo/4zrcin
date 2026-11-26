package enroll

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/c1ayooo/4zwebshell/webshell"
)

// onlineNode returns a joined node whose last heartbeat is fresh.
func onlineNode(t *testing.T, svc *Service) string {
	t.Helper()
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1")
	if _, err := svc.Heartbeat(joined.NodeID, Report{Hostname: "edge"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	return joined.NodeID
}

func execTask() ShellTask {
	return ShellTask{
		URL:        "http://target.example/s.jsp",
		Credential: webshell.Credential{Type: "jsp", Protocol: webshell.ShellProtocolLegacy, Password: "s3cret"},
		Op:         webshell.Op{Kind: webshell.OpExec, Command: "id"},
	}
}

type shellOutcome struct {
	res webshell.Result
	err error
}

func startShell(ctx context.Context, svc *Service, nodeID string) <-chan shellOutcome {
	done := make(chan shellOutcome, 1)
	go func() {
		res, err := svc.RunShell(ctx, nodeID, execTask())
		done <- shellOutcome{res, err}
	}()
	return done
}

func shellHeld(svc *Service, nodeID string) (inFlight, pending int) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	return svc.shellInFlight[nodeID], len(svc.shellPending)
}

// The long-poll hands the node the operation with everything it needs; the
// node's answer, and only that node's answer, reaches the caller.
func TestRunShellPairsTheNodesAnswerWithTheCaller(t *testing.T) {
	svc, _, _ := newTestService(t)
	nodeID := onlineNode(t, svc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := startShell(ctx, svc, nodeID)

	delivered := svc.Next(nodeID, 5*time.Second)
	if len(delivered.Directives) != 1 {
		t.Fatalf("the long-poll woke with %d directives, want 1", len(delivered.Directives))
	}
	d := delivered.Directives[0]
	if d.Op != OpShell || d.Seq == 0 || d.Shell == nil {
		t.Fatalf("directive = %+v", d)
	}
	task := *d.Shell
	if task.RequestID == "" || task.URL != "http://target.example/s.jsp" || task.Op.Command != "id" ||
		task.Credential.Password != "s3cret" || task.Credential.Protocol != webshell.ShellProtocolLegacy {
		t.Fatalf("the node was handed %+v", task)
	}
	if remaining := time.Until(task.NotAfter); remaining <= 0 || remaining > ShellWait {
		t.Fatalf("the operation's lifetime is %s, want about %s", remaining, ShellWait)
	}

	// Another node cannot answer for it, and an unknown id is refused.
	foreign := webshell.Result{Output: "forged"}
	if err := svc.SubmitShellResult("another-node", ShellResult{RequestID: task.RequestID, Result: &foreign}); !errors.Is(err, ErrNoSuchShellTask) {
		t.Fatalf("a foreign node answered: %v", err)
	}
	if err := svc.SubmitShellResult(nodeID, ShellResult{RequestID: "unknown"}); !errors.Is(err, ErrNoSuchShellTask) {
		t.Fatalf("an unknown request was accepted: %v", err)
	}
	select {
	case got := <-done:
		t.Fatalf("the caller returned before its own node answered: %+v", got)
	default:
	}

	if err := svc.SubmitShellResult(nodeID, ShellResult{RequestID: task.RequestID, Result: &webshell.Result{Output: "uid=0"}}); err != nil {
		t.Fatalf("SubmitShellResult: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.res.Output != "uid=0" {
			t.Fatalf("caller got %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("the caller was not woken by its node's answer")
	}
	if inFlight, pending := shellHeld(svc, nodeID); inFlight != 0 || pending != 0 {
		t.Fatalf("an answered operation still holds a slot: in-flight %d, pending %d", inFlight, pending)
	}
}

// A node's failure comes back as the operation's error, with whatever the
// shell returned before failing.
func TestRunShellReturnsTheNodesFailure(t *testing.T) {
	svc, _, _ := newTestService(t)
	nodeID := onlineNode(t, svc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := startShell(ctx, svc, nodeID)
	task := *svc.Next(nodeID, 5*time.Second).Directives[0].Shell

	if err := svc.SubmitShellResult(nodeID, ShellResult{
		RequestID: task.RequestID, Result: &webshell.Result{Output: "partial"}, Error: "shell 返回 HTTP 500",
	}); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err == nil || got.err.Error() != "shell 返回 HTTP 500" || got.res.Output != "partial" {
		t.Fatalf("caller got %+v", got)
	}
}

// What cannot run is refused before anything is queued: an unknown or offline
// node, an operation no executor would accept.
func TestRunShellRefusesWhatCannotRun(t *testing.T) {
	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1") // joined, never reported: offline
	ctx := context.Background()

	if _, err := svc.RunShell(ctx, "00000000000000000000000000000000", execTask()); !errors.Is(err, ErrNoSuchNode) {
		t.Fatalf("unknown node = %v", err)
	}
	if _, err := svc.RunShell(ctx, joined.NodeID, execTask()); !errors.Is(err, ErrShellOffline) {
		t.Fatalf("offline node = %v", err)
	}
	noCommand := execTask()
	noCommand.Op = webshell.Op{Kind: webshell.OpExec}
	if _, err := svc.RunShell(ctx, joined.NodeID, noCommand); err == nil {
		t.Fatal("an operation with no command was queued")
	}
	noPassword := execTask()
	noPassword.Credential = webshell.Credential{Type: "jsp", Protocol: webshell.ShellProtocolLegacy}
	if _, err := svc.RunShell(ctx, joined.NodeID, noPassword); err == nil {
		t.Fatal("a credential with no password was queued")
	}
	if inFlight, pending := shellHeld(svc, joined.NodeID); inFlight != 0 || pending != 0 {
		t.Fatalf("a refused operation left state behind: in-flight %d, pending %d", inFlight, pending)
	}
}

// An operation nobody answers is reported, not retried: it is handed to the
// node once, the caller's own deadline is honoured, and a late answer finds
// nobody waiting.
func TestRunShellTimesOutWithoutRetrying(t *testing.T) {
	svc, _, _ := newTestService(t)
	nodeID := onlineNode(t, svc)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := svc.RunShell(ctx, nodeID, execTask())
	if !errors.Is(err, ErrShellTimeout) {
		t.Fatalf("unanswered operation = %v, want a timeout", err)
	}
	if waited := time.Since(started); waited > 3*time.Second {
		t.Fatalf("the caller's 200ms deadline was ignored: waited %s", waited)
	}
	if inFlight, pending := shellHeld(svc, nodeID); inFlight != 0 || pending != 0 {
		t.Fatalf("an abandoned operation still holds a slot: in-flight %d, pending %d", inFlight, pending)
	}

	delivered := svc.Next(nodeID, 0)
	if len(delivered.Directives) != 1 {
		t.Fatalf("the operation was handed out %d times, want exactly once", len(delivered.Directives))
	}
	if again := svc.Next(nodeID, 0); len(again.Directives) != 0 {
		t.Fatalf("a timed-out operation was re-queued: %+v", again.Directives)
	}
	late := ShellResult{RequestID: delivered.Directives[0].Shell.RequestID, Result: &webshell.Result{Output: "too late"}}
	if err := svc.SubmitShellResult(nodeID, late); !errors.Is(err, ErrNoSuchShellTask) {
		t.Fatalf("a late answer was accepted: %v", err)
	}
}

// A stuck node must not make the platform accumulate work without bound.
func TestRunShellBoundsWhatOneNodeHolds(t *testing.T) {
	svc, _, _ := newTestService(t)
	nodeID := onlineNode(t, svc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	held := make(chan shellOutcome, ShellInFlightMax)
	for i := 0; i < ShellInFlightMax; i++ {
		go func() {
			res, err := svc.RunShell(ctx, nodeID, execTask())
			held <- shellOutcome{res, err}
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		inFlight, _ := shellHeld(svc, nodeID)
		if inFlight == ShellInFlightMax {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d operations registered", inFlight)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Every slot is held and nobody has answered: one more is refused at once.
	quick, cancelQuick := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelQuick()
	if _, err := svc.RunShell(quick, nodeID, execTask()); !errors.Is(err, ErrShellBusy) {
		t.Fatalf("an operation past the cap = %v, want busy", err)
	}

	// Answering every operation gives the slots back.
	for _, d := range svc.Next(nodeID, 0).Directives {
		if err := svc.SubmitShellResult(nodeID, ShellResult{RequestID: d.Shell.RequestID, Result: &webshell.Result{}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < ShellInFlightMax; i++ {
		if got := <-held; got.err != nil {
			t.Fatalf("a held operation came back as %v", got.err)
		}
	}
	if inFlight, _ := shellHeld(svc, nodeID); inFlight != 0 {
		t.Fatalf("answered operations still hold %d slots", inFlight)
	}
}

// /node/result is authenticated like the rest of the ingress and answers 404
// for a result nobody is waiting for, so the node stops redelivering it.
func TestIngressShellResult(t *testing.T) {
	in, svc, _ := newTestIngress(t)
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1")
	if _, err := svc.Heartbeat(joined.NodeID, Report{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := startShell(ctx, svc, joined.NodeID)
	task := *svc.Next(joined.NodeID, 5*time.Second).Directives[0].Shell

	if rec := doJSON(t, in, http.MethodPost, "/node/result", "", ShellResult{RequestID: task.RequestID}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a result without a credential = %d", rec.Code)
	}
	if rec := doJSON(t, in, http.MethodPost, "/node/result", joined.NodeCredential, ShellResult{RequestID: "unknown"}); rec.Code != http.StatusNotFound {
		t.Fatalf("a result nobody waits for = %d %s", rec.Code, rec.Body)
	}
	select {
	case got := <-done:
		t.Fatalf("the caller returned on a refused result: %+v", got)
	default:
	}

	answer := ShellResult{RequestID: task.RequestID, Result: &webshell.Result{Output: "uid=0"}}
	rec := doJSON(t, in, http.MethodPost, "/node/result", joined.NodeCredential, answer)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("a valid result = %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	select {
	case got := <-done:
		if got.err != nil || got.res.Output != "uid=0" {
			t.Fatalf("caller got %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("the caller was not woken by the posted result")
	}
}
