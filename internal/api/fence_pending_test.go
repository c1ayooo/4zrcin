package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	native "github.com/c1ayooo/4zrcin/internal/aop/traffic"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/traffic"
)

// fenceFlowServer is the binding fixture plus the manifest writer the fence
// membership flows call (the hub supplies the real one in production).
func fenceFlowServer(t *testing.T) (*Server, *project.Project) {
	t.Helper()
	s, p := newBindingServer(t)
	s.PersistScopeRules = func(rules *project.ScopeRules) (project.ScopeRules, error) {
		normalized, err := project.NormalizeScopeRules(*rules)
		if err != nil {
			return project.ScopeRules{}, err
		}
		p.ScopeRuleSet = &normalized
		return normalized, nil
	}
	return s, p
}

func refusedOutOfRange(url string) traffic.Capture {
	return traffic.Capture{URL: url, GuardrailAction: "review", GuardrailDefaultMissed: true}
}

// The first refusal of an out-of-range host records the pending asset, parks
// the stop intent and activates the asset-scoped instruction; every later
// refusal of the same host is quiet (one decision per identity, no duplicate
// guidance), and an in-fence/denied asset is never re-queued.
func TestFenceOutTriggerRecordsOnce(t *testing.T) {
	s, _ := fenceFlowServer(t)
	if err := s.observeFenceOut(refusedOutOfRange("https://new.example/api/users")); err != nil {
		t.Fatal(err)
	}
	rows := s.Store.AssetsByFence(model.AssetFencePending)
	if len(rows) != 1 || rows[0].Kind != model.AssetKindDomain || rows[0].Key != "new.example" || !rows[0].Target {
		t.Fatalf("pending rows = %+v", rows)
	}
	instructions := s.Store.InstructionsForAsset("new.example")
	if len(instructions) != 1 || instructions[0].State != model.InstructionActive {
		t.Fatalf("guidance = %+v", instructions)
	}
	parked := 0
	for _, in := range s.Store.ListIntents() {
		if in.Status == model.StatusParked {
			parked++
			if in.Title != "不再测试这个资产：new.example" {
				t.Fatalf("parked marker title = %q", in.Title)
			}
		}
	}
	if parked != 1 {
		t.Fatalf("parked intents = %d, want 1", parked)
	}

	// Idempotent: same host again, and a subdomain is its own identity.
	if err := s.observeFenceOut(refusedOutOfRange("https://new.example/other")); err != nil {
		t.Fatal(err)
	}
	if rows := s.Store.AssetsByFence(model.AssetFencePending); len(rows) != 1 {
		t.Fatalf("repeat trigger duplicated the asset: %+v", rows)
	}
	if got := len(s.Store.InstructionsForAsset("new.example")); got != 1 {
		t.Fatalf("repeat trigger duplicated guidance: %d", got)
	}

	// An in-fence asset's refusal (stale-rule round) does not re-queue.
	if _, _, err := s.Store.UpsertAsset(model.Asset{Kind: model.AssetKindDomain, Key: "allowed.example",
		Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence}); err != nil {
		t.Fatal(err)
	}
	if err := s.observeFenceOut(refusedOutOfRange("https://allowed.example/x")); err != nil {
		t.Fatal(err)
	}
	if rows := s.Store.AssetsByFence(model.AssetFencePending); len(rows) != 1 {
		t.Fatalf("in-fence asset re-queued: %+v", rows)
	}
}

type fencePendingResponse struct {
	Pending []model.Asset `json:"pending"`
	Count   int           `json:"count"`
}

