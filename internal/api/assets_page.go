package api

// The asset page's read model (spec #1 工单6/#7): one row per domain (the
// registrable domain grouping), with the observed hosts, traffic shape, fence
// standing, vulnerability count and the honest three-state test reading. The
// row universe is the union of the fence-seeded/imported assets, the traffic
// captures and the declared facts — a domain the operator fenced but never
// touched still appears (未触达), because the page's first job is to show what
// the engagement declared, not only what it happened to observe.
//
// "Tested" has exactly three honest values: untouched (no captured request),
// reached (requests happened, which intents are listed) and concluded (a
// vulnerability entry names the host). There is no "tested" flag to invent.

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/traffic"
	"github.com/c1ayooo/4zrcin/internal/vulnlib"
)

// AssetRow is one domain/range row of the asset table.
type AssetRow struct {
	Key  string `json:"key"`
	Kind string `json:"kind"` // domain | host | range
	// AssetID/Fence are the stored asset row's identity and standing when one
	// exists; empty fence means no asset record yet (未登记), never "out of
	// fence" — the control plane cannot judge the rule table here.
	AssetID string `json:"asset_id,omitempty"`
	Fence   string `json:"fence,omitempty"`
	Target  *bool  `json:"target,omitempty"`
	// Hosts are the observed and seeded hosts under the row.
	Hosts     []string       `json:"hosts"`
	HostCount int            `json:"host_count"`
	URLs      int            `json:"urls"`
	Requests  int            `json:"requests"`
	Statuses  map[string]int `json:"statuses,omitempty"`
	Vulns     int            `json:"vulns"`
	// Tested is untouched | reached | concluded (see the package comment).
	Tested   string     `json:"tested"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

// AssetHostDetail is one host under a row, with its own fence standing and
// traffic shape.
type AssetHostDetail struct {
	Host     string         `json:"host"`
	AssetID  string         `json:"asset_id,omitempty"`
	Fence    string         `json:"fence,omitempty"`
	URLs     int            `json:"urls"`
	Requests int            `json:"requests"`
	Statuses map[string]int `json:"statuses,omitempty"`
	Intents  []string       `json:"intents,omitempty"`
	LastSeen *time.Time     `json:"last_seen,omitempty"`
	Vulns    int            `json:"vulns"`
}

// AssetDetail is the drill-down: the row, its hosts, the per-URL rows of the
// selected host (or the union over the row's hosts) and the vulnerability
// entries grouped under it.
type AssetDetail struct {
	Row   AssetRow             `json:"row"`
	Hosts []AssetHostDetail    `json:"hosts"`
	URLs  []traffic.URLTraffic `json:"urls"`
	Vulns []vulnlib.Entry      `json:"vulns"`
	// EvidenceIDs are the row's own stored evidence references (fence/import
	// seeds attach them); empty means none were recorded.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// fenceWeight orders the fence standings by operator attention.
func fenceWeight(fence string) int {
	switch fence {
	case model.AssetFencePending:
		return 3
	case model.AssetFenceDenied:
		return 2
	case model.AssetFenceIn:
		return 1
	default:
		return 0
	}
}

// assetRowKey is the registrable domain of an observed host, or the host
// itself when it is an IP literal or has no registrable form.
func assetRowKey(host string) (key, kind string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", ""
	}
	if isIPLiteral(host) {
		return host, model.AssetKindHost
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && domain != "" {
		return domain, model.AssetKindDomain
	}
	return host, model.AssetKindDomain
}

func isIPLiteral(host string) bool {
	return model.IsIPLiteral(host)
}

// vulnEntityHost normalizes a library entry's entity_ref to a bare host: a
// declaration may write "host:example.com" or the bare host alike. The
// convention also carries non-host identities ("account:uid=2" — see the
// declaration prompt), so only a value that IS a hostname/IP after the
// optional prefix names a host; anything still carrying a colon (a port, an
// account id) stays unlinked rather than inventing a phantom row.
func vulnEntityHost(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	ref = strings.TrimPrefix(ref, "host:")
	ref = strings.TrimSuffix(ref, "/")
	if ref == "" || strings.ContainsAny(ref, " :/?#@[]") {
		return ""
	}
	for _, label := range strings.Split(ref, ".") {
		if label == "" {
			return ""
		}
	}
	return ref
}

// assetTable assembles the table rows and the unlinked vulnerability count.
func (s *Server) assetTable() ([]AssetRow, int) {
	db := s.Store.Snapshot()
	hosts, _ := s.assetTraffic.Hosts(s.CaseRoot)

	rows := map[string]*AssetRow{}
	hostVulns := map[string]int{}

	var vulns []vulnlib.Entry
	if s.VulnEntries != nil {
		vulns = s.VulnEntries()
	}
	unlinked := 0
	for _, entry := range vulns {
		host := vulnEntityHost(entry.EntityRef)
		if host == "" {
			unlinked++
			continue
		}
		hostVulns[host]++
	}

	row := func(key, kind string) *AssetRow {
		r := rows[key]
		if r == nil {
			r = &AssetRow{Key: key, Kind: kind, Statuses: map[string]int{}}
			rows[key] = r
		}
		return r
	}
	addHost := func(host string) *AssetRow {
		key, kind := assetRowKey(host)
		if key == "" {
			return nil
		}
		r := row(key, kind)
		if !slices.Contains(r.Hosts, host) {
			r.Hosts = append(r.Hosts, host)
		}
		return r
	}

	// 1) The fence/import-seeded assets: they define the row universe even
	//    before any traffic exists.
	for _, a := range db.Assets {
		switch a.Kind {
		case model.AssetKindRange:
			r := row(a.Key, model.AssetKindRange)
			r.AssetID, r.Fence = a.ID, a.Fence
			target := a.Target
			r.Target = &target
		case model.AssetKindDomain, model.AssetKindHost:
			r := addHost(a.Key)
			if r == nil {
				continue
			}
			// One row can hold the domain's own asset and host assets; show the
			// standing that needs the operator most (pending > denied >
			// in_fence), not whichever row was read last.
			if fenceWeight(a.Fence) > fenceWeight(r.Fence) || r.AssetID == "" {
				r.AssetID, r.Fence = a.ID, a.Fence
			}
			target := a.Target
			r.Target = &target
		case model.AssetKindService:
			host, _, ok := strings.Cut(a.Key, ":")
			if ok {
				addHost(host)
			}
		}
	}

	// 2) Observed traffic: hosts and their shape attach to their row.
	for host, ht := range hosts {
		r := addHost(host)
		if r == nil {
			continue
		}
		r.URLs += ht.URLs
		r.Requests += ht.Requests
		for bucket, n := range ht.Statuses {
			r.Statuses[bucket] += n
		}
		if ht.LastSeen.After(timeOrZero(r.LastSeen)) {
			t := ht.LastSeen
			r.LastSeen = &t
		}
	}

	// 3) Declared facts name hosts the captures may not have seen; a service
	//    fact's title is ip:port.
	for _, f := range db.Facts {
		if f.Unusable() {
			continue
		}
		switch f.Kind {
		case "host":
			addHost(f.Title)
		case "service":
			if host, _, ok := strings.Cut(f.Title, ":"); ok {
				addHost(host)
			}
		}
	}

	// 4) Vulnerability counts land on the row owning the named host; a host
	//    the page has no row for still gets one (a declared vuln is a reason
	//    to see the asset).
	for host, n := range hostVulns {
		r := addHost(host)
		if r == nil {
			continue
		}
		r.Vulns += n
	}

	out := make([]AssetRow, 0, len(rows))
	for _, r := range rows {
		sort.Strings(r.Hosts)
		r.HostCount = len(r.Hosts)
		switch {
		case r.Vulns > 0:
			r.Tested = "concluded"
		case r.Requests > 0:
			r.Tested = "reached"
		default:
			r.Tested = "untouched"
		}
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return timeOrZero(out[i].LastSeen).After(timeOrZero(out[j].LastSeen))
	})
	return out, unlinked
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// GET /api/assets/table
func (s *Server) listAssetTable(w http.ResponseWriter, r *http.Request) {
	rows, unlinked := s.assetTable()
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":           rows,
		"unlinked_vulns": unlinked,
		"total":          len(rows),
	})
}

// GET /api/assets/detail?key=<row key>&host=<optional host>
func (s *Server) assetDetail(w http.ResponseWriter, r *http.Request) {
	detail, code, msg := s.assetDetailFor(
		strings.ToLower(strings.TrimSpace(r.URL.Query().Get("key"))),
		strings.ToLower(strings.TrimSpace(r.URL.Query().Get("host"))),
	)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// assetDetailFor is the drill-down projection shared by the JSON endpoint and
// the asset graph: the graph must read the same hosts, URL rows and entries the
// sheet shows, or the two surfaces would disagree about one domain.
func (s *Server) assetDetailFor(key, host string) (AssetDetail, int, string) {
	if key == "" {
		return AssetDetail{}, 400, "需要 key（资产行的域名或主机）"
	}
	db := s.Store.Snapshot()
	rows, _ := s.assetTable()
	var row AssetRow
	found := false
	for _, r := range rows {
		if r.Key == key {
			row, found = r, true
			break
		}
	}
	if !found {
		return AssetDetail{}, 404, "没有这个资产行：" + key
	}
	if host != "" && !slices.Contains(row.Hosts, host) {
		return AssetDetail{}, 404, "该主机不在这行资产里：" + host
	}

	hosts, _ := s.assetTraffic.Hosts(s.CaseRoot)
	hostAssets := map[string]model.Asset{}
	for _, a := range db.Assets {
		if a.Kind == model.AssetKindDomain || a.Kind == model.AssetKindHost {
			hostAssets[strings.ToLower(a.Key)] = a
		}
	}
	hostVulns := map[string]int{}
	var vulns []vulnlib.Entry
	if s.VulnEntries != nil {
		for _, entry := range s.VulnEntries() {
			h := vulnEntityHost(entry.EntityRef)
			if h == "" {
				continue
			}
			hostVulns[h]++
			if slices.Contains(row.Hosts, h) {
				vulns = append(vulns, entry)
			}
		}
	}

	// Empty groups marshal as [] not null: the console reads .length on every
	// group, and a row with no URLs or no entries must read "zero", not
	// "the field is absent" (a nil slice here crashed the drill-down sheet).
	detail := AssetDetail{
		Row:   row,
		Hosts: []AssetHostDetail{},
		URLs:  []traffic.URLTraffic{},
		Vulns: []vulnlib.Entry{},
	}
	if len(vulns) > 0 {
		detail.Vulns = vulns
	}
	urlSeen := map[string]bool{}
	for _, h := range row.Hosts {
		hd := AssetHostDetail{Host: h, Vulns: hostVulns[h]}
		if a, ok := hostAssets[h]; ok {
			hd.AssetID, hd.Fence = a.ID, a.Fence
		}
		if ht := hosts[h]; ht != nil {
			hd.URLs, hd.Requests, hd.Statuses, hd.Intents = ht.URLs, ht.Requests, ht.Statuses, ht.Intents
			if !ht.LastSeen.IsZero() {
				t := ht.LastSeen
				hd.LastSeen = &t
			}
		}
		detail.Hosts = append(detail.Hosts, hd)
		// Per-URL rows: the selected host only when named, else the union
		// over the row's hosts (capped so one row cannot flood the panel).
		if host == "" || host == h {
			for _, u := range s.assetTraffic.URLs(s.CaseRoot, h) {
				if urlSeen[u.URL] {
					continue
				}
				if len(detail.URLs) >= 200 {
					break
				}
				urlSeen[u.URL] = true
				detail.URLs = append(detail.URLs, u)
			}
		}
	}
	sort.SliceStable(detail.URLs, func(i, j int) bool {
		if detail.URLs[i].Requests != detail.URLs[j].Requests {
			return detail.URLs[i].Requests > detail.URLs[j].Requests
		}
		return detail.URLs[i].URL < detail.URLs[j].URL
	})
	// The rule TEXT beside the verdict: the operator reads the row that
	// matched, not just its id.
	ruleText := map[string]string{}
	if rules, ok := s.Project.ScopeRulesOf(); ok {
		for _, r := range rules.Rules {
			ruleText[r.ID] = strings.TrimSpace(r.Action + " " + r.Method + " " + r.Host + " " + r.PathPrefix)
		}
	}
	for i := range detail.URLs {
		if id := detail.URLs[i].RuleID; id != "" {
			detail.URLs[i].RuleText = ruleText[id]
		}
	}
	// The row's stored evidence references (an import names its capture).
	seenEvidence := map[string]bool{}
	for _, a := range db.Assets {
		var key string
		switch a.Kind {
		case model.AssetKindDomain, model.AssetKindHost:
			key = strings.ToLower(a.Key)
		case model.AssetKindService:
			key, _, _ = strings.Cut(strings.ToLower(a.Key), ":")
		default:
			continue
		}
		if !slices.Contains(row.Hosts, key) {
			continue
		}
		for _, id := range a.EvidenceIDs {
			if !seenEvidence[id] {
				seenEvidence[id] = true
				detail.EvidenceIDs = append(detail.EvidenceIDs, id)
			}
		}
	}
	return detail, 0, ""
}

// GET /api/assets/unlinked-vulns lists the library entries that name no host
// (the 未关联资产 group): the declaration carried no entity_ref, or one that
// names something else (an account). The page never guesses a row for them.
func (s *Server) unlinkedVulns(w http.ResponseWriter, r *http.Request) {
	entries := []vulnlib.Entry{}
	if s.VulnEntries != nil {
		for _, entry := range s.VulnEntries() {
			if vulnEntityHost(entry.EntityRef) == "" {
				entries = append(entries, entry)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vulns": entries, "count": len(entries)})
}
