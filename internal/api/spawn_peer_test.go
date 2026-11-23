package api

// Peer publishes (POST /api/spawn with no parent_intent_id) from an executor run
// are the AML-2.2..2.4 surface: a source fact is required, the node is locked to
// the publisher's own direction, and the lineage budget can refuse the request
// with a machine code. The operator/console path keeps the old behaviour.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/model"
)

func peerPublishBody(key, factID string) map[string]any {
	return spawnBody(key, map[string]any{"context_fact_ids": []string{factID}})
}

// A peer publish without a recorded fact is refused with the documented code and
// message, and nothing is written. interest_entity_refs is a reading focus, not
// a source, so it does not satisfy the rule.
func TestPeerSpawnRequiresRecordedSource(t *testing.T) {
	f := newSpawnFixture(t)
	rec := f.call(t, f.srcID, http.MethodPost, "/api/spawn", spawnBody("peer-no-source", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no source: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "peer_publish_needs_source" {
		t.Fatalf("code = %v, want peer_publish_needs_source", body["code"])
	}
	if got, _ := body["error"].(string); got != peerPublishNeedsSource {
		t.Fatalf("message = %q, want %q", got, peerPublishNeedsSource)
	}
	// A focus hint alone is not a source.
	rec = f.call(t, f.srcID, http.MethodPost, "/api/spawn",
		spawnBody("peer-interest-only", map[string]any{"interest_entity_refs": []string{"host:a"}}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "peer_publish_needs_source") {
		t.Fatalf("interest-only: %d %s", rec.Code, rec.Body.String())
	}
	if n := len(f.p.Store.ListIntents()); n != 2 {
		t.Fatalf("refused publishes wrote rows: %d intents", n)
	}
}

// A sourced peer publish inherits Doc and skills from the publishing direction
// (no ParentID is invented: the result is still a peer, not a delegated child).
func TestPeerSpawnInheritsSourceContext(t *testing.T) {
	f := newSpawnFixture(t)
	if _, err := f.p.Store.UpdateIntent(f.src.ID, func(i *model.Intent) {
		i.Doc, i.Node, i.Skills = "操作者约束", "local", []string{"collab"}
	}); err != nil {
		t.Fatal(err)
	}
	rec := f.call(t, f.srcID, http.MethodPost, "/api/spawn", peerSpawnBody(t, f, "peer-inherit", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("peer publish: %d %s", rec.Code, rec.Body.String())
	}
	var resp spawnResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Intent.ParentID != "" {
		t.Fatalf("peer publish grew a parent: %+v", resp.Intent)
	}
	if resp.Intent.Doc != "操作者约束" || resp.Intent.Node != "local" {
		t.Fatalf("peer did not inherit doc/node: %+v", resp.Intent)
	}
	if len(resp.Intent.Skills) != 1 || resp.Intent.Skills[0] != "collab" {
		t.Fatalf("peer skills = %v", resp.Intent.Skills)
	}
}

// The peer publish stays on the publisher's node: a different node is refused
// with the documented code, the same node and the inherited node pass.
func TestPeerSpawnNodeLocked(t *testing.T) {
	f := newSpawnFixture(t)
	body := peerPublishBody("peer-node-lock", spawnRefFact(t, f))
	body["node"] = "local"
	rec := f.call(t, f.srcID, http.MethodPost, "/api/spawn", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "peer_publish_node_locked") {
		t.Fatalf("different node: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := f.p.Store.UpdateIntent(f.src.ID, func(i *model.Intent) { i.Node = "local" }); err != nil {
		t.Fatal(err)
	}
	rec = f.call(t, f.srcID, http.MethodPost, "/api/spawn",
		spawnBody("peer-node-same", map[string]any{"context_fact_ids": []string{spawnRefFact(t, f)}, "node": "local"}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("same node: %d %s", rec.Code, rec.Body.String())
	}
	var resp spawnResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Intent.Node != "local" {
		t.Fatalf("same-node publish node = %q", resp.Intent.Node)
	}
	// No node named: inherit the publisher's node.
	rec = f.call(t, f.srcID, http.MethodPost, "/api/spawn", peerSpawnBody(t, f, "peer-node-inherit", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("inherited node: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Intent.Node != "local" {
		t.Fatalf("inherited-node publish node = %q", resp.Intent.Node)
	}
}

// runScopedFixture makes the source direction a live turn with a real run row,
// so the budget refusal can be recorded on that run.
func runScopedFixture(t *testing.T) (*agentFixture, collab.Identity, model.Run) {
	t.Helper()
	f := newSpawnFixture(t)
	run, err := f.p.Store.AddRun(model.Run{IntentID: f.src.ID, Node: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Store.UpdateIntent(f.src.ID, func(i *model.Intent) {
		i.Status, i.RunID = model.StatusRunning, run.ID
	}); err != nil {
		t.Fatal(err)
	}
	id, err := f.srv.Collab.Issue(collab.Identity{
		ProjectID: f.p.ID, ParticipantID: f.srcID.ParticipantID,
		IntentID: f.src.ID, RunID: run.ID, Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, id, run
}

// The per-run lineage gate maps to 409 lineage_budget, appends one deduped info
// to the caller's run (not just an HTTP error), and a retry of an already
// recorded publish still returns that row after the budget is gone.
func TestPeerSpawnLineageBudget(t *testing.T) {
	f, id, run := runScopedFixture(t)
	factID := spawnRefFact(t, f)
	var keys []string
	var last spawnResp
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("peer-budget-%d", i)
		keys = append(keys, key)
		rec := f.call(t, id, http.MethodPost, "/api/spawn", peerPublishBody(key, factID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("publish %d: %d %s", i, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &last); err != nil {
			t.Fatal(err)
		}
	}
	over := peerPublishBody("peer-budget-over", factID)
	rec := f.call(t, id, http.MethodPost, "/api/spawn", over)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"lineage_budget"`) {
		t.Fatalf("over budget: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "闸门=本轮发布") || !strings.Contains(rec.Body.String(), "idempotency_key") {
		t.Fatalf("budget message lacks the gate/guidance: %s", rec.Body.String())
	}
	// The refusal is on the run, deduped by text and capped.
	gotRun, ok := f.p.Store.GetRun(run.ID)
	if !ok || len(gotRun.Infos) != 1 {
		t.Fatalf("run infos = %+v ok=%v", gotRun.Infos, ok)
	}
	if !strings.Contains(gotRun.Infos[0], "谱系预算") || !strings.Contains(gotRun.Infos[0], "当前用量=8") {
		t.Fatalf("run info = %q", gotRun.Infos[0])
	}
	rec = f.call(t, id, http.MethodPost, "/api/spawn", over)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second refusal: %d %s", rec.Code, rec.Body.String())
	}
	if again, _ := f.p.Store.GetRun(run.ID); len(again.Infos) != 1 {
		t.Fatalf("budget info not deduped: %+v", again.Infos)
	}

	// The retry of an already accepted publish is answered from the ledger, not
	// from the gate, so the caller can still recover its worker.
	rec = f.call(t, id, http.MethodPost, "/api/spawn", peerPublishBody(keys[len(keys)-1], factID))
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent retry after budget: %d %s", rec.Code, rec.Body.String())
	}
	var again spawnResp
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.Created || again.Intent.ID != last.Intent.ID {
		t.Fatalf("retry = %+v, want existing %s", again, last.Intent.ID)
	}

	// Child delegation shares the same per-run gate and the same 409 code.
	rec = f.call(t, id, http.MethodPost, "/api/intents/"+f.src.ID+"/children",
		map[string]any{"title": "超预算子意图", "idempotency_key": "child-over"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"lineage_budget"`) {
		t.Fatalf("child over budget: %d %s", rec.Code, rec.Body.String())
	}
}

// The console (operator/commander) may still spawn a parentless worker without a
// source fact: bootstrap by a human is the documented exception.
func TestConsolePeerSpawnWithoutSourceUnchanged(t *testing.T) {
	f := newSpawnFixture(t)
	rec := consoleCall(t, f.srv, http.MethodPost, "/api/spawn", "", spawnBody("console-no-source", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("console spawn: %d %s", rec.Code, rec.Body.String())
	}
}
