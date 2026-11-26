package api

// The asset graph's read model (spec #1 工单7/#8): the console reuses the
// engagement canvas, so this projection speaks the same language the web-side
// graph model already draws — nodes and edges, no layout. Positions, collision
// and camera stay the canvas's job.
//
// Two views, one projection:
//
//	full  (no key)  one node per asset row — domain, IP or CIDR — coloured by
//	                its fence standing. It answers "what does the engagement
//	                declare, and where is each row standing?".
//	keyed           the drill-down hierarchy: the row, its hosts, the declared
//	                ip:port services, the observed URLs, and what hangs off
//	                them (intentions, library entries, stored evidence).
//
// The drill-down reuses assetDetailFor, so the graph and the sheet can never
// disagree about one domain's hosts, URLs or entries.

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// AssetGraphNode is one drawn node. Kind names the asset vocabulary the graph
// adds to the fact kinds: domain, range, host, service, url, vuln, evidence and
// intent. Fence is the stored standing (empty = 未登记); Tested is the table's
// three-state reading.
type AssetGraphNode struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Sub      string `json:"sub,omitempty"`
	Fence    string `json:"fence,omitempty"`
	Tested   string `json:"tested,omitempty"`
	AssetID  string `json:"asset_id,omitempty"`
	Host     string `json:"host,omitempty"`
	URL      string `json:"url,omitempty"`
	IntentID string `json:"intent_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Vulns    int    `json:"vulns,omitempty"`
	Requests int    `json:"requests,omitempty"`
	URLs     int    `json:"urls,omitempty"`
}

// AssetGraphEdge is one relation. "contains" is the hierarchy the drill-down
// reads top-down; "hangs" attaches an intention, a library entry or evidence
// to the asset it names. Both are structural, so the layered layout can place
// them as tree edges.
type AssetGraphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"` // contains | hangs
}

// AssetGraph is the response: the project's fence totals, the rows, and — when
// a key was asked for — that row's hierarchy.
type AssetGraph struct {
	Key           string           `json:"key,omitempty"`
	Full          bool             `json:"full"`
	Nodes         []AssetGraphNode `json:"nodes"`
	Edges         []AssetGraphEdge `json:"edges"`
	Totals        map[string]int   `json:"totals"`
	UnlinkedVulns int              `json:"unlinked_vulns"`
}

const (
	assetEdgeContains = "contains"
	assetEdgeHangs    = "hangs"
)

func assetRowNodeID(key string) string     { return "asset:row:" + key }
func assetHostNodeID(host string) string   { return "asset:host:" + host }
func assetServiceNodeID(key string) string { return "asset:service:" + key }
func assetURLNodeID(raw string) string     { return "asset:url:" + raw }
func assetVulnNodeID(id string) string     { return "asset:vuln:" + id }
func assetEvidenceNodeID(id string) string { return "asset:evidence:" + id }
func assetIntentNodeID(id string) string   { return "asset:intent:" + id }

// assetFenceState names the standing a row shows: the stored fence, or
// "unregistered" when there is no asset record — never "out of fence", because
// the control plane cannot judge the rule table here.
func assetFenceState(fence string) string {
	switch fence {
	case model.AssetFenceIn, model.AssetFencePending, model.AssetFenceDenied:
		return fence
	default:
		return "unregistered"
	}
}

