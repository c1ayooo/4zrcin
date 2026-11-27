package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/tunnel"
)

// fakeIngress answers Session and panics on the rest: the adapter only reads.
type fakeIngress struct {
	tunnel.Ingress
	sessions map[string]tunnel.SessionInfo
}

func (f fakeIngress) TicketHandler() http.Handler { return nil }
func (f fakeIngress) TunnelHandler() http.Handler { return nil }
func (f fakeIngress) Session(nodeID string) (tunnel.SessionInfo, bool) {
	info, ok := f.sessions[nodeID]
	return info, ok
}
func (f fakeIngress) Close(string) error { return nil }
func (f fakeIngress) Port() int          { return 0 }

// The profile is where the enrollment record and the live tunnel meet: the
// record says how the node was deployed, the tunnel says whether it is up.
// Without this source the registry would treat a tunnel node as a direct one
// and the router would send no turn seed (docs/14 §7.3, §8).
func TestRemoteProfileForJoinsRecordAndTunnel(t *testing.T) {
	store, err := enroll.Open(filepath.Join(t.TempDir(), "remote-nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := enroll.New(store, enroll.Options{
		PublicURL:   "https://ops.example.com",
		DataDir:     "/srv/zrcin/data",
		ConsoleAddr: "127.0.0.1:8080",
		HubAddr:     "127.0.0.1:18766",
		HubKey:      "hub-key",
		IssueKey:    []byte("0123456789abcdef0123456789abcdef"),
	})
	created, err := svc.Create(enroll.CreateOptions{Name: "edge-01"})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := svc.Join(created.Token, "install-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Heartbeat(joined.NodeID, enroll.Report{NodeBinPath: "/opt/zrcin-node/bin/zrcin"}); err != nil {
		t.Fatal(err)
	}

	source := remoteProfileFor(svc, fakeIngress{sessions: map[string]tunnel.SessionInfo{
		joined.NodeID: {Up: true, BytesIn: 10, BytesOut: 20},
	}})

	remote := source(joined.NodeID)
	if remote == nil {
		t.Fatal("a joined tunnel node has no profile")
	}
	if remote.Deploy != enroll.DeployCommand || remote.Recipe != enroll.RecipeFull {
		t.Fatalf("profile = %+v, want command/full", remote)
	}
	if !remote.TunnelUp || !remote.DispatchReady {
		t.Fatalf("profile = %+v, want tunnel up and dispatch ready", remote)
	}
	if remote.CLIPath != "/opt/zrcin-node/bin/zrcin" {
		t.Fatalf("CLIPath = %q", remote.CLIPath)
	}

	// No live session: still the same node, but the tunnel is down.
	if remote := remoteProfileFor(svc, nil)(joined.NodeID); remote.TunnelUp {
		t.Fatal("a missing ingress must not report a tunnel")
	}
	if source("not-a-node") != nil {
		t.Fatal("an unknown agent must have no profile")
	}
}

// A node whose scan still runs the previous default proxy takes no direction
// until its launcher reports the new one applied, and the refusal says why
// (docs/14 §7.6).
func TestRemoteProfileHoldsNodeWithPendingProxy(t *testing.T) {
	store, err := enroll.Open(filepath.Join(t.TempDir(), "remote-nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := enroll.New(store, enroll.Options{
		PublicURL:   "https://ops.example.com",
		DataDir:     "/srv/zrcin/data",
		ConsoleAddr: "127.0.0.1:8080",
		HubAddr:     "127.0.0.1:18766",
		HubKey:      "hub-key",
		IssueKey:    []byte("0123456789abcdef0123456789abcdef"),
	})
	created, err := svc.Create(enroll.CreateOptions{Name: "edge-01"})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := svc.Join(created.Token, "install-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Heartbeat(joined.NodeID, enroll.Report{NodeBinPath: "/opt/zrcin-node/bin/zrcin", ScanProxyDigest: enroll.ScanProxyDigest("")}); err != nil {
		t.Fatal(err)
	}
	source := remoteProfileFor(svc, fakeIngress{})

	svc.SetScanProxy(func() string { return "socks5://proxy.test:1080" })
	remote := source(joined.NodeID)
	if remote == nil || remote.DispatchReady || !strings.Contains(remote.DispatchBlockedReason, "代理") {
		t.Fatalf("node with a pending proxy = %+v", remote)
	}

	if _, err := svc.Heartbeat(joined.NodeID, enroll.Report{ScanProxyDigest: enroll.ScanProxyDigest("socks5://proxy.test:1080")}); err != nil {
		t.Fatal(err)
	}
	if remote := source(joined.NodeID); remote == nil || !remote.DispatchReady {
		t.Fatalf("node with the proxy applied still held: %+v", remote)
	}
}
