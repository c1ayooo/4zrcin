package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/traffic"
	native "github.com/chainreactors/cyber/aop/traffic"
)

func readAssetTree(t *testing.T, s *Server) AssetGraph {
	t.Helper()
	rec := call(t, s, http.MethodGet, "/api/assets/graph", nil)
	if rec.Code != 200 {
		t.Fatalf("graph = %d %s", rec.Code, rec.Body.String())
	}
	var out AssetGraph
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func treeNodes(out AssetGraph) map[string]AssetGraphNode {
	byID := map[string]AssetGraphNode{}
	for _, n := range out.Nodes {
		byID[n.ID] = n
	}
	return byID
}

func hasEdge(out AssetGraph, source, target string) bool {
	for _, e := range out.Edges {
		if e.Source == source && e.Target == target {
			return true
		}
	}
	return false
}

// The tree is rooted at a project-name card; the fence's rows fan out beneath
// it in a stable order, and every node hangs off exactly one parent.
func TestAssetTreeRootAndColumns(t *testing.T) {
	s, _ := assetPageServer(t)
	out := readAssetTree(t, s)

	root := treeNodes(out)[assetRootID]
	if root.Kind != "project" || root.Label != "绑定测试" {
		t.Fatalf("root = %+v", root)
	}
	// Column 1: the four rows, domains before IPs before ranges.
	// Column 1 = exactly the nodes the root card parents: col2 host nodes
	// share the kinds, so parenthood — not kind — is the identity.
	rows := map[string]AssetGraphNode{}
	col1 := 0
	for _, n := range out.Nodes {
		if n.ID != assetRootID && hasEdge(out, assetRootID, n.ID) {
			rows[n.Label] = n
			col1++
		}
	}
	if col1 != 4 {
		t.Fatalf("col1 rows = %d: %+v", col1, out.Nodes)
	}
	if rows["example.com"].Kind != model.AssetKindDomain || rows["example.com"].Fence != model.AssetFenceIn {
		t.Fatalf("domain row = %+v", rows["example.com"])
	}
	if rows["10.0.0.5"].Kind != model.AssetKindHost || rows["10.0.0.5"].Fence != "" {
		t.Fatalf("ip row = %+v", rows["10.0.0.5"])
	}
	if rows["10.0.0.0/8"].Kind != model.AssetKindRange {
		t.Fatalf("range row = %+v", rows["10.0.0.0/8"])
	}
	if out.Totals["in_fence"] != 2 || out.Totals["pending"] != 1 || out.Totals["unregistered"] != 1 {
		t.Fatalf("totals = %+v", out.Totals)
	}
	if out.UnlinkedVulns != 3 {
		t.Fatalf("unlinked = %d", out.UnlinkedVulns)
	}
}

// Column 2: the domain's observed hosts (the apex folds into the row node);
// column 3: declared interfaces; column 4: captured URLs. A portless URL
// attaches to the host, never to an interface it never used.
func TestAssetTreeHierarchy(t *testing.T) {
	s, _ := assetPageServer(t)
	// One interface with a functional summary under the apex domain.
	if _, err := s.Store.AddFact(model.Fact{Kind: "service", Title: "example.com:443", Summary: "https · open · nginx/1.25 · 网关", Confidence: "observed", Status: "unverified"}); err != nil {
		t.Fatal(err)
	}
	out := readAssetTree(t, s)
	byID := treeNodes(out)
	rowID := assetRowNodeID("example.com")

	// The apex host is the row node: it carries the apex's own world.
	if _, ok := byID[assetHostNodeID("example.com")]; ok {
		t.Fatal("the apex host was drawn a second time beside its row")
	}
	for _, host := range []string{"www.example.com", "api.example.com"} {
		node, ok := byID[assetHostNodeID(host)]
		if !ok || node.Kind != "host" {
			t.Fatalf("host node %s = %+v", host, node)
		}
		if !hasEdge(out, rowID, assetHostNodeID(host)) {
			t.Fatalf("host %s not attached to its domain", host)
		}
	}
	www := byID[assetHostNodeID("www.example.com")]
	if www.Tested != "concluded" || www.Vulns != 1 || len(www.VulnTags) != 1 || www.VulnTags[0].ID != "gv_1" {
		t.Fatalf("www tags = %+v", www)
	}
	api := byID[assetHostNodeID("api.example.com")]
	if api.Tested != "concluded" || api.Vulns != 1 || api.VulnTags[0].ID != "gv_2" {
		t.Fatalf("api tags = %+v", api)
	}

	// Column 3 under the apex: the declared :443 with its functional label.
	gw := byID[assetServiceNodeID("example.com:443")]
	if gw.Kind != "service" || gw.Label != ":443 nginx/1.25 · 网关" || gw.Sub != "https · open · nginx/1.25 · 网关" {
		t.Fatalf("gateway interface = %+v", gw)
	}
	if !hasEdge(out, rowID, gw.ID) {
		t.Fatal("interface not attached to the apex row")
	}
	// Column 4: the captured paths.
	paths := map[string]AssetGraphNode{}
	for _, n := range out.Nodes {
		if n.Kind == "url" {
			paths[n.URL] = n
		}
	}
	aURL := paths["https://www.example.com/a"]
	if aURL.ID == "" || aURL.Label != "/a" || !hasEdge(out, assetHostNodeID("www.example.com"), aURL.ID) {
		t.Fatalf("www url node = %+v", aURL)
	}
	xURL := paths["http://10.0.0.5/x"]
	if xURL.ID == "" {
		t.Fatal("ip url node missing")
	}
	// Portless: the URL hangs off the ip row, and never under :8080.
	if !hasEdge(out, assetRowNodeID("10.0.0.5"), xURL.ID) {
		t.Fatalf("portless url not attached to the ip row")
	}
	if hasEdge(out, assetServiceNodeID("10.0.0.5:8080"), xURL.ID) {
		t.Fatal("the url was attached to an interface it never used")
	}
	// :8080 carries its own interface tag (host:ip:port names exactly it).
	if8080 := byID[assetServiceNodeID("10.0.0.5:8080")]
	if if8080.Kind != "service" || len(if8080.VulnTags) != 1 || if8080.VulnTags[0].ID != "gv_5" {
		t.Fatalf(":8080 tags = %+v", if8080)
	}
	if if8080.Label != ":8080 〔漏洞〕" {
		t.Fatalf(":8080 label = %q", if8080.Label)
	}
	if !hasEdge(out, assetRowNodeID("10.0.0.5"), if8080.ID) {
		t.Fatal("interface not attached to the ip row")
	}
}

// A host the fence admits but nobody registered still reads 未登记 — and its
// node is present: the tree never invents an asset standing.
func TestAssetTreeUnregisteredHost(t *testing.T) {
	s, _ := assetPageServer(t)
	out := readAssetTree(t, s)
	byID := treeNodes(out)
	host := byID[assetHostNodeID("www.example.com")]
	if host.Fence != "" {
		t.Fatalf("unregistered host fence = %q", host.Fence)
	}
	pending := byID[assetRowNodeID("pending.example")]
	if pending.Fence != model.AssetFencePending || pending.Tested != "untouched" {
		t.Fatalf("pending row = %+v", pending)
	}
	// The pending domain folds its only host into the row: no ghost second node.
	if _, ok := byID[assetHostNodeID("pending.example")]; ok {
		t.Fatal("the pending domain's apex was drawn twice")
	}
	if rec := call(t, s, http.MethodGet, "/api/assets/graph?key=anything", nil); rec.Code != 200 {
		t.Fatalf("the key parameter must be accepted and ignored = %d", rec.Code)
	}
}

// An interface's own tested reading: the URLs it served reach it, an entry
// naming it concludes it — the :443 above has neither and stays untouched.
func TestAssetTreeServiceTested(t *testing.T) {
	s, _ := assetPageServer(t)
	if _, err := s.Store.AddFact(model.Fact{Kind: "service", Title: "api.example.com:8443", Summary: "https · open · tomcat · 管理后台", Confidence: "observed", Status: "unverified"}); err != nil {
		t.Fatal(err)
	}
	if _, err := traffic.SaveCapture(s.CaseRoot, "run-svc", "in_s1", "sess", "ev-s1", &native.Flow{
		Id: "f-svc", Complete: true,
		Request:  &native.HttpRequest{Method: "GET", Url: "https://api.example.com:8443/manage"},
		Response: &native.HttpResponse{StatusCode: 200},
	}, nil, "record", "sr-1", false, false); err != nil {
		t.Fatal(err)
	}
	out := readAssetTree(t, s)
	byID := treeNodes(out)
	svc := byID[assetServiceNodeID("api.example.com:8443")]
	if svc.Kind != "service" || svc.Label != ":8443 tomcat · 管理后台" {
		t.Fatalf("service = %+v", svc)
	}
	if svc.Tested != "reached" || svc.Requests != 1 {
		t.Fatalf("service tested = %+v", svc)
	}
	// The port-matched URL hangs under the interface, not the host.
	if !hasEdge(out, svc.ID, assetURLNodeID("https://api.example.com:8443/manage")) {
		t.Fatal("the url did not attach to its interface")
	}
	// :8080 has a tag but its port served no URL: concluded wins over reached.
	s8080 := byID[assetServiceNodeID("10.0.0.5:8080")]
	if s8080.Tested != "concluded" {
		t.Fatalf(":8080 tested = %+v", s8080)
	}
	// An interface whose port served nothing stays untouched: :3306 is declared
	// but no capture ever used it, and nothing names it in the library.
	if _, err := s.Store.AddFact(model.Fact{Kind: "service", Title: "10.0.0.5:3306", Summary: "mysql · open", Confidence: "observed", Status: "unverified"}); err != nil {
		t.Fatal(err)
	}
	out = readAssetTree(t, s)
	byID = treeNodes(out)
	db := byID[assetServiceNodeID("10.0.0.5:3306")]
	if db.Kind != "service" || db.Tested != "untouched" {
		t.Fatalf("mysql interface = %+v", db)
	}
	if !hasEdge(out, assetRowNodeID("10.0.0.5"), db.ID) {
		t.Fatal("the untouched interface is not attached to its ip row")
	}
}
