package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
)

// importFenceServer is the fence fixture with a table that admits example.com
// (and its subdomains) and 10.0.0.0/8; 192.168.1.9 and other.example fall
// outside it.
func importFenceServer(t *testing.T) (*Server, *project.Project) {
	t.Helper()
	s, p := fenceFlowServer(t)
	rules, err := project.NormalizeScopeRules(project.ScopeRules{
		Enabled: true, DefaultAction: project.ScopeActionReview,
		Rules: []project.ScopeRule{
			{ID: "sr-domain", Action: project.ScopeActionRecord, Method: "*", Host: "example.com", PathPrefix: "/"},
			{ID: "sr-net", Action: project.ScopeActionRecord, Method: "*", Host: "10.0.0.0/8", PathPrefix: "/"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.ScopeRuleSet = &rules
	return s, p
}

const assetScanFixture = `{"ip":"10.1.2.3","port":"80","protocol":"http","status":"open"}
{"ip":"10.1.2.3","port":"443","protocol":"https","status":"open"}
{"ip":"192.168.1.9","port":"22","protocol":"ssh","status":"open"}
`

const harFixture = `{"log":{"version":"1.2","creator":{"name":"test","version":"1"},"entries":[
 {"startedDateTime":"2026-01-01T00:00:00Z","request":{"method":"GET","url":"https://www.example.com/a"},"response":{"status":200},"serverIPAddress":"10.1.2.3"},
 {"startedDateTime":"2026-01-01T00:00:01Z","request":{"method":"GET","url":"https://other.example/x"},"response":{"status":404}},
 {"startedDateTime":"2026-01-01T00:00:02Z","request":{"method":"POST","url":"https://192.168.1.9:8443/y"},"response":{"status":200}}
]}}`

func writeFixture(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func readPreview(t *testing.T, s *Server, path string, body any) ImportPreview {
	t.Helper()
	rec := call(t, s, http.MethodPost, path, body)
	if rec.Code != 200 {
		t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
	}
	var out ImportPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The scan preview groups an address by its own identity: the CIDR row admits
// 10.1.2.3, the address outside both rows is out of fence, and nothing lands
// while the operator is just looking twice.
func TestAssetImportPreviewGroupsWithoutWriting(t *testing.T) {
	s, p := importFenceServer(t)
	name := writeFixture(t, s.CaseRoot, "scan.jsonl", assetScanFixture)
	before := len(s.Store.ListFacts("", 0))
	rulesBefore, _ := json.Marshal(p.ScopeRuleSet)

	first := readPreview(t, s, "/api/assets/import/preview", map[string]string{"path": name})
	second := readPreview(t, s, "/api/assets/import/preview", map[string]string{"path": name})

	if first.Kind != "assets" || first.Records != 3 || first.Hosts != 2 {
		t.Fatalf("preview = %+v", first)
	}
	if first.InFence != 1 || first.OutOfFence != 1 || first.Unjudged != 0 || !first.FenceEnabled {
		t.Fatalf("groups = in %d out %d unjudged %d enabled %v", first.InFence, first.OutOfFence, first.Unjudged, first.FenceEnabled)
	}
	items := map[string]ImportPreviewItem{}
	for _, item := range first.Items {
		items[item.Host] = item
	}
	in := items["10.1.2.3"]
	if in.Classification != "in_fence" || in.RuleID != "sr-net" || in.Ports != 2 || in.Kind != model.AssetKindHost {
		t.Fatalf("in-fence item = %+v", in)
	}
	if !containsString([]string{in.RuleText}, "record * 10.0.0.0/8 /") {
		t.Fatalf("rule text = %q", in.RuleText)
	}
	if out := items["192.168.1.9"]; out.Classification != "out_of_fence" || out.Ports != 1 {
		t.Fatalf("out item = %+v", out)
	}
	// The second read is the same answer: the preview holds no state.
	if a, b := string(mustJSON(t, first)), string(mustJSON(t, second)); a != b {
		t.Fatalf("preview is not idempotent:\n%s\n%s", a, b)
	}
	if after := len(s.Store.ListFacts("", 0)); after != before {
		t.Fatalf("preview wrote %d facts", after-before)
	}
	if rulesAfter, _ := json.Marshal(p.ScopeRuleSet); string(rulesAfter) != string(rulesBefore) {
		t.Fatalf("preview changed the rule table: %s", rulesAfter)
	}
	if len(s.Store.AssetsByFence(model.AssetFenceIn)) != 0 {
		t.Fatalf("preview wrote assets")
	}
}

// A project without an enabled fence cannot judge: the group is 无法判定, not
// "outside", and no host can be checked into the fence from the preview.
func TestAssetImportPreviewUnjudgedWithoutFence(t *testing.T) {
	s, _ := importFenceServer(t)
	rules := project.ScopeRules{Enabled: false, DefaultAction: project.ScopeActionReview}
	s.Project.ScopeRuleSet = &rules
	name := writeFixture(t, s.CaseRoot, "scan.jsonl", assetScanFixture)
	out := readPreview(t, s, "/api/assets/import/preview", map[string]string{"path": name})
	if out.Unjudged != 2 || out.InFence != 0 || out.OutOfFence != 0 || out.FenceEnabled {
		t.Fatalf("unjudged preview = %+v", out)
	}
}

// The HAR preview judges hostnames: the domain row admits www.example.com, the
// sibling domain and the literal address stay outside, and the port is kept as
// a count rather than judged.
func TestTrafficImportPreviewGroupsByHostname(t *testing.T) {
	s, _ := importFenceServer(t)
	name := writeFixture(t, s.CaseRoot, "capture.har", harFixture)
	out := readPreview(t, s, "/api/traffic/import/preview", map[string]string{"path": name})
	if out.Kind != "traffic" || out.Records != 3 || out.Hosts != 3 {
		t.Fatalf("preview = %+v", out)
	}
	if out.InFence != 1 || out.OutOfFence != 2 {
		t.Fatalf("groups = %+v", out)
	}
	items := map[string]ImportPreviewItem{}
	for _, item := range out.Items {
		items[item.Host] = item
	}
	if item := items["www.example.com"]; item.Classification != "in_fence" || item.Requests != 1 {
		t.Fatalf("www = %+v", item)
	}
	if item := items["other.example"]; item.Classification != "out_of_fence" {
		t.Fatalf("other = %+v", item)
	}
	ip := items["192.168.1.9"]
	if ip.Classification != "out_of_fence" || ip.Ports != 1 || ip.Kind != model.AssetKindHost {
		t.Fatalf("ip = %+v", ip)
	}
}

// Checking an out-of-fence host writes its allow row and its asset first, then
// imports the whole batch; checking a host the preview did not list is refused.
func TestImportConfirmAddsCheckedHostsToFence(t *testing.T) {
	s, p := importFenceServer(t)
	name := writeFixture(t, s.CaseRoot, "scan.jsonl", assetScanFixture)

	rec := call(t, s, http.MethodPost, "/api/assets/import", map[string]any{
		"path": name, "fence_hosts": []string{"192.168.1.9"},
	})
	if rec.Code != 200 {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["fence_added"] != float64(1) {
		t.Fatalf("receipt = %v", body)
	}
	asset, ok := s.Store.AssetByKindKey(model.AssetKindHost, "192.168.1.9")
	if !ok || asset.Fence != model.AssetFenceIn || asset.Source != model.AssetSourceFence {
		t.Fatalf("asset = %+v ok=%v", asset, ok)
	}
	if p.ScopeRuleSet == nil || p.ScopeRuleSet.Rules[0].Host != "192.168.1.9" ||
		p.ScopeRuleSet.Rules[0].Action != project.ScopeActionRecord {
		t.Fatalf("allow row was not prepended: %+v", p.ScopeRuleSet)
	}
	// The batch still landed: the two addresses produced service facts.
	if len(s.Store.ListFacts("", 0)) == 0 {
		t.Fatal("import wrote no facts")
	}

	// A host outside the preview's out-of-fence set cannot be smuggled in.
	before := len(p.ScopeRuleSet.Rules)
	rec = call(t, s, http.MethodPost, "/api/assets/import", map[string]any{
		"path": name, "fence_hosts": []string{"evil.example"},
	})
	if rec.Code != 400 {
		t.Fatalf("unlisted host = %d %s", rec.Code, rec.Body.String())
	}
	if len(p.ScopeRuleSet.Rules) != before {
		t.Fatal("a refused selection still wrote a rule")
	}
}

// No selection keeps the legacy import: the same receipt fields, no rule
// change, no new assets.
func TestImportWithoutSelectionKeepsLegacyBehavior(t *testing.T) {
	s, p := importFenceServer(t)
	name := writeFixture(t, s.CaseRoot, "scan.jsonl", assetScanFixture)
	rulesBefore, _ := json.Marshal(p.ScopeRuleSet)
	beforeAssets := len(s.Store.AssetsByFence(model.AssetFenceIn))

	rec := call(t, s, http.MethodPost, "/api/assets/import", map[string]any{"path": name})
	if rec.Code != 200 {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body["fence_added"]; present {
		t.Fatalf("no selection added a fence field: %v", body)
	}
	if body["records"] != float64(3) || body["hosts"] != float64(2) {
		t.Fatalf("receipt changed: %v", body)
	}
	if rulesAfter, _ := json.Marshal(p.ScopeRuleSet); string(rulesAfter) != string(rulesBefore) {
		t.Fatalf("no-selection import changed rules: %s", rulesAfter)
	}
	if got := len(s.Store.AssetsByFence(model.AssetFenceIn)); got != beforeAssets {
		t.Fatalf("no-selection import wrote %d fence assets", got-beforeAssets)
	}
}

// The HAR confirm uses the same guard: selecting the IP adds it before the
// capture's facts land.
func TestTrafficImportConfirmAddsHostToFence(t *testing.T) {
	s, p := importFenceServer(t)
	name := writeFixture(t, s.CaseRoot, "capture.har", harFixture)
	rec := call(t, s, http.MethodPost, "/api/traffic/import", map[string]any{
		"path": name, "fence_hosts": []string{"192.168.1.9"},
	})
	if rec.Code != 200 {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	if p.ScopeRuleSet == nil || p.ScopeRuleSet.Rules[0].Host != "192.168.1.9" {
		t.Fatalf("rules = %+v", p.ScopeRuleSet)
	}
	if _, ok := s.Store.AssetByKindKey(model.AssetKindHost, "192.168.1.9"); !ok {
		t.Fatal("the checked host did not land as an asset")
	}
	if len(s.Store.ListFacts("", 0)) == 0 {
		t.Fatal("the capture's facts did not land")
	}
}
