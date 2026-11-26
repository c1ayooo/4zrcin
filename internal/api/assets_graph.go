package api

// The asset tree (spec #1 工单7-9/#8, the operator's aligned revision): one
// layered, left-to-right hierarchy on the same canvas grammar the engagement
// graph uses — a project-name card at the root, then the fence's domains /
// standalone IPs / CIDR rows, then the hosts observed under each domain, then
// the declared ip:port interfaces, then the captured URL paths.
//
// 项目卡片 → 域名/独立IP/网段 → 域名下的主机(子域/IP) → 接口(ip:port) → URL路径
//
// Every node hangs off exactly one parent, so the layered tidy-tree layout can
// draw it symmetrically (each parent centred on its children fan, sibling
// subtrees evenly stacked). What a node carries is honest and local:
//
//   - an interface's 功能作用 is the scan record's own summary (middleware,
//     title, fingerprint) — displayed verbatim, never invented;
//   - vulnerability entries ride as TAGS on the node their entity_ref names:
//     a bare host (or host:subdomain) tags that host node, and host:ip:port —
//     which the asset table deliberately refuses to guess onto rows — names
//     exactly one interface, so as a tag it is precise, not a guess;
//   - tested keeps the table's honest three-state reading, per node.
//
// No layout, positions or camera here: the console's canvas owns those.

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// AssetGraphNode is one drawn node of the asset tree.
type AssetGraphNode struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // project | domain | range | host | service | url
	Label    string `json:"label"`
	Sub      string `json:"sub,omitempty"`
	Fence    string `json:"fence,omitempty"`
	Tested   string `json:"tested,omitempty"`
	AssetID  string `json:"asset_id,omitempty"`
	Host     string `json:"host,omitempty"`
	URL      string `json:"url,omitempty"`
	Vulns    int    `json:"vulns,omitempty"`
	Requests int    `json:"requests,omitempty"`
	URLs     int    `json:"urls,omitempty"`
	// VulnTags are the library entries naming exactly this node.
	VulnTags []AssetGraphVulnTag `json:"vuln_tags,omitempty"`
}

// AssetGraphVulnTag is one library entry rendered as a label on its node.
type AssetGraphVulnTag struct {
	ID       string `json:"id"`
	Severity string `json:"severity,omitempty"`
	Title    string `json:"title"`
}

// AssetGraphEdge is one structural relation. The layered layout needs every
// node parented exactly once, so both shapes read as containment.
type AssetGraphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"` // contains | hangs
}

// AssetGraph is the response: the project-rooted tree plus the fence totals
// and the unlinked-vuln count the page's header reads.
type AssetGraph struct {
	Root          string           `json:"root"`
	Nodes         []AssetGraphNode `json:"nodes"`
	Edges         []AssetGraphEdge `json:"edges"`
	Totals        map[string]int   `json:"totals"`
	UnlinkedVulns int              `json:"unlinked_vulns"`
}

const (
	assetEdgeContains = "contains"
	assetEdgeHangs    = "hangs"

	assetRootID = "asset:project"
)

func assetRowNodeID(key string) string     { return "asset:row:" + key }
func assetHostNodeID(host string) string   { return "asset:host:" + host }
func assetServiceNodeID(key string) string { return "asset:service:" + key }
func assetURLNodeID(raw string) string     { return "asset:url:" + raw }

// assetFenceState names the standing a node shows: the stored fence, or
// "unregistered" when there is no asset record — never "out of fence".
func assetFenceState(fence string) string {
	switch fence {
	case model.AssetFenceIn, model.AssetFencePending, model.AssetFenceDenied:
		return fence
	default:
		return "unregistered"
	}
}

// assetVulnTarget reads a library entity_ref as the exact node it tags. The
// bare-host convention returns the host; host:ip:port returns that interface.
// Anything else — an account, a ref with junk — names no node and stays
// unlinked (counted apart, never guessed).
func assetVulnTarget(ref string) (kind, key string) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	ref = strings.TrimPrefix(ref, "host:")
	if host, port, err := net.SplitHostPort(ref); err == nil && port != "" {
		return "service", strings.ToLower(strings.Trim(host, "[]")) + ":" + port
	}
	if host := vulnEntityHost(ref); host != "" {
		return "host", host
	}
	return "", ""
}

