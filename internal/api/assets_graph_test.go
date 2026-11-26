package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
)

func readAssetGraph(t *testing.T, s *Server, path string) AssetGraph {
	t.Helper()
	rec := call(t, s, http.MethodGet, path, nil)
	if rec.Code != 200 {
		t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
	}
	var out AssetGraph
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func graphNodes(out AssetGraph) map[string]AssetGraphNode {
	byID := map[string]AssetGraphNode{}
	for _, n := range out.Nodes {
		byID[n.ID] = n
	}
	return byID
}

func hasEdge(out AssetGraph, source, target, kind string) bool {
	for _, e := range out.Edges {
		if e.Source == source && e.Target == target && e.Kind == kind {
			return true
		}
	}
	return false
}

// The full view is one node per asset row — the declared domain, the pending
// domain, the IP row and the CIDR — with the fence totals the legend reads.
func TestAssetGraphFullView(t *testing.T) {
	s, _ := assetPageServer(t)
	out := readAssetGraph(t, s, "/api/assets/graph")
	if !out.Full || out.Key != "" {
		t.Fatalf("full flag = %+v", out)
	}
	if len(out.Nodes) != 4 || len(out.Edges) != 0 {
		t.Fatalf("nodes = %d edges = %d", len(out.Nodes), len(out.Edges))
	}
	byID := graphNodes(out)
	domain := byID[assetRowNodeID("example.com")]
	if domain.Kind != model.AssetKindDomain || domain.Fence != model.AssetFenceIn || domain.AssetID == "" {
		t.Fatalf("domain node = %+v", domain)
	}
	if domain.Tested != "concluded" || domain.Vulns != 2 {
		t.Fatalf("domain readings = %+v", domain)
	}
	if ip := byID[assetRowNodeID("10.0.0.5")]; ip.Kind != model.AssetKindHost || ip.Fence != "" {
		t.Fatalf("ip node = %+v", ip)
	}
	if rng := byID[assetRowNodeID("10.0.0.0/8")]; rng.Kind != model.AssetKindRange {
		t.Fatalf("range node = %+v", rng)
	}
	if out.Totals["in_fence"] != 2 || out.Totals["pending"] != 1 || out.Totals["unregistered"] != 1 {
		t.Fatalf("totals = %+v", out.Totals)
	}
	if out.UnlinkedVulns != 3 {
		t.Fatalf("unlinked = %d", out.UnlinkedVulns)
	}
}

// The drill-down draws the hierarchy the sheet describes: root domain, its
// hosts, the observed URLs, the entries and the directions that touched them.
// The apex host that repeats the row key is the root, not a second node.
func TestAssetGraphDrilldown(t *testing.T) {
	s, _ := assetPageServer(t)
	asset, ok := s.Store.AssetByKindKey(model.AssetKindDomain, "example.com")
	if !ok {
		t.Fatal("seed asset missing")
	}
	if _, err := s.Store.UpdateAsset(asset.ID, func(a *model.Asset) {
		a.EvidenceIDs = []string{"in_x/log.txt"}
	}); err != nil {
		t.Fatal(err)
	}

	out := readAssetGraph(t, s, "/api/assets/graph?key=example.com")
	if out.Full || out.Key != "example.com" {
		t.Fatalf("drill flag = %+v", out)
	}
	byID := graphNodes(out)
	root := byID[assetRowNodeID("example.com")]
	if root.Kind != model.AssetKindDomain || root.Fence != model.AssetFenceIn {
		t.Fatalf("root = %+v", root)
	}
	if _, ok := byID[assetHostNodeID("example.com")]; ok {
		t.Fatal("the apex host was drawn a second time beside the root")
	}
	for _, host := range []string{"www.example.com", "api.example.com"} {
		node, ok := byID[assetHostNodeID(host)]
		if !ok || node.Kind != "host" || node.Fence != "" {
			t.Fatalf("host node %s = %+v", host, node)
		}
		if !hasEdge(out, assetRowNodeID("example.com"), assetHostNodeID(host), assetEdgeContains) {
			t.Fatalf("host %s not attached to the root", host)
		}
	}
	urls := 0
	for _, n := range out.Nodes {
		if n.Kind != "url" {
			continue
		}
		urls++
		if n.URL == "" || n.Label == "" {
			t.Fatalf("url node = %+v", n)
		}
		parent := assetHostNodeID(n.Host)
		if !hasEdge(out, parent, n.ID, assetEdgeContains) {
			t.Fatalf("url %s not attached to %s", n.URL, parent)
		}
	}
	if urls != 2 {
		t.Fatalf("url nodes = %d", urls)
	}
	vulns := 0
	for _, n := range out.Nodes {
		if n.Kind != "vuln" {
			continue
		}
		vulns++
		if !hasEdge(out, assetHostNodeID(n.Host), n.ID, assetEdgeHangs) {
			t.Fatalf("vuln %s not attached to %s", n.ID, n.Host)
		}
	}
	if vulns != 2 {
		t.Fatalf("vuln nodes = %d", vulns)
	}
	intents := 0
	for _, n := range out.Nodes {
		if n.Kind != "intent" {
			continue
		}
		intents++
		// The direction is not in this fixture's ledger, so the label is the id.
		if n.Label == "" || n.IntentID == "" {
			t.Fatalf("intent node = %+v", n)
		}
	}
	if intents != 2 {
		t.Fatalf("intent nodes = %d", intents)
	}
	if !hasEdge(out, assetRowNodeID("example.com"), assetEvidenceNodeID("in_x/log.txt"), assetEdgeHangs) {
		t.Fatal("evidence not attached to the row")
	}

	if rec := call(t, s, http.MethodGet, "/api/assets/graph?key=missing.example", nil); rec.Code != 404 {
		t.Fatalf("unknown row = %d", rec.Code)
	}
}

// A declared ip:port service sits between the host and its URL, so the chain
// reads host → ip:port → URL.
func TestAssetGraphServiceHierarchy(t *testing.T) {
	s, _ := assetPageServer(t)
	out := readAssetGraph(t, s, "/api/assets/graph?key=10.0.0.5")
	byID := graphNodes(out)
	root := byID[assetRowNodeID("10.0.0.5")]
	if root.Kind != model.AssetKindHost {
		t.Fatalf("root = %+v", root)
	}
	service := byID[assetServiceNodeID("10.0.0.5:8080")]
	if service.Kind != "service" || service.Host != "10.0.0.5" {
		t.Fatalf("service = %+v", service)
	}
	if !hasEdge(out, root.ID, service.ID, assetEdgeContains) {
		t.Fatal("service not attached to the host row")
	}
	urlID := ""
	for _, n := range out.Nodes {
		if n.Kind == "url" {
			urlID = n.ID
		}
	}
	if urlID == "" {
		t.Fatal("url node missing")
	}
	// The captured URL carries no explicit port, so it attaches to the host and
	// not to :8080 — the graph never invents a port relation.
	if !hasEdge(out, root.ID, urlID, assetEdgeContains) {
		t.Fatalf("url not attached to the host: edges %+v", out.Edges)
	}
	found := false
	for _, n := range out.Nodes {
		if n.Kind == "intent" && n.IntentID == "in_f3" {
			found = true
			if !hasEdge(out, urlID, n.ID, assetEdgeHangs) {
				t.Fatal("intent not attached to the URL")
			}
		}
	}
	if !found {
		t.Fatalf("intent in_f3 missing: %+v", out.Nodes)
	}
}
