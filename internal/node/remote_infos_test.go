package node

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// A discovered tunnel node carries its enrollment profile into the node info
// the console renders, and the router refuses it before any session work —
// the two places docs/14 §7.3 puts the gate.

func newRemoteFixture(t *testing.T) *Registry {
	t.Helper()
	reg, err := New([]Spec{{ID: "local", AOP: "http://127.0.0.1:1", Token: "tok"}}, "local")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reg.SetLocalID("local")
	// A discovered agent as RefreshNodes would have recorded it: id prefixed,
	// Agent holding the node id the enrollment issued.
	reg.discovered = map[string]Spec{
		"scan_0123456789abcdef0123456789abcdef": {
			ID: "scan_0123456789abcdef0123456789abcdef", AOP: "http://127.0.0.1:1", Token: "tok",
			Agent: "0123456789abcdef0123456789abcdef",
		},
	}
	reg.discoveryInfo = map[string]executor.NodeInfo{
		"scan_0123456789abcdef0123456789abcdef": {
			ID: "scan_0123456789abcdef0123456789abcdef", Agent: "0123456789abcdef0123456789abcdef",
			Note: "edge-01", Transport: "aop", Endpoint: "http://127.0.0.1:1",
		},
	}
	reg.SetRemoteSource(func(agentID string) *executor.RemoteNode {
		if agentID != "0123456789abcdef0123456789abcdef" {
			return nil
		}
		return &executor.RemoteNode{
			Deploy: "command", Recipe: "full", State: "online", TunnelUp: true,
			DispatchBlockedReason: "隧道节点在文件合同上线前不能承接意图（docs/14 §8）",
		}
	})
	return reg
}

func TestInfosCarryTheRemoteProfile(t *testing.T) {
	reg := newRemoteFixture(t)
	var found *executor.NodeInfo
	for i := range reg.Infos() {
		if reg.Infos()[i].ID == "scan_0123456789abcdef0123456789abcdef" {
			info := reg.Infos()[i]
			found = &info
		}
	}
	if found == nil {
		t.Fatal("the discovered tunnel node is missing from Infos")
	}
	if found.Remote == nil {
		t.Fatal("the discovered tunnel node lost its remote profile")
	}
	if !found.Remote.TunnelUp || found.Remote.Recipe != "full" || found.Remote.DispatchReady {
		t.Fatalf("remote profile = %+v", found.Remote)
	}
	// The local node has no remote profile: it is not a tunnel node.
	for _, info := range reg.Infos() {
		if info.ID == "local" && info.Remote != nil {
			t.Fatal("the local node claimed a remote profile")
		}
	}
}

func TestRouterRefusesBlockedRemoteNodeBeforeDialing(t *testing.T) {
	reg := newRemoteFixture(t)
	router := reg.Executor(time.Second)
	res, err := router.Run(context.Background(), executor.Request{Node: "scan_0123456789abcdef0123456789abcdef"}, func(executor.Event) {})
	if err == nil {
		t.Fatal("a blocked tunnel node accepted a run")
	}
	if !strings.Contains(err.Error(), "不能承接意图") {
		t.Fatalf("refusal = %v, want the file-contract reason (not a network error)", err)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
}

// Once the file contract lands the same node must be routable again: the gate
// is the profile flag, not the node's transport.
func TestRouterAllowsRemoteNodeWhenReady(t *testing.T) {
	reg := newRemoteFixture(t)
	reg.SetRemoteSource(func(agentID string) *executor.RemoteNode {
		return &executor.RemoteNode{Recipe: "full", DispatchReady: true}
	})
	// The AOP endpoint is unreachable; what matters is that the refusal did
	// not fire and the run failed at the network layer instead.
	_, err := reg.Executor(time.Second).Run(context.Background(), executor.Request{Node: "scan_0123456789abcdef0123456789abcdef"}, func(executor.Event) {})
	if err != nil && strings.Contains(err.Error(), "不能承接意图") {
		t.Fatalf("a ready remote node was still refused: %v", err)
	}
}
