package host

// --no-local-node (docs/19 §2 纯控制面): the Hub runs, no scan node is
// launched, and readiness means "the Hub answers ListAgents" — there is no
// local agent to wait for. The tests drive the same seams as the liveness
// suite: no real binary, no real HTTP.

import (
	"context"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/aop"
)

func noLocalConfig(t *testing.T, dir string, logs *testLog) Config {
	t.Helper()
	return Config{
		Bin:              hostBin(t),
		Dir:              dir,
		NoLocalNode:      true,
		ReadyTimeout:     40 * time.Millisecond,
		RetryBackoff:     time.Millisecond,
		MaxReadyFailures: 3,
		Logf:             logs.Logf,
	}
}

func TestStartNoLocalNodeReadyWhenHubAnswers(t *testing.T) {
	// The Hub answers with zero agents: with a local node this exact answer is
	// "not ready" (the local agent is missing); hub-only it is ready.
	logs := &testLog{}
	agents := &testAgents{decide: func(int) ([]aop.Agent, error) { return nil, nil }}
	launcher := &testLauncher{}
	installSeams(t, launcher, agents)

	h, err := Start(context.Background(), noLocalConfig(t, t.TempDir(), logs))
	if err != nil {
		// An empty NodeBin must not even be resolved: the failure named below is
		// what a Start that still required a node produces.
		t.Fatalf("Start(NoLocalNode) = %v（纯控制面不起节点，不应要求 cyber-scan）", err)
	}
	defer h.Stop()

	if got := h.Agent(); got != "" {
		t.Fatalf("Host.Agent() = %q，纯控制面没有本地 agent，应为空", got)
	}
	if !h.Ready() {
		t.Fatal("Hub 应答即就绪，实际未就绪")
	}
	if st := h.Status(); st.NodePID != 0 {
		t.Fatalf("Status().NodePID = %d，纯控制面没有节点进程", st.NodePID)
	}
	if !logs.has("host_event=ready") {
		t.Fatalf("缺 host_event=ready，日志：\n%s", logs)
	}
}

func TestStartNoLocalNodeWaitsForHubAnswer(t *testing.T) {
	// The Hub does not answer at all: not ready, bounded retries, then the
	// failure that names the missing Hub — never a missing node.
	logs := &testLog{}
	agents := &testAgents{decide: func(int) ([]aop.Agent, error) {
		return nil, context.DeadlineExceeded
	}}
	launcher := &testLauncher{}
	installSeams(t, launcher, agents)

	_, err := Start(context.Background(), noLocalConfig(t, t.TempDir(), logs))
	if err == nil {
		t.Fatal("Hub 一直不应答，Start 应当失败")
	}
	if !logs.has("host_event=not_ready") {
		t.Fatalf("缺 host_event=not_ready，日志：\n%s", logs)
	}
}