// GET /api/assets/graph?key=<optional row key>
func (s *Server) assetGraph(w http.ResponseWriter, r *http.Request) {
	out, code, msg := s.assetGraphData(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("key"))))
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) assetGraphData(key string) (AssetGraph, int, string) {
	rows, unlinked := s.assetTable()
	out := AssetGraph{
		Key:  key,
		Full: key == "",
		// Empty lists marshal as [] not null: the console iterates both on
		// every load, and the full view's "no assets yet" must read as zero
		// nodes, not as a broken response.
		Nodes:         []AssetGraphNode{},
		Edges:         []AssetGraphEdge{},
		Totals:        map[string]int{},
		UnlinkedVulns: unlinked,
	}
	for _, row := range rows {
		out.Totals[assetFenceState(row.Fence)]++
	}
	if key == "" {
		for _, row := range rows {
			out.Nodes = append(out.Nodes, AssetGraphNode{
				ID:       assetRowNodeID(row.Key),
				Kind:     row.Kind,
				Label:    row.Key,
				Fence:    row.Fence,
				Tested:   row.Tested,
				AssetID:  row.AssetID,
				Vulns:    row.Vulns,
				Requests: row.Requests,
				URLs:     row.URLs,
			})
		}
		return out, 0, ""
	}

	detail, code, msg := s.assetDetailFor(key, "")
	if code != 0 {
		return AssetGraph{}, code, msg
	}
	row := detail.Row
	out.Nodes = append(out.Nodes, AssetGraphNode{
		ID:       assetRowNodeID(row.Key),
		Kind:     row.Kind,
		Label:    row.Key,
		Fence:    row.Fence,
		Tested:   row.Tested,
		AssetID:  row.AssetID,
		Vulns:    row.Vulns,
		Requests: row.Requests,
		URLs:     row.URLs,
	})
	rootID := assetRowNodeID(row.Key)
	addEdge := func(source, target, kind string) {
		out.Edges = append(out.Edges, AssetGraphEdge{
			ID: "e:" + source + ">" + target, Source: source, Target: target, Kind: kind,
		})
	}

	db := s.Store.Snapshot()
	intentTitles := map[string]model.Intent{}
	for _, in := range db.Intents {
		intentTitles[in.ID] = in
	}
	// Intent nodes are one per direction, not one per URL: the same direction
	// touching three paths is one direction.
	intentNode := map[string]string{}
	addIntent := func(parent, intentID string) {
		if intentID == "" {
			return
		}
		nodeID, seen := intentNode[intentID]
		if !seen {
			label, status := intentID, ""
			if in, ok := intentTitles[intentID]; ok {
				label, status = in.Title, in.Status
			}
			nodeID = assetIntentNodeID(intentID)
			intentNode[intentID] = nodeID
			out.Nodes = append(out.Nodes, AssetGraphNode{
				ID: nodeID, Kind: "intent", Label: label, Status: status, IntentID: intentID,
			})
		}
		addEdge(parent, nodeID, assetEdgeHangs)
	}

	// Hosts: the row's own key is the root, so an apex host that repeats it is
	// not drawn a second time. Everything else hangs off the root.
	hostNode := map[string]string{}
	for _, hd := range detail.Hosts {
		if hd.Host == row.Key {
			hostNode[hd.Host] = rootID
			continue
		}
		nodeID := assetHostNodeID(hd.Host)
		hostNode[hd.Host] = nodeID
		out.Nodes = append(out.Nodes, AssetGraphNode{
			ID: nodeID, Kind: "host", Label: hd.Host, Host: hd.Host,
			Fence: hd.Fence, AssetID: hd.AssetID,
			Vulns: hd.Vulns, Requests: hd.Requests, URLs: hd.URLs,
		})
		addEdge(rootID, nodeID, assetEdgeContains)
	}

	// The declared ip:port services under one host. A URL with a port attaches
	// to its service, so the hierarchy reads domain → host → ip:port → URL.
	serviceNode := map[string]string{} // host:port → node id
	var serviceOrder []string
	for _, f := range db.Facts {
		if f.Unusable() || f.Kind != "service" {
			continue
		}
		host, port, ok := strings.Cut(f.Title, ":")
		if !ok || host == "" || port == "" {
			continue
		}
		host = strings.ToLower(host)
		if !containsHost(row.Hosts, host) {
			continue
		}
		nodeID := assetServiceNodeID(host + ":" + port)
		serviceNode[host+":"+port] = nodeID
		serviceOrder = append(serviceOrder, host+":"+port)
		out.Nodes = append(out.Nodes, AssetGraphNode{
			ID: nodeID, Kind: "service", Label: host + ":" + port, Host: host,
		})
	}
	for _, hostPort := range serviceOrder {
		nodeID := serviceNode[hostPort]
		host, _, _ := strings.Cut(hostPort, ":")
		parent := hostNode[host]
		if parent == "" {
			parent = rootID
		}
		addEdge(parent, nodeID, assetEdgeContains)
	}

	// URL rows: one node per path shape, attached below the service that owns
	// its port, else below its host.
	urlIntent := map[string]bool{}
	for _, u := range detail.URLs {
		parsed, err := url.Parse(u.URL)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		port := parsed.Port()
		nodeID := assetURLNodeID(u.URL)
		out.Nodes = append(out.Nodes, AssetGraphNode{
			ID: nodeID, Kind: "url", Label: parsed.Path, Sub: u.URL, URL: u.URL, Host: host,
			Requests: u.Requests, Status: u.Verdict,
		})
		parent := ""
		if port != "" {
			parent = serviceNode[host+":"+port]
		}
		if parent == "" {
			parent = hostNode[host]
		}
		if parent == "" {
			parent = rootID
		}
		addEdge(parent, nodeID, assetEdgeContains)
		for _, intentID := range u.Intents {
			urlIntent[intentID] = true
			addIntent(nodeID, intentID)
		}
	}

	// Host-level intentions that no URL named (a run that only touched the host).
	for _, hd := range detail.Hosts {
		parent := hostNode[hd.Host]
		if parent == "" {
			continue
		}
		for _, intentID := range hd.Intents {
			if urlIntent[intentID] {
				continue
			}
			addIntent(parent, intentID)
		}
	}

	// Library entries hang off the host their entity_ref names.
	for _, entry := range detail.Vulns {
		host := vulnEntityHost(entry.EntityRef)
		parent := hostNode[host]
		if parent == "" {
			parent = rootID
		}
		nodeID := assetVulnNodeID(entry.ID)
		sub := strings.TrimSpace(entry.Severity + " " + entry.Status)
		out.Nodes = append(out.Nodes, AssetGraphNode{
			ID: nodeID, Kind: "vuln", Label: entry.Title, Sub: sub, Host: host,
		})
		addEdge(parent, nodeID, assetEdgeHangs)
	}

	// Stored evidence references attach to the row itself: a fence/import seed
	// names the capture it came from, and the graph shows the citation.
	for _, id := range detail.EvidenceIDs {
		nodeID := assetEvidenceNodeID(id)
		out.Nodes = append(out.Nodes, AssetGraphNode{ID: nodeID, Kind: "evidence", Label: id})
		addEdge(rootID, nodeID, assetEdgeHangs)
	}
	return out, 0, ""
}

func containsHost(list []string, host string) bool {
	for _, h := range list {
		if h == host {
			return true
		}
	}
	return false
}