func TestFencePendingResolveApprove(t *testing.T) {
	s, p := fenceFlowServer(t)
	if err := s.observeFenceOut(refusedOutOfRange("https://new.example/x")); err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, http.MethodGet, "/api/fence-pending", nil)
	if rec.Code != 200 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	var list fencePendingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Pending[0].Key != "new.example" {
		t.Fatalf("pending = %+v", list)
	}

	rec = call(t, s, http.MethodPost, "/api/fence-pending/resolve", map[string]any{
		"ids": []string{list.Pending[0].ID}, "approve": true,
	})
	if rec.Code != 200 {
		t.Fatalf("resolve = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Results []fencePendingResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Error != "" || out.Results[0].Fence != model.AssetFenceIn {
		t.Fatalf("results = %+v", out.Results)
	}
	// The fence now allowlists the host with the fixed fallback, the stop
	// guidance is revoked and a continue intent is queued.
	rules := p.ScopeRuleSet
	if rules == nil || !rules.Enabled || rules.DefaultAction != project.ScopeActionReview {
		t.Fatalf("scope rules = %+v", rules)
	}
	found := false
	for _, r := range rules.Rules {
		if r.Host == "new.example" && r.Action == project.ScopeActionRecord {
			found = true
		}
	}
	if !found {
		t.Fatalf("no allow row for the joined asset: %+v", rules.Rules)
	}
	for _, in := range s.Store.InstructionsForAsset("new.example") {
		if in.State == model.InstructionActive && in.IdempotencyKey == fenceOutStopKey(model.AssetKindDomain, "new.example") {
			t.Fatalf("stop guidance still active after approval: %+v", in)
		}
	}
	continued := false
	for _, in := range s.Store.ListIntents() {
		if in.Title == "继续测试 new.example：已加入围栏" {
			continued = true
		}
	}
	if !continued {
		t.Fatalf("continue intent missing: %+v", s.Store.ListIntents())
	}
	if rows := s.Store.AssetsByFence(model.AssetFencePending); len(rows) != 0 {
		t.Fatalf("queue not drained: %+v", rows)
	}
}

