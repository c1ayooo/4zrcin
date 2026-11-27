package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/node"
	"github.com/c1ayooo/4zrcin/internal/settings"
)

func tunnelNodesForTest(t *testing.T) (*enroll.Service, string) {
	t.Helper()
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
	return svc, joined.NodeID
}

// Saving the node proxy from the console works end to end: the save that
// writes it regenerates the host's runtime config in the same request, the
// value lands where the harness accepts it, and the tunnel nodes learn it at
// once — held from dispatch until their scan runs it.
func TestSettingsSaveHandsProxyToTunnelNodes(t *testing.T) {
	t.Setenv("CYBER_PROXY", "")
	h, harness := singleModelHub(t)
	h.cfg.Nodes, h.cfg.DataDir = &node.Registry{}, t.TempDir()
	svc, nodeID := tunnelNodesForTest(t)
	h.cfg.RemoteNodes = svc
	h.startNodeProxy()
	if got := svc.Next(nodeID, 0).ScanProxy; got != "" {
		t.Fatalf("unconfigured platform hands out %q", got)
	}

	rec := settingsPUT(t, h, `{"node_proxy_write":{"proxy":"socks5://127.0.0.1:1080"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings save = %d %s", rec.Code, rec.Body.String())
	}
	var view settings.View
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.NodeProxy.Configured || view.NodeProxy.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("node proxy view = %+v", view.NodeProxy)
	}
	data, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	// Top-level cyberhub is the layout every harness binary accepts since the
	// gateway rework; the nested extensions form is the rejected one.
	if !strings.Contains(text, "cyberhub:") || !strings.Contains(text, "proxy: socks5://127.0.0.1:1080") {
		t.Fatalf("proxy not saved under cyberhub:\n%s", text)
	}
	if strings.Contains(text, "extensions:") {
		t.Fatalf("proxy written to the rejected nested layout:\n%s", text)
	}
	if got := svc.Next(nodeID, 0).ScanProxy; got != "socks5://127.0.0.1:1080" {
		t.Fatalf("tunnel node was handed %q", got)
	}
	if got := svc.Nodes()[0]; !got.ProxyPending {
		t.Fatalf("node still dispatch-ready before its scan runs the proxy: %+v", got)
	}
	if env := hostEnv(nil, "", nodeProxyValue(h.settings)); envValue(env, "CYBER_PROXY") != "socks5://127.0.0.1:1080" {
		t.Fatalf("local host env = %v", env)
	}
}

// A tunnel node is busy while a run executes on it in any project: the
// launcher reads the answer before it restarts scan for a proxy change.
func TestNodeBusyAcrossProjects(t *testing.T) {
	h, _ := createOptionsHub(t, hubConfig{NoDecider: true})
	ids := []string{}
	for _, name := range []string{"one.example", "two.example"} {
		code, out := postProject(t, h, `{"name":"`+name+`","scope":"`+name+`"}`)
		if code != http.StatusCreated {
			t.Fatalf("create = %d %v", code, out)
		}
		ids = append(ids, out["id"].(string))
	}
	if h.nodeBusy("node-a") {
		t.Fatal("a node with no runs reads busy")
	}
	first := h.kits[ids[0]].srv.Store
	run, err := first.AddRun(model.Run{Node: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.nodeBusy("node-a") {
		t.Fatal("a running run did not make the node busy")
	}
	if _, err := first.UpdateRun(run.ID, func(r *model.Run) { r.Status = model.RunDone }); err != nil {
		t.Fatal(err)
	}
	if h.nodeBusy("node-a") {
		t.Fatal("a finished run keeps the node busy")
	}
	if _, err := h.kits[ids[1]].srv.Store.AddRun(model.Run{Node: "node-a"}); err != nil {
		t.Fatal(err)
	}
	if !h.nodeBusy("node-a") {
		t.Fatal("a run in the second project did not make the node busy")
	}
	if h.nodeBusy("node-b") {
		t.Fatal("another node reads busy")
	}
}
