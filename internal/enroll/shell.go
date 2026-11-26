package enroll

// Shell operations over the directive channel (docs/18): the platform queues
// one operation on a node, the node's embedded client runs it there — the
// traffic to the target leaves that host, not the console — and the node
// posts its answer to /node/result. Like the queues themselves the pairing
// lives in memory: a restart forgets what was in flight, and the caller sees a
// timeout rather than a silent retry.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/c1ayooo/4zwebshell/webshell"
)

// OpShell is the directive that carries a shell operation.
const OpShell = "shell"

const (
	// ShellWait is how long the platform waits for a node to answer. It is
	// also the operation's lifetime: a node that picks the directive up later
	// does not run it, because the caller has already been told it timed out.
	ShellWait = 75 * time.Second
	// ShellInFlightMax bounds the operations one node may hold at once, so a
	// stuck node cannot make the platform accumulate work forever.
	ShellInFlightMax = 16
)

var (
	// ErrShellOffline reports a node that cannot take the operation.
	ErrShellOffline = errors.New("节点不在线，无法执行 shell 操作")
	// ErrShellBusy reports a node already holding ShellInFlightMax operations.
	ErrShellBusy = errors.New("节点上的 shell 操作过多，请稍后再试")
	// ErrShellTimeout reports an operation nobody answered in time. It may
	// still have run.
	ErrShellTimeout = errors.New("节点未在限时内回执")
	// ErrNoSuchShellTask reports an answer nobody is waiting for.
	ErrNoSuchShellTask = errors.New("没有这个待回执的 shell 操作")
)

// ShellTask is one operation the platform wants a node to run against a
// deployed shell. Credential carries the plaintext key or password: it rides
// the authenticated ingress only, lives in the two processes' memory, and is
// never logged or persisted.
type ShellTask struct {
	RequestID  string              `json:"request_id"`
	URL        string              `json:"url"`
	Credential webshell.Credential `json:"credential"`
	Op         webshell.Op         `json:"op"`
	NotAfter   time.Time           `json:"not_after"`
}

// ShellResult is the node's answer to one ShellTask: the operation's result
// when it ran, or why it did not.
type ShellResult struct {
	RequestID string           `json:"request_id"`
	Result    *webshell.Result `json:"result,omitempty"`
	Error     string           `json:"error,omitempty"`
}

// RunShell queues one shell operation on a node and blocks until the node
// answers, the caller's deadline passes, or ShellWait elapses — whichever is
// first. An operation is never retried: one that is not answered is reported
// as a timeout, because it may well have run.
func (s *Service) RunShell(ctx context.Context, nodeID string, task ShellTask) (webshell.Result, error) {
	if err := task.Credential.Validate(); err != nil {
		return webshell.Result{}, err
	}
	if err := webshell.Validate(task.Op); err != nil {
		return webshell.Result{}, err
	}
	view, ok := s.NodeByID(nodeID)
	if !ok {
		return webshell.Result{}, ErrNoSuchNode
	}
	if !view.DispatchesOK {
		return webshell.Result{}, fmt.Errorf("%w（%s）", ErrShellOffline, view.State)
	}
	id, err := newID()
	if err != nil {
		return webshell.Result{}, err
	}
	task.RequestID, task.NotAfter = id, time.Now().Add(ShellWait)
	key := shellKey(nodeID, id)

	wait := time.NewTimer(ShellWait)
	defer wait.Stop()
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < ShellWait {
		wait.Reset(time.Until(deadline))
	}

	s.mu.Lock()
	if s.shellInFlight[nodeID] >= ShellInFlightMax {
		s.mu.Unlock()
		return webshell.Result{}, ErrShellBusy
	}
	answer := make(chan ShellResult, 1)
	s.shellPending[key] = answer
	s.shellInFlight[nodeID]++
	s.mu.Unlock()
	abandon := func() {
		s.mu.Lock()
		delete(s.shellPending, key)
		s.releaseShellLocked(nodeID)
		s.mu.Unlock()
	}

	if _, err := s.enqueue(nodeID, Directive{Op: OpShell, Shell: &task}); err != nil {
		abandon()
		return webshell.Result{}, err
	}
	select {
	case res := <-answer:
		if res.Result != nil && res.Error != "" {
			return *res.Result, errors.New(res.Error)
		}
		if res.Result == nil {
			if res.Error != "" {
				return webshell.Result{}, errors.New(res.Error)
			}
			return webshell.Result{}, fmt.Errorf("节点对 shell 操作 %s 的回执为空", task.Op.Kind)
		}
		return *res.Result, nil
	case <-wait.C:
		abandon()
		return webshell.Result{}, fmt.Errorf("%w（%s）", ErrShellTimeout, task.Op.Kind)
	case <-ctx.Done():
		abandon()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return webshell.Result{}, fmt.Errorf("%w（%s）", ErrShellTimeout, task.Op.Kind)
		}
		return webshell.Result{}, ctx.Err()
	}
}

// ShellDirectiveNode reports whether nodeID takes operations over the directive
// channel — i.e. it is a known launcher node. The API's shell dispatcher uses
// this to split local/direct nodes (command channel) from tunnel nodes.
func (s *Service) ShellDirectiveNode(nodeID string) bool {
	_, ok := s.NodeByID(nodeID)
	return ok
}

// SubmitShellResult hands a node's answer to the caller waiting on it. A node
// can only answer the operations it was given: an unknown id, or one queued
// on another node, is refused and leaves the real waiter untouched.
func (s *Service) SubmitShellResult(nodeID string, res ShellResult) error {
	key := shellKey(nodeID, res.RequestID)
	s.mu.Lock()
	defer s.mu.Unlock()
	answer := s.shellPending[key]
	if answer == nil {
		return ErrNoSuchShellTask
	}
	delete(s.shellPending, key)
	s.releaseShellLocked(nodeID)
	// Buffered: the caller may have timed out a moment ago.
	answer <- res
	return nil
}

func shellKey(nodeID, requestID string) string { return nodeID + "/" + requestID }

// releaseShellLocked gives back one in-flight slot. Call with s.mu held.
func (s *Service) releaseShellLocked(nodeID string) {
	if s.shellInFlight[nodeID] <= 1 {
		delete(s.shellInFlight, nodeID)
		return
	}
	s.shellInFlight[nodeID]--
}

// handleShellResult takes a node's answer to one shell operation. The body can
// carry a file chunk (base64), so it has more room than a heartbeat.
func (in *Ingress) handleShellResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	nodeID, status := in.authNode(w, r)
	if status != 0 {
		return
	}
	var res ShellResult
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&res); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if err := in.svc.SubmitShellResult(nodeID, res); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
