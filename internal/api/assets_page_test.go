package api

import (
	"encoding/json"
	"net/http"
	"testing"

	native "github.com/c1ayooo/4zrcin/internal/aop/traffic"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/traffic"
	"github.com/c1ayooo/4zrcin/internal/vulnlib"
)

// assetPageServer seeds one project with a fence-seeded domain, a pending
// host, a CIDR range, captured traffic across two subdomains and an IP, one
// declared host/service fact, and three library entries (two linked, one
// unlinked).
func assetPageServer(t *testing.T) (*Server, *project.Project) {
	t.Helper()
	s, p := newBindingServer(t)
	vulns := []vulnlib.Entry{
		{ID: "gv_1", Title: "越权", EntityRef: "host:www.example.com", Status: "verified", Severity: "high"},
		{ID: "gv_2", Title: "注入", EntityRef: "api.example.com", Status: "unverified"},
		{ID: "gv_3", Title: "历史漏洞", EntityRef: "", Status: "unverified"},
		{ID: "gv_4", Title: "账户维度", EntityRef: "account:uid=2", Status: "unverified"},
		{ID: "gv_5", Title: "带端口", EntityRef: "host:10.0.0.5:8080", Status: "unverified"},
	}
	s.VulnEntries = func() []vulnlib.Entry { return vulns }
	rules, err := project.NormalizeScopeRules(project.ScopeRules{Enabled: true, DefaultAction: project.ScopeActionReview, Rules: []project.ScopeRule{
		{ID: "sr-1", Action: project.ScopeActionRecord, Method: "GET", Host: "example.com", PathPrefix: "/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p.ScopeRuleSet = &rules

	for _, a := range []model.Asset{
		{Kind: model.AssetKindDomain, Key: "example.com", Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
		{Kind: model.AssetKindDomain, Key: "pending.example", Target: true, Fence: model.AssetFencePending, Source: model.AssetSourceObserved},
		{Kind: model.AssetKindRange, Key: "10.0.0.0/8", Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
	} {
		if _, _, err := s.Store.UpsertAsset(a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Store.AddFact(model.Fact{Kind: "host", Title: "10.0.0.5", Confidence: "observed", Status: "unverified"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.AddFact(model.Fact{Kind: "service", Title: "10.0.0.5:8080", Confidence: "observed", Status: "unverified"}); err != nil {
		t.Fatal(err)
	}

	flow := func(id, url string, status int32, ack, rule string, missed bool) {
		if _, err := traffic.SaveCapture(s.CaseRoot, "run-"+id, "in_"+id, "sess", "ev-"+id, &native.Flow{
			Id: id, Complete: true,
			Request:  &native.HttpRequest{Method: "GET", Url: url},
			Response: &native.HttpResponse{StatusCode: status},
		}, nil, ack, rule, missed); err != nil {
			t.Fatal(err)
		}
	}
	flow("f1", "https://www.example.com/a", 200, "record", "sr-1", false)
	flow("f2", "https://api.example.com/b", 404, "record", "sr-1", false)
	flow("f3", "http://10.0.0.5/x", 200, "", "", false)
	return s, p
}

type assetTableResponse struct {
	Rows          []AssetRow `json:"rows"`
	UnlinkedVulns int        `json:"unlinked_vulns"`
	Total         int        `json:"total"`
}

// The table groups observed hosts by registrable domain, unions the seeded
// assets (an untouched fenced domain still shows), and reads the three test
// states honestly: untouched < reached < concluded (a library entry names the
// host). An unlinked library entry is counted apart, never guessed onto a row.
func TestAssetTableProjection(t *testing.T) {
	s, _ := assetPageServer(t)
	rec := call(t, s, http.MethodGet, "/api/assets/table", nil)
	if rec.Code != 200 {
		t.Fatalf("table = %d %s", rec.Code, rec.Body.String())
	}
	var out assetTableResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byKey := map[string]AssetRow{}
	for _, r := range out.Rows {
		byKey[r.Key] = r
	}
	domain, ok := byKey["example.com"]
	if !ok {
		t.Fatalf("example.com row missing: %+v", out.Rows)
	}
	if domain.Kind != model.AssetKindDomain || domain.Fence != model.AssetFenceIn || domain.AssetID == "" {
		t.Fatalf("domain row = %+v", domain)
	}
	// Three hosts: the two observed subdomains plus the apex the fence row
	// named (a seeded domain asset is a host too).
	if domain.HostCount != 3 || domain.Requests != 2 || domain.URLs != 2 {
		t.Fatalf("domain aggregate = hosts %d requests %d urls %d", domain.HostCount, domain.Requests, domain.URLs)
	}
	if !containsString(domain.Hosts, "example.com") {
		t.Fatalf("apex host missing from the row: %+v", domain.Hosts)
	}
	if domain.Vulns != 2 || domain.Tested != "concluded" {
		t.Fatalf("domain vulns/tested = %d/%q", domain.Vulns, domain.Tested)
	}
	if !containsString(domain.Hosts, "www.example.com") || !containsString(domain.Hosts, "api.example.com") {
		t.Fatalf("domain hosts = %+v", domain.Hosts)
	}
	if ip := byKey["10.0.0.5"]; ip.Kind != model.AssetKindHost || ip.Requests != 1 || ip.Tested != "reached" {
		t.Fatalf("ip row = %+v", ip)
	}
	if pending := byKey["pending.example"]; pending.Fence != model.AssetFencePending || pending.Tested != "untouched" {
		t.Fatalf("pending row = %+v", pending)
	}
	if rng := byKey["10.0.0.0/8"]; rng.Kind != model.AssetKindRange || rng.Tested != "untouched" {
		t.Fatalf("range row = %+v", rng)
	}
	// Unlinked: the empty ref, the account identity and the port-carrying ref
	// — none of them names a bare host, so none invents a row.
	if out.UnlinkedVulns != 3 {
		t.Fatalf("unlinked = %d, want 3", out.UnlinkedVulns)
	}
}

// The drill-down: the row, its hosts with their own fence standing, the
// per-URL rows (with the newest verdict) and the entries grouped under it.
func TestAssetDetailProjection(t *testing.T) {
	s, _ := assetPageServer(t)
	rec := call(t, s, http.MethodGet, "/api/assets/detail?key=example.com", nil)
	if rec.Code != 200 {
		t.Fatalf("detail = %d %s", rec.Code, rec.Body.String())
	}
	var out AssetDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Row.Key != "example.com" || len(out.Hosts) != 3 {
		t.Fatalf("detail row/hosts = %+v / %+v", out.Row, out.Hosts)
	}
	if len(out.URLs) != 2 {
		t.Fatalf("urls = %+v", out.URLs)
	}
	verdicts := 0
	for _, u := range out.URLs {
		if u.Verdict != "record" || u.RuleID != "sr-1" {
			continue
		}
		// The rule's own text rides the row: the operator reads what matched,
		// not just the id.
		if u.RuleText != "record GET example.com /" {
			t.Fatalf("rule text = %q", u.RuleText)
		}
		verdicts++
	}
	if verdicts != 2 {
		t.Fatalf("url verdicts = %+v", out.URLs)
	}
	if len(out.Vulns) != 2 {
		t.Fatalf("vulns = %+v", out.Vulns)
	}
	for _, v := range out.Vulns {
		if v.EntityRef == "" {
			t.Fatalf("an unlinked entry leaked into the row's vulns: %+v", v)
		}
	}
	if rec := call(t, s, http.MethodGet, "/api/assets/detail?key=missing.example", nil); rec.Code != 404 {
		t.Fatalf("unknown row = %d", rec.Code)
	}
}

// The unlinked list names the entries the page refuses to place: no ref, an
// account identity and a port-carrying ref are all "not a host".
func TestUnlinkedVulnsEndpoint(t *testing.T) {
	s, _ := assetPageServer(t)
	rec := call(t, s, http.MethodGet, "/api/assets/unlinked-vulns", nil)
	if rec.Code != 200 {
		t.Fatalf("unlinked = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Vulns []vulnlib.Entry `json:"vulns"`
		Count int             `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 3 || len(out.Vulns) != 3 {
		t.Fatalf("unlinked = %d %+v", out.Count, out.Vulns)
	}
	for _, v := range out.Vulns {
		if vulnEntityHost(v.EntityRef) != "" {
			t.Fatalf("a linked entry leaked into the unlinked list: %+v", v)
		}
	}
}
