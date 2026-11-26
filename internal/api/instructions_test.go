package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/model"
)

func TestPersistentInstructionAPIKeepsPausedGuidanceOutsideFacts(t *testing.T) {
	s, _ := newBindingServer(t)
	s.paused.Store(true)
	rec := call(t, s, "POST", "/api/instructions", map[string]any{"text": "不访问内网", "idempotency_key": "scope"})
	if rec.Code != 201 {
		t.Fatalf("create=%d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Instruction model.Instruction `json:"instruction"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(s.Store.Snapshot().Facts) != 0 || len(s.Store.PendingFor(collab.CommanderID)) != 1 {
		t.Fatal("guidance lost or became fact")
	}
	if rec := call(t, s, "PUT", "/api/instructions/"+out.Instruction.ID, map[string]any{"version": 1, "text": "新的边界", "state": "active"}); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec := call(t, s, "PUT", "/api/instructions/"+out.Instruction.ID, map[string]any{"version": 1, "text": "stale", "state": "revoked"}); rec.Code != 409 {
		t.Fatal(rec.Code)
	}
	if rec := call(t, s, "GET", "/api/instructions/"+out.Instruction.ID+"/history", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "新的边界") {
		t.Fatal(rec.Body.String())
	}
}

// An instruction can name the asset it governs (spec #1 工单2): the reference
// rides create, lands in the store, and survives the response round trip.
// It is identity for asset-scoped listing, not prompt content.
func TestPersistentInstructionCarriesAssetRef(t *testing.T) {
	s, _ := newBindingServer(t)
	s.paused.Store(true)
	rec := call(t, s, "POST", "/api/instructions", map[string]any{
		"text": "不再测试这个域名", "asset_ref": "target.example", "idempotency_key": "asset-ref",
	})
	if rec.Code != 201 {
		t.Fatalf("create=%d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Instruction model.Instruction `json:"instruction"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Instruction.AssetRef != "target.example" {
		t.Fatalf("instruction asset_ref = %q", out.Instruction.AssetRef)
	}
	stored := s.Store.Instructions()
	if len(stored) != 1 || stored[0].AssetRef != "target.example" {
		t.Fatalf("stored instruction = %+v", stored)
	}
	// The reference is create-time identity: the update surface keeps text and
	// state as its mutable fields and does not rebind the asset.
	if rec := call(t, s, "PUT", "/api/instructions/"+out.Instruction.ID, map[string]any{
		"version": 1, "text": "改写后的约束", "state": "active", "asset_ref": "other.example",
	}); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	final := s.Store.Instructions()[0]
	if final.AssetRef != "target.example" {
		t.Fatalf("update rebound the asset: %q", final.AssetRef)
	}
}

func TestCommanderEveryTurnReceivesCurrentInstructionsIncludingRevocation(t *testing.T) {
	s, ex, st := newCommanderServer(t)
	in, _, err := st.CreateInstruction(model.Instruction{CreatedBy: collab.OperatorID, Text: "persistent scope", IdempotencyKey: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StartCommander(context.Background(), withBoardAnchor("round"), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ex.req.Prompt, "persistent scope") || !strings.HasPrefix(ex.req.Prompt, "/skill:commander ") {
		t.Fatal(ex.req.Prompt)
	}
	_, _ = st.UpdateInstruction(in.ID, 1, in.Text, model.InstructionRevoked)
	if _, _, err := s.StartCommander(context.Background(), withBoardAnchor("round"), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ex.req.Prompt, "persistent scope") || !strings.Contains(ex.req.Prompt, "当前没有有效持久指令") {
		t.Fatal("revoked instruction still injected")
	}
}

func TestInboxDrainsOldestMessagesBeforeNewTraffic(t *testing.T) {
	s, _ := newBindingServer(t)
	ids := []string{}
	for i := 0; i < 25; i++ {
		m, _, err := s.Store.AddMessage(model.Message{FromParticipantID: "a", ToParticipantID: "b", Text: fmt.Sprintf("message-%02d", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	first := s.inboxSection("b", "fixture", "run", "", 0)
	if !strings.Contains(first, ids[0]) || strings.Contains(first, ids[10]) {
		t.Fatal("old messages starved")
	}
	_, _, _ = s.Store.AddMessage(model.Message{FromParticipantID: "a", ToParticipantID: "b", Text: "newest"})
	second := s.inboxSection("b", "fixture2", "run", "", 0)
	if !strings.Contains(second, ids[10]) || strings.Contains(second, "newest") {
		t.Fatal("new traffic displaced queued work")
	}
}

func TestParticipantInstructionReadIsScopedAndWritesAreAbsent(t *testing.T) {
	f := newAgentFixture(t)
	in, _, _ := f.p.Store.CreateInstruction(model.Instruction{CreatedBy: collab.OperatorID, Text: "target only", IntentID: f.target.ID, IdempotencyKey: "local"})
	if rec := f.call(t, f.srcID, http.MethodGet, "/api/instructions", nil); rec.Code != 200 || strings.Contains(rec.Body.String(), in.ID) {
		t.Fatal(rec.Body.String())
	}
	if rec := f.call(t, f.srcID, http.MethodGet, "/api/instructions/"+in.ID, nil); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec := f.call(t, f.tgtID, http.MethodGet, "/api/instructions/"+in.ID, nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := f.call(t, f.tgtID, http.MethodPost, "/api/instructions", map[string]any{"text": "invent scope", "idempotency_key": "x"}); rec.Code != 405 && rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

func TestInstructionChangeIsInjectedWhenWorkerSessionContinues(t *testing.T) {
	s, ex, task := newSteerServer(t)
	first := ex.requests()[0]
	if !strings.Contains(first.Prompt, "当前没有有效持久指令") {
		t.Fatal("first turn has no authoritative baseline")
	}
	_, _, err := s.Store.CreateInstruction(model.Instruction{CreatedBy: collab.OperatorID, IntentID: task.ID, Text: "只使用新增的只读约束", IdempotencyKey: "scope"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.CaseRoot, task.ID, ".zrcin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.CaseRoot, task.ID, ".zrcin", "aop-session.json"), []byte(`{"url":"http://fixture","session":"session-fixture","agent":"local"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executor.SessionPath(filepath.Join(s.CaseRoot, task.ID)), []byte("{\"seq\":1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SteerIntent(task.ID, "继续当前意图", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return len(ex.requests()) >= 2 })
	second := ex.requests()[1]
	if !second.Resume || !strings.Contains(second.Prompt, "只使用新增的只读约束") {
		t.Fatalf("continuation lost guidance: %s", second.Prompt)
	}
	cur, _ := s.Store.GetIntent(task.ID)
	run, _ := s.Store.GetRun(cur.RunID)
	if len(run.InstructionVersions) != 1 || len(run.InstructionAcks) != 0 {
		t.Fatal("input versions lost or falsely acknowledged")
	}
}

func TestInstructionAckCannotNameAnotherParticipantsRun(t *testing.T) {
	f := newAgentFixture(t)
	other := f.makeLive(t)
	rec := f.call(t, f.srcID, http.MethodPost, "/api/instructions/instruction_fixture/ack", map[string]any{"version": 1, "run_id": other.ID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign acknowledgement=%d %s", rec.Code, rec.Body.String())
	}
}
