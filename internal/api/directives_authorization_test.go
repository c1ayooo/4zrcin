package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/store"
)

// The checked box is what makes the record a permission. Without it the same
// sentence is still a durable instruction, but nothing in the ledger says a
// boundary moved — and the receipt must not claim one did.
func TestAsAuthorizationWritesTheChainAndTheReceipt(t *testing.T) {
	s, p := newBindingServer(t)
	rec := call(t, s, http.MethodPost, "/api/directives", map[string]any{
		"text": "这台机器的订阅可以随便测", "target": "project", "persistent": true,
		"as_authorization": true, "idempotency_key": "auth-project",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("authorization directive = %d %s", rec.Code, rec.Body.String())
	}
	var out directiveResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Instruction == nil || !out.Instruction.Authorization {
		t.Fatalf("stored instruction is not an authorization: %+v", out.Instruction)
	}
	if out.Authorization == nil {
		t.Fatalf("receipt has no authorization block: %s", rec.Body.String())
	}
	if out.Authorization.ID != out.Instruction.ID || out.Authorization.Version != out.Instruction.Version {
		t.Fatalf("receipt %+v does not point at instruction %+v", out.Authorization, out.Instruction)
	}
	if out.Authorization.Scope != "project" || !out.Authorization.VisibleToCommander {
		t.Fatalf("project receipt = %+v", out.Authorization)
	}
	db := p.Store.Snapshot()
	if len(db.Instructions) != 1 || !db.Instructions[0].Authorization {
		t.Fatalf("ledger instruction = %+v", db.Instructions)
	}
}

// A direction-scoped authorization is a permission for that subtree and its
// descendants — but the commander reads it too. The receipt says so from the
// store's own projection, so the console cannot drift from the prompt.
func TestIntentAuthorizationIsVisibleToTheCommander(t *testing.T) {
	s, p := newBindingServer(t)
	target, _ := p.Store.CreateIntent(model.Intent{Title: "订阅"})
	other, _ := p.Store.CreateIntent(model.Intent{Title: "无关方向"})
	rec := call(t, s, http.MethodPost, "/api/directives", map[string]any{
		"text": "只在 36.103.240.7 这个 broker 上放宽", "target": "intent", "intent_id": target.ID,
		"persistent": true, "as_authorization": true, "idempotency_key": "auth-intent",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("intent authorization = %d %s", rec.Code, rec.Body.String())
	}
	var out directiveResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Authorization == nil || out.Authorization.Scope != "intent:"+target.ID {
		t.Fatalf("intent receipt = %+v", out.Authorization)
	}
	if !out.Authorization.VisibleToCommander {
		t.Fatal("receipt claims the commander does not see a direction authorization")
	}
	db := p.Store.Snapshot()
	// The claim must match the projection the prompts actually use.
	seenByCommander := store.ApplicableInstructions(db, "", true)
	if len(seenByCommander) != 1 || seenByCommander[0].ID != out.Instruction.ID {
		t.Fatalf("commander sees %+v", seenByCommander)
	}
	if got := store.ApplicableInstructions(db, other.ID, false); len(got) != 0 {
		t.Fatalf("an unrelated direction sees the authorization: %+v", got)
	}
	if got := store.ApplicableInstructions(db, target.ID, false); len(got) != 1 {
		t.Fatalf("the scoped direction does not see its own authorization: %+v", got)
	}
}

// An unchecked persistent directive records no permission, and the two shapes
// are different requests even under one idempotency key: reusing the key must
// contradict rather than silently return the other shape.
func TestPlainDirectiveIsNotAnAuthorization(t *testing.T) {
	s, p := newBindingServer(t)
	plain := call(t, s, http.MethodPost, "/api/directives", map[string]any{
		"text": "多关注一下运维口", "target": "project", "persistent": true, "idempotency_key": "same-text",
	})
	if plain.Code != http.StatusCreated {
		t.Fatalf("plain = %d %s", plain.Code, plain.Body.String())
	}
	var out directiveResult
	_ = json.Unmarshal(plain.Body.Bytes(), &out)
	if out.Instruction == nil || out.Instruction.Authorization || out.Authorization != nil {
		t.Fatalf("plain directive claimed an authorization: %s", plain.Body.String())
	}
	if strings.Contains(plain.Body.String(), `"authorization"`) {
		t.Fatalf("plain receipt carries an authorization field: %s", plain.Body.String())
	}
	// Same key, same text, different mark: a conflict, never the old row.
	conflict := call(t, s, http.MethodPost, "/api/directives", map[string]any{
		"text": "多关注一下运维口", "target": "project", "persistent": true,
		"as_authorization": true, "idempotency_key": "same-text",
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("key reuse across shapes = %d %s", conflict.Code, conflict.Body.String())
	}
	// A different key makes it a second, separate record.
	marked := call(t, s, http.MethodPost, "/api/directives", map[string]any{
		"text": "多关注一下运维口", "target": "project", "persistent": true,
		"as_authorization": true, "idempotency_key": "same-text-auth",
	})
	if marked.Code != http.StatusCreated || !strings.Contains(marked.Body.String(), `"authorization":{`) {
		t.Fatalf("marked = %d %s", marked.Code, marked.Body.String())
	}
	db := p.Store.Snapshot()
	if len(db.Instructions) != 2 || db.Instructions[0].Authorization || !db.Instructions[1].Authorization {
		t.Fatalf("ledger = %+v", db.Instructions)
	}
}

// A one-shot hint has no revocation point, so it cannot be a permission; the
// request is refused instead of being downgraded silently.
func TestOneShotCannotBeAnAuthorization(t *testing.T) {
	s, p := newBindingServer(t)
	in, _ := p.Store.CreateIntent(model.Intent{Title: "A"})
	rec := call(t, s, http.MethodPost, "/api/directives", map[string]any{
		"text": "顺手测一下", "target": "intent", "intent_id": in.ID,
		"as_authorization": true, "idempotency_key": "hint-auth",
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "directive_invalid") {
		t.Fatalf("one-shot authorization = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "已记入授权链") {
		t.Fatalf("refusal claimed to have recorded a chain: %s", rec.Body.String())
	}
	db := p.Store.Snapshot()
	if len(db.Instructions) != 0 || len(db.Messages) != 0 {
		t.Fatalf("refused request wrote rows: instructions=%d messages=%d", len(db.Instructions), len(db.Messages))
	}
	if got := len(p.Store.PendingFor(collab.ParticipantID(in.ID))); got != 0 {
		t.Fatalf("refused request delivered a hint: %d", got)
	}
}

// A replayed key that resolves to the row it originally wrote reports the same
// chain entry, not a second one.
func TestAuthorizationReplayReportsTheSameChainEntry(t *testing.T) {
	s, _ := newBindingServer(t)
	body := map[string]any{
		"text": "整个项目放宽到可测订阅", "target": "project", "persistent": true,
		"as_authorization": true, "idempotency_key": "auth-replay",
	}
	first := call(t, s, http.MethodPost, "/api/directives", body)
	var a directiveResult
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	again := call(t, s, http.MethodPost, "/api/directives", body)
	var b directiveResult
	_ = json.Unmarshal(again.Body.Bytes(), &b)
	if again.Code != http.StatusOK || b.Created {
		t.Fatalf("replay = %d %s", again.Code, again.Body.String())
	}
	if a.Authorization == nil || b.Authorization == nil || a.Authorization.ID != b.Authorization.ID {
		t.Fatalf("replay chain entries differ: %+v vs %+v", a.Authorization, b.Authorization)
	}
}