func TestFencePendingResolveReject(t *testing.T) {
	s, _ := fenceFlowServer(t)
	if err := s.observeFenceOut(refusedOutOfRange("https://blocked.example/x")); err != nil {
		t.Fatal(err)
	}
	pending := s.Store.AssetsByFence(model.AssetFencePending)
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	rec := call(t, s, http.MethodPost, "/api/fence-pending/resolve", map[string]any{
		"ids": []string{pending[0].ID}, "approve": false,
	})
	if rec.Code != 200 {
		t.Fatalf("resolve = %d %s", rec.Code, rec.Body.String())
	}
	asset, ok := s.Store.GetAsset(pending[0].ID)
	if !ok || asset.Fence != model.AssetFenceDenied {
		t.Fatalf("asset after reject = %+v ok=%v", asset, ok)
	}
	denied := false
	for _, in := range s.Store.InstructionsForAsset("blocked.example") {
		if in.State == model.InstructionActive && in.Text == "blocked.example 不在授权范围，不要测试。" {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("denied guidance missing: %+v", s.Store.InstructionsForAsset("blocked.example"))
	}
	// A denied asset is never re-queued by later refusals.
	if err := s.observeFenceOut(refusedOutOfRange("https://blocked.example/again")); err != nil {
		t.Fatal(err)
	}
	if rows := s.Store.AssetsByFence(model.AssetFencePending); len(rows) != 0 {
		t.Fatalf("denied asset re-queued: %+v", rows)
	}
}

// Leaving the fence: the asset's own allow row is removed, the asset turns
// denied, the operator-scope stop record names the entity, and the guidance
// is rewritten. An asset admitted only by a broader row gets an explicit
// block row so first-hit-wins actually excludes it.
func TestAssetFenceLeave(t *testing.T) {
	s, p := fenceFlowServer(t)
	// Seed: a parent-domain allow row plus an asset with its own row.
	seed := project.ScopeRules{Enabled: true, DefaultAction: project.ScopeActionReview, Rules: []project.ScopeRule{
		{Action: project.ScopeActionRecord, Method: "*", Host: "parent.example", PathPrefix: "/"},
		{Action: project.ScopeActionRecord, Method: "*", Host: "child.parent.example", PathPrefix: "/"},
	}}
	normalized, err := project.NormalizeScopeRules(seed)
	if err != nil {
		t.Fatal(err)
	}
	p.ScopeRuleSet = &normalized
	asset, _, err := s.Store.UpsertAsset(model.Asset{Kind: model.AssetKindDomain, Key: "child.parent.example",
		Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence})
	if err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, http.MethodPost, "/api/assets/"+asset.ID+"/fence", map[string]any{"action": "leave"})
	if rec.Code != 200 {
		t.Fatalf("leave = %d %s", rec.Code, rec.Body.String())
	}
	left, _ := s.Store.GetAsset(asset.ID)
	if left.Fence != model.AssetFenceDenied {
		t.Fatalf("asset after leave = %+v", left)
	}
	rules := p.ScopeRuleSet
	ownRow, blockRow := false, false
	for _, r := range rules.Rules {
		if r.Host == "child.parent.example" && r.Action == project.ScopeActionRecord {
			ownRow = true
		}
		if r.Host == "child.parent.example" && r.Action == project.ScopeActionBlock {
			blockRow = true
		}
	}
	// The own allow row is gone AND the block row is present: the parent row
	// still admits every subdomain, so exclusion must be explicit.
	if ownRow || !blockRow {
		t.Fatalf("leave kept the allow row or lost the block row: %+v", rules.Rules)
	}
	// Rejoining prepends its allow row ahead of the block, so the host is
	// admitted again under first-hit-wins.

	scope, err := s.readOperatorScope()
	if err != nil {
		t.Fatal(err)
	}
	stops := scope.Stops()
	// Both spellings ride the record: the bare asset key and the "host:"
	// convention facts are declared with (late-fact matching is exact).
	if len(stops) != 1 || !containsString(stops[0].EntityRefs, "child.parent.example") ||
		!containsString(stops[0].EntityRefs, "host:child.parent.example") {
		t.Fatalf("stop record = %+v", stops)
	}

	// Joining again restores the allow row and the in_fence state.
	rec = call(t, s, http.MethodPost, "/api/assets/"+asset.ID+"/fence", map[string]any{"action": "join"})
	if rec.Code != 200 {
		t.Fatalf("rejoin = %d %s", rec.Code, rec.Body.String())
	}
	rejoined, _ := s.Store.GetAsset(asset.ID)
	if rejoined.Fence != model.AssetFenceIn {
		t.Fatalf("asset after rejoin = %+v", rejoined)
	}
	allowIdx, blockIdx := -1, -1
	for i, r := range p.ScopeRuleSet.Rules {
		if r.Host == "child.parent.example" && r.Action == project.ScopeActionRecord && allowIdx < 0 {
			allowIdx = i
		}
		if r.Host == "child.parent.example" && r.Action == project.ScopeActionBlock && blockIdx < 0 {
			blockIdx = i
		}
	}
	if allowIdx < 0 || blockIdx < 0 || allowIdx > blockIdx {
		t.Fatalf("rejoin did not prepend its allow row above the block: %+v", p.ScopeRuleSet.Rules)
	}

	// Leaving twice must not stack block rows or stop records.
	if rec := call(t, s, http.MethodPost, "/api/assets/"+asset.ID+"/fence", map[string]any{"action": "leave"}); rec.Code != 200 {
		t.Fatalf("second leave = %d %s", rec.Code, rec.Body.String())
	}
	blocks := 0
	for _, r := range p.ScopeRuleSet.Rules {
		if r.Host == "child.parent.example" && r.Action == project.ScopeActionBlock {
			blocks++
		}
	}
	if blocks != 1 {
		t.Fatalf("repeat leave stacked block rows: %d", blocks)
	}
	if scope, _ := s.readOperatorScope(); len(scope.Stops()) != 2 {
		t.Fatalf("stops after two leaves = %d (the first leave was undone by the rejoin, so one new stop)", len(scope.Stops()))
	}
}

