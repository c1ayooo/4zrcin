package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/traffic"
	native "github.com/chainreactors/cyber/aop/traffic"
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

// The first refusal of an out-of-range host records the pending asset and
// NOTHING ELSE MOVES: no wake, no marker intent, no standing instruction, no
// message — the fence already blocked the traffic and the operator finds the
// queue on the pending card. Every later refusal of the same host is quiet
// (one decision per identity), and an in-fence/denied asset is never re-queued.
func TestFenceOutTriggerRecordsOnce(t *testing.T) {
	s, _ := fenceFlowServer(t)
	if err := s.observeFenceOut(refusedOutOfRange("https://new.example/api/users")); err != nil {
		t.Fatal(err)
	}
	rows := s.Store.AssetsByFence(model.AssetFencePending)
	if len(rows) != 1 || rows[0].Kind != model.AssetKindDomain || rows[0].Key != "new.example" || !rows[0].Target {
		t.Fatalf("pending rows = %+v", rows)
	}
	if got := fenceMessages(s); len(got) != 0 {
		t.Fatalf("the trigger spent messages the queue did not need: %+v", got)
	}
	if got := s.Store.Instructions(); len(got) != 0 {
		t.Fatalf("the trigger wrote standing instructions: %+v", got)
	}
	for _, in := range s.Store.ListIntents() {
		if in.Title == "不再测试这个资产：new.example" {
			t.Fatalf("the trigger parked a marker intent: %+v", in)
		}
	}

	// Idempotent: same host again, and a subdomain is its own identity.
	if err := s.observeFenceOut(refusedOutOfRange("https://new.example/other")); err != nil {
		t.Fatal(err)
	}
	if rows := s.Store.AssetsByFence(model.AssetFencePending); len(rows) != 1 {
		t.Fatalf("repeat trigger duplicated the asset: %+v", rows)
	}
	if got := len(s.Store.InstructionsForAsset("new.example")); got != 0 {
		t.Fatalf("repeat trigger wrote guidance: %d", got)
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
	// The row is the record: no message, no standing instruction, no intents —
	// the board's fence block carries the standing without a model round.
	if got := fenceMessages(s); len(got) != 0 {
		t.Fatalf("reject spent messages the row did not need: %+v", got)
	}
	if got := s.Store.Instructions(); len(got) != 0 {
		t.Fatalf("reject wrote standing instructions: %+v", got)
	}
	// A denied asset is never re-queued by later refusals.
	if err := s.observeFenceOut(refusedOutOfRange("https://blocked.example/again")); err != nil {
		t.Fatal(err)
	}
	if rows := s.Store.AssetsByFence(model.AssetFencePending); len(rows) != 0 {
		t.Fatalf("denied asset re-queued: %+v", rows)
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

// fenceMessages collects the fence lifecycle messages from the ledger.
func fenceMessages(s *Server) []model.Message {
	var out []model.Message
	for _, m := range s.Store.Messages(500) {
		if m.Kind == fenceMessageKind {
			out = append(out, m)
		}
	}
	return out
}

// The approval is the one model round this flow spends: the continue intent
// carries the work and one message tells the commander now (live round or
// next round), with the intent id in the text.
func TestFencePendingApproveAnnouncesCommander(t *testing.T) {
	s, _ := fenceFlowServer(t)
	if err := s.observeFenceOut(refusedOutOfRange("https://approve.example/x")); err != nil {
		t.Fatal(err)
	}
	pending := s.Store.AssetsByFence(model.AssetFencePending)
	rec := call(t, s, http.MethodPost, "/api/fence-pending/resolve", map[string]any{
		"ids": []string{pending[0].ID}, "approve": true,
	})
	if rec.Code != 200 {
		t.Fatalf("resolve = %d %s", rec.Code, rec.Body.String())
	}
	continueID := ""
	for _, in := range s.Store.ListIntents() {
		if in.Title == "继续测试 approve.example：已加入围栏" {
			continueID = in.ID
		}
	}
	if continueID == "" {
		t.Fatal("continue intent missing")
	}
	msgs := fenceMessages(s)
	if len(msgs) != 1 {
		t.Fatalf("approve published %d fence messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.FromParticipantID != collab.OperatorID || m.ToParticipantID != collab.CommanderID ||
		!strings.Contains(m.Text, "approve.example") || !strings.Contains(m.Text, continueID) {
		t.Fatalf("approve message = %+v (continue intent %s)", m, continueID)
	}
}

// Rejoining a DENIED asset from the membership surface is a fresh approval:
// it announces like any other join. The row is the record — nothing else is
// written on the fence's authority.
func TestFenceJoinDeniedAnnounces(t *testing.T) {
	s, _ := fenceFlowServer(t)
	asset, _, err := s.Store.UpsertAsset(model.Asset{Kind: model.AssetKindDomain, Key: "back.example",
		Target: true, Fence: model.AssetFenceDenied, Source: model.AssetSourceObserved})
	if err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, http.MethodPost, "/api/assets/"+asset.ID+"/fence", map[string]any{"action": "join"})
	if rec.Code != 200 {
		t.Fatalf("join = %d %s", rec.Code, rec.Body.String())
	}
	joined, _ := s.Store.GetAsset(asset.ID)
	if joined.Fence != model.AssetFenceIn {
		t.Fatalf("asset after rejoin = %+v", joined)
	}
	msgs := fenceMessages(s)
	if len(msgs) != 1 || msgs[0].FromParticipantID != collab.OperatorID || !strings.Contains(msgs[0].Text, "back.example") {
		t.Fatalf("rejoin did not announce to the commander: %+v", msgs)
	}
}

// The import's "checked hosts join the fence" step is the same approval: a
// pending asset admitted by an import gets the full tail — continue intent,
// commander announcement — not a silent fence flip.
func TestImportJoinAdmitsPendingAssetThroughApprovalPath(t *testing.T) {
	s, p := fenceFlowServer(t)
	seed, err := project.NormalizeScopeRules(project.ScopeRules{Enabled: true, DefaultAction: project.ScopeActionReview})
	if err != nil {
		t.Fatal(err)
	}
	p.ScopeRuleSet = &seed
	if err := s.observeFenceOut(refusedOutOfRange("https://bulk.example/x")); err != nil {
		t.Fatal(err)
	}
	added, err := s.addHostsToFence([]string{"bulk.example"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added = %d", added)
	}
	asset, ok := s.Store.AssetByKindKey(model.AssetKindDomain, "bulk.example")
	if !ok || asset.Fence != model.AssetFenceIn {
		t.Fatalf("asset after import join = %+v ok=%v", asset, ok)
	}
	continued := false
	for _, in := range s.Store.ListIntents() {
		if in.Title == "继续测试 bulk.example：已加入围栏" {
			continued = true
		}
	}
	if !continued {
		t.Fatal("import join did not create the continue intent")
	}
	msgs := fenceMessages(s)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "bulk.example") {
		t.Fatalf("import join did not announce to the commander: %+v", msgs)
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

// A passive static subresource's range miss (fork patch 34) is a record, not
// a pending trigger: the capture metadata carries guardrail_passive, and the
// fence-out flow stays quiet. This is the regression the harness's dropped
// flag used to make unreachable on real traffic.
func TestJournalPassiveCaptureIsNotPendingTrigger(t *testing.T) {
	s, _ := fenceFlowServer(t)
	run, err := s.Store.AddRun(model.Run{SessionID: "sess-passive", Status: model.RunRunning})
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
		Kind: "http", SessionID: "sess-passive",
		NativeFlow: &native.Flow{
			Id: "f-passive", Complete: true,
			Request: &native.HttpRequest{Method: "GET", Url: "https://cdn.example/app.js",
				Headers: []*native.Header{{Name: "Sec-Fetch-Dest", Value: "script"}}},
			Response: &native.HttpResponse{StatusCode: 403},
		},
		TrafficAction: "record", TrafficPassive: true,
	}
	if err := s.appendRunEvent(run.ID, ev); err != nil {
		t.Fatal(err)
	}
	if rows := s.Store.AssetsByFence(model.AssetFencePending); len(rows) != 0 {
		t.Fatalf("a passive miss queued a pending asset: %+v", rows)
	}
	flows, _, err := traffic.ListCaptures(s.CaseRoot, "", "", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || !flows[0].GuardrailPassive || flows[0].GuardrailAction != "record" {
		t.Fatalf("capture metadata = %+v", flows)
	}
}

// The pending queue carries provenance: refused attempts aggregated from the
// traffic index (count, window, URL shapes, the newest related intent), so
// the operator can judge "放不放" without opening the traffic page.
func TestFencePendingCarriesProvenance(t *testing.T) {
	s, _ := fenceFlowServer(t)
	run, err := s.Store.AddRun(model.Run{SessionID: "sess-prov", Status: model.RunRunning})
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
		Kind: "http", SessionID: "sess-prov",
		NativeFlow: &native.Flow{
			Id: "f-prov", Complete: true,
			Request:  &native.HttpRequest{Method: "GET", Url: "https://prov.example/admin"},
			Response: &native.HttpResponse{StatusCode: 403},
		},
		TrafficAction: "review", TrafficDefaultMissed: true,
	}
	if err := s.appendRunEvent(run.ID, ev); err != nil {
		t.Fatal(err)
	}
	rows := s.Store.AssetsByFence(model.AssetFencePending)
	if len(rows) != 1 || rows[0].Key != "prov.example" {
		t.Fatalf("pending rows = %+v", rows)
	}
	rec := call(t, s, http.MethodGet, "/api/fence-pending", nil)
	if rec.Code != 200 {
		t.Fatalf("pending = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Pending []struct {
			Key        string `json:"key"`
			Provenance *struct {
				RefusedCount  int      `json:"refused_count"`
				RefusedURLs   []string `json:"refused_urls"`
				RunIDs        []string `json:"run_ids"`
				LastRefusedAt string   `json:"last_refused_at"`
			} `json:"provenance"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Pending) != 1 || out.Pending[0].Key != "prov.example" || out.Pending[0].Provenance == nil {
		t.Fatalf("pending payload = %+v", out.Pending)
	}
	pv := out.Pending[0].Provenance
	if pv.RefusedCount != 1 || len(pv.RefusedURLs) != 1 || pv.RefusedURLs[0] != "https://prov.example/admin" {
		t.Fatalf("provenance = %+v", pv)
	}
	if len(pv.RunIDs) != 1 || pv.RunIDs[0] != run.ID {
		t.Fatalf("run ids = %+v", pv.RunIDs)
	}
}

// The board's fence block is how "不在范围" becomes round-visible without a
// wake: the mode and host rows come from the manifest, pending/denied from the
// asset table. The rule TABLE never rides — only the derived standing.
func TestBoardFenceBlock(t *testing.T) {
	s, p := fenceFlowServer(t)
	seed, err := project.NormalizeScopeRules(project.ScopeRules{Enabled: true, DefaultAction: project.ScopeActionReview, Rules: []project.ScopeRule{
		{Action: project.ScopeActionRecord, Method: "*", Host: "in.example", PathPrefix: "/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p.ScopeRuleSet = &seed
	if err := s.observeFenceOut(refusedOutOfRange("https://pending.example/x")); err != nil {
		t.Fatal(err)
	}
	if err := s.observeFenceOut(refusedOutOfRange("https://second.example/x")); err != nil {
		t.Fatal(err)
	}
	pending := s.Store.AssetsByFence(model.AssetFencePending)
	if _, err := s.resolvePendingAsset(context.Background(), pending[0], false); err != nil {
		t.Fatal(err)
	}

	fence := s.boardFenceBlock(s.Store.Snapshot())
	if fence == nil || fence.Mode != project.FenceEnforced || !fence.Enabled || fence.DefaultAction != project.ScopeActionReview {
		t.Fatalf("fence block = %+v", fence)
	}
	if len(fence.InRange) != 1 || fence.InRange[0] != "in.example" {
		t.Fatalf("in range = %+v", fence.InRange)
	}
	if fence.PendingCount != 1 || fence.DeniedCount != 1 ||
		len(fence.Pending) != 1 || fence.Pending[0] != "second.example" ||
		len(fence.Denied) != 1 || fence.Denied[0] != "pending.example" {
		t.Fatalf("standing = %+v", fence)
	}

	// The board endpoint carries the block for a harness session too: the
	// executor and commander read the standing from the board they already
	// receive each round.
	rec := call(t, s, http.MethodGet, "/api/board", nil)
	if rec.Code != 200 {
		t.Fatalf("board = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Fence *struct {
			Mode         string   `json:"mode"`
			InRange      []string `json:"in_range"`
			Pending      []string `json:"pending"`
			Denied       []string `json:"denied"`
			PendingCount int      `json:"pending_count"`
		} `json:"fence"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Fence == nil || out.Fence.Mode != project.FenceEnforced ||
		len(out.Fence.Pending) != 1 || out.Fence.Pending[0] != "second.example" {
		t.Fatalf("board fence = %+v", out.Fence)
	}
}