// assetSevLabel is the tag suffix the interface label carries: the highest
// severity among the node's entries, in the page's own vocabulary.
func assetSevLabel(tags []AssetGraphVulnTag) string {
	if len(tags) == 0 {
		return ""
	}
	rank := map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}
	label := map[string]string{"critical": "严重", "high": "高危", "medium": "中危", "low": "低危"}
	best, name := 0, "漏洞"
	for _, tag := range tags {
		if r := rank[strings.ToLower(tag.Severity)]; r > best {
			best, name = r, label[strings.ToLower(tag.Severity)]
		}
	}
	return name
}

// assetFunctionalOf extracts the functional part of a scan summary. The
// importer composes "protocol · status · fingerprint · title"; the two leading
// chunks say how the port was probed, the rest says what it IS. A summary with
// fewer than three chunks has no separable functional part.
func assetFunctionalOf(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	chunks := strings.Split(summary, "·")
	for i := range chunks {
		chunks[i] = strings.TrimSpace(chunks[i])
	}
	if len(chunks) >= 3 {
		return strings.Join(chunks[2:], " · ")
	}
	return summary
}

// GET /api/assets/graph — the project-rooted asset tree.
func (s *Server) assetGraph(w http.ResponseWriter, r *http.Request) {
	out, err := s.assetGraphData()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) assetGraphData() (AssetGraph, error) {
	rows, unlinked := s.assetTable()
	out := AssetGraph{
		Root: assetRootID,
		// Empty lists marshal as [] not null: the console iterates both on
		// every load, and an empty project must read as one root card, not a
		// broken response.
		Nodes:         []AssetGraphNode{},
		Edges:         []AssetGraphEdge{},
		Totals:        map[string]int{},
		UnlinkedVulns: unlinked,
	}
	for _, row := range rows {
		out.Totals[assetFenceState(row.Fence)]++
	}
	name := ""
	if s.Project != nil {
		name = strings.TrimSpace(s.Project.Name)
	}
	if name == "" {
		name = "项目"
	}
	out.Nodes = append(out.Nodes, AssetGraphNode{ID: assetRootID, Kind: "project", Label: name})

	db := s.Store.Snapshot()
	hostFence := map[string]string{}
	for _, a := range db.Assets {
		if a.Kind != model.AssetKindDomain && a.Kind != model.AssetKindHost {
			continue
		}
		hostFence[strings.ToLower(a.Key)] = assetFenceState(a.Fence)
	}

	// Declared interfaces: one ip:port per service fact, with the scan's own
	// functional summary.
	type iface struct {
		port    string
		summary string
	}
	interfacesByHost := map[string][]iface{}
	for _, f := range db.Facts {
		if f.Unusable() || f.Kind != "service" {
			continue
		}
		host, port, ok := strings.Cut(f.Title, ":")
		if !ok || host == "" || port == "" {
			continue
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		interfacesByHost[host] = append(interfacesByHost[host], iface{port: port, summary: strings.TrimSpace(f.Summary)})
	}
	for host := range interfacesByHost {
		list := interfacesByHost[host]
		sort.Slice(list, func(i, j int) bool { return list[i].port < list[j].port })
		interfacesByHost[host] = list
	}

	// Vulnerability tags by exact target.
	hostTags := map[string][]AssetGraphVulnTag{}
	serviceTags := map[string][]AssetGraphVulnTag{}
	if s.VulnEntries != nil {
		for _, entry := range s.VulnEntries() {
			kind, key := assetVulnTarget(entry.EntityRef)
			tag := AssetGraphVulnTag{ID: entry.ID, Severity: entry.Severity, Title: entry.Title}
			switch kind {
			case "host":
				hostTags[key] = append(hostTags[key], tag)
			case "service":
				serviceTags[key] = append(serviceTags[key], tag)
			}
		}
	}

	addEdge := func(source, target, kind string) {
		out.Edges = append(out.Edges, AssetGraphEdge{
			ID: "e:" + source + ">" + target, Source: source, Target: target, Kind: kind,
		})
	}
	// attachHostWorld appends one host's interfaces and captured URLs beneath
	// the parent node (a domain row's own key parents them on the row itself).
	// An interface's own tested reading comes last: its tags conclude it, the
	// requests of the URLs it served reached it, and anything else is untouched.
	attachHostWorld := func(parentID, host string) {
		type svcRef struct {
			nodeIdx  int
			port     string
			requests int
			tagged   bool
		}
		var svcs []svcRef
		for _, it := range interfacesByHost[host] {
			tags := serviceTags[host+":"+it.port]
			node := AssetGraphNode{
				ID:       assetServiceNodeID(host + ":" + it.port),
				Kind:     "service",
				Label:    ":" + it.port,
				Sub:      it.summary,
				Host:     host,
				Vulns:    len(tags),
				VulnTags: tags,
			}
			if fn := assetFunctionalOf(it.summary); fn != "" {
				node.Label = ":" + it.port + " " + fn
			}
			if sev := assetSevLabel(tags); sev != "" {
				node.Label += " 〔" + sev + "〕"
			}
			out.Nodes = append(out.Nodes, node)
			addEdge(parentID, node.ID, assetEdgeContains)
			svcs = append(svcs, svcRef{nodeIdx: len(out.Nodes) - 1, port: it.port, tagged: len(tags) > 0})
		}
		for _, u := range s.assetTraffic.URLs(s.CaseRoot, host) {
			parsed, err := url.Parse(u.URL)
			if err != nil || parsed.Host == "" {
				continue
			}
			node := AssetGraphNode{
				ID:       assetURLNodeID(u.URL),
				Kind:     "url",
				Label:    parsed.Path,
				Sub:      u.URL,
				URL:      u.URL,
				Host:     host,
				Requests: u.Requests,
			}
			if node.Label == "" {
				node.Label = "/"
			}
			out.Nodes = append(out.Nodes, node)
			parent := parentID
			if port := parsed.Port(); port != "" {
				for i := range svcs {
					if svcs[i].port == port {
						parent = assetServiceNodeID(host + ":" + port)
						svcs[i].requests += u.Requests
						break
					}
				}
			}
			addEdge(parent, node.ID, assetEdgeContains)
		}
		for _, svc := range svcs {
			out.Nodes[svc.nodeIdx].Requests = svc.requests
			out.Nodes[svc.nodeIdx].Tested = testedOfNode(svc.requests, len(out.Nodes[svc.nodeIdx].VulnTags))
		}
	}

	// Column 1: one node per asset row, straight off the root card. The tree
	// reads top-down by kind: domains, then standalone IPs, then CIDR rows;
	// keys alphabetical inside each group so the map never reshuffles it.
	rank := map[string]int{
		model.AssetKindDomain: 0,
		model.AssetKindHost:   1,
		model.AssetKindRange:  2,
	}
	sortedRows := append([]AssetRow(nil), rows...)
	sort.SliceStable(sortedRows, func(i, j int) bool {
		if rank[sortedRows[i].Kind] != rank[sortedRows[j].Kind] {
			return rank[sortedRows[i].Kind] < rank[sortedRows[j].Kind]
		}
		return sortedRows[i].Key < sortedRows[j].Key
	})
	for _, row := range sortedRows {
		node := AssetGraphNode{
			ID:       assetRowNodeID(row.Key),
			Kind:     row.Kind,
			Label:    row.Key,
			Fence:    row.Fence,
			Tested:   row.Tested,
			AssetID:  row.AssetID,
			Vulns:    row.Vulns,
			Requests: row.Requests,
			URLs:     row.URLs,
			VulnTags: hostTags[row.Key],
		}
		out.Nodes = append(out.Nodes, node)
		addEdge(assetRootID, node.ID, assetEdgeContains)
		if row.Kind == model.AssetKindRange {
			continue
		}
		// Column 2: the hosts observed under this domain. The apex host repeats
		// the row key, so its world parents on the row node itself.
		hosts := append([]string(nil), row.Hosts...)
		sort.Strings(hosts)
		for _, host := range hosts {
			if host == row.Key {
				continue
			}
			requests := 0
			if hts, _ := s.assetTraffic.Hosts(s.CaseRoot); hts[host] != nil {
				requests = hts[host].Requests
			}
			tags := hostTags[host]
			hostNode := AssetGraphNode{
				ID:       assetHostNodeID(host),
				Kind:     "host",
				Label:    host,
				Host:     host,
				Fence:    hostFence[host],
				Requests: requests,
				Vulns:    len(tags),
				VulnTags: tags,
				Tested:   testedOfNode(requests, len(tags)),
			}
			out.Nodes = append(out.Nodes, hostNode)
			addEdge(assetRowNodeID(row.Key), hostNode.ID, assetEdgeContains)
			attachHostWorld(hostNode.ID, host)
		}
		attachHostWorld(assetRowNodeID(row.Key), row.Key)
	}
	return out, nil
}

// testedOfNode is the per-node reading of the table's three honest states: a
// library entry naming the node concludes it; observed requests reached it;
// anything else is untouched.
func testedOfNode(requests, vulns int) string {
	switch {
	case vulns > 0:
		return "concluded"
	case requests > 0:
		return "reached"
	default:
		return "untouched"
	}
}