// The stop record names both entity-ref spellings, so a fact declared with
// the "host:" convention is annotated as late-after-stop (the operator-scope
// annotation matches strings exactly).
func TestLeaveFenceAnnotatesLateFacts(t *testing.T) {
	s, p := fenceFlowServer(t)
	normalized, err := project.NormalizeScopeRules(project.ScopeRules{Enabled: true, DefaultAction: project.ScopeActionReview, Rules: []project.ScopeRule{
		{Action: project.ScopeActionRecord, Method: "*", Host: "late.example", PathPrefix: "/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p.ScopeRuleSet = &normalized
	asset, _, err := s.Store.UpsertAsset(model.Asset{Kind: model.AssetKindDomain, Key: "late.example",
		Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence})
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(t, s, http.MethodPost, "/api/assets/"+asset.ID+"/fence", map[string]any{"action": "leave"}); rec.Code != 200 {
		t.Fatalf("leave = %d %s", rec.Code, rec.Body.String())
	}
	// A fact recorded after the stop, declared with the host: convention.
	if _, err := s.Store.AddFact(model.Fact{Kind: "note", Title: "停测后仍观测到界面变化",
		Confidence: "observed", Status: "unverified", EntityRef: "host:late.example"}); err != nil {
		t.Fatal(err)
	}
	scope, err := s.readOperatorScope()
	if err != nil {
		t.Fatal(err)
	}
	view := s.operatorScopeView(scope)
	if len(view.LateFacts) != 1 || view.LateFacts[0].EntityRef != "host:late.example" {
		t.Fatalf("late facts = %+v", view.LateFacts)
	}
}

func TestAssetFenceJoinPendingIsApproval(t *testing.T) {
	s, _ := fenceFlowServer(t)
	if err := s.observeFenceOut(refusedOutOfRange("https://queued.example/x")); err != nil {
		t.Fatal(err)
	}
	pending := s.Store.AssetsByFence(model.AssetFencePending)
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	rec := call(t, s, http.MethodPost, "/api/assets/"+pending[0].ID+"/fence", map[string]any{"action": "join"})
	if rec.Code != 200 {
		t.Fatalf("join pending = %d %s", rec.Code, rec.Body.String())
	}
	joined, _ := s.Store.GetAsset(pending[0].ID)
	if joined.Fence != model.AssetFenceIn {
		t.Fatalf("asset = %+v", joined)
	}
	for _, in := range s.Store.InstructionsForAsset("queued.example") {
		if in.State == model.InstructionActive && in.IdempotencyKey == fenceOutStopKey(model.AssetKindDomain, "queued.example") {
			t.Fatalf("stop guidance still active after membership join: %+v", in)
		}
	}
	continued := false
	for _, in := range s.Store.ListIntents() {
		if in.Title == "继续测试 queued.example：已加入围栏" {
			continued = true
		}
	}
	if !continued {
		t.Fatal("membership join did not dispatch the continue intent")
	}
}

func TestAssetFenceRejectsServiceJoin(t *testing.T) {
	s, _ := fenceFlowServer(t)
	asset, _, err := s.Store.UpsertAsset(model.Asset{Kind: model.AssetKindService, Key: "10.0.0.5:8080",
		Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceObserved})
	if err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, http.MethodPost, "/api/assets/"+asset.ID+"/fence", map[string]any{"action": "join"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("service join = %d %s", rec.Code, rec.Body.String())
	}
}

// The trigger runs from the real flow-save path: a refused flow whose event
// carries the fork's default_missed marker and the review action registers
// the pending asset. This is the wiring observeFenceOut's own test cannot
// see — the journal gate (flag AND action) and the run lookup.
func TestJournalTriggerFromRefusedFlow(t *testing.T) {
	s, _ := fenceFlowServer(t)
	run, err := s.Store.AddRun(model.Run{SessionID: "sess-e2e", Status: model.RunRunning})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(testDir(t), "runs", run.ID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openJournal(run.ID, logPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.closeJournal(run.ID) })
	ev := executor.Event{
		Kind: "http", SessionID: "sess-e2e",
		NativeFlow: &native.Flow{
			Id: "f-e2e", Complete: true,
			Request:  &native.HttpRequest{Method: "GET", Url: "https://observed.example/x"},
			Response: &native.HttpResponse{StatusCode: 403},
		},
		TrafficAction: "review", TrafficDefaultMissed: true,
	}
	if err := s.appendRunEvent(run.ID, ev); err != nil {
		t.Fatal(err)
	}
	rows := s.Store.AssetsByFence(model.AssetFencePending)
	if len(rows) != 1 || rows[0].Key != "observed.example" {
		t.Fatalf("journal trigger did not queue the asset: %+v", rows)
	}
}
