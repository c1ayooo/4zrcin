package enroll

import (
	"testing"
	"time"
)

// The launcher learns the node's default egress proxy on the channel it
// already polls, together with whether the node is busy, so it can restart
// scan at a moment that interrupts nothing.
func TestNextCarriesScanProxyAndBusy(t *testing.T) {
	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{Name: "edge-01"})
	joined := mustJoin(t, svc, created.Token, "install-1")

	// Nothing configured: the node egresses direct and takes work as usual.
	if res := svc.Next(joined.NodeID, 0); res.ScanProxy != "" || res.Busy {
		t.Fatalf("unconfigured long-poll = proxy %q busy %v", res.ScanProxy, res.Busy)
	}

	svc.SetScanProxy(func() string { return "socks5://proxy.test:1080" })
	svc.SetNodeBusy(func(nodeID string) bool { return nodeID == joined.NodeID })
	res := svc.Next(joined.NodeID, 0)
	if res.ScanProxy != "socks5://proxy.test:1080" || !res.Busy {
		t.Fatalf("long-poll = proxy %q busy %v", res.ScanProxy, res.Busy)
	}
}

// A node whose scan still runs the previous default does not take work: the
// direction would egress from where the operator just moved it away from. The
// hold lifts on the heartbeat that reports the new default applied, and a
// launcher that predates the report is never held.
func TestScanProxyPendingHoldsDispatchUntilApplied(t *testing.T) {
	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{Name: "edge-01"})
	joined := mustJoin(t, svc, created.Token, "install-1")
	view, err := svc.Heartbeat(joined.NodeID, Report{NodeBinPath: "/opt/zrcin-node/bin/zrcin", ScanProxyDigest: ScanProxyDigest("")})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if view.ProxyPending || !view.DispatchesOK {
		t.Fatalf("direct node with nothing configured = %+v", view)
	}

	// The operator saves a proxy: the node holds before its next heartbeat.
	svc.SetScanProxy(func() string { return "socks5://proxy.test:1080" })
	if got := svc.Nodes()[0]; !got.ProxyPending || got.DispatchesOK {
		t.Fatalf("node still dispatch-ready with the old egress: %+v", got)
	}

	view, err = svc.Heartbeat(joined.NodeID, Report{ScanProxyDigest: ScanProxyDigest("socks5://proxy.test:1080")})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if view.ProxyPending || !view.DispatchesOK {
		t.Fatalf("applied node still held: %+v", view)
	}

	// A launcher that never reports (predates the field) is not managed and
	// must not be parked forever.
	old := mustJoin(t, svc, mustCreate(t, svc, CreateOptions{Name: "edge-old"}).Token, "install-2")
	view, err = svc.Heartbeat(old.NodeID, Report{NodeBinPath: "/opt/zrcin-node/bin/zrcin"})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if view.ProxyPending || !view.DispatchesOK {
		t.Fatalf("launcher without the report was held: %+v", view)
	}
}

// A saved change reaches a parked long-poll at once: the launcher should not
// wait out its 30 s hold to learn the node's egress moved.
func TestScanProxyChangeWakesLongPoll(t *testing.T) {
	svc, _, _ := newTestService(t)
	joined := mustJoin(t, svc, mustCreate(t, svc, CreateOptions{Name: "edge-01"}).Token, "install-1")

	type outcome struct{ res NextResult }
	done := make(chan outcome, 1)
	go func() { done <- outcome{res: svc.Next(joined.NodeID, 10*time.Second)} }()
	time.Sleep(50 * time.Millisecond) // let the poll park

	svc.SetScanProxy(func() string { return "socks5://proxy.test:1080" })
	select {
	case got := <-done:
		if got.res.ScanProxy != "socks5://proxy.test:1080" {
			t.Fatalf("woken long-poll = proxy %q", got.res.ScanProxy)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake on a proxy change")
	}
}
