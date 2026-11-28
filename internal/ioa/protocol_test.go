package ioa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/testutil"
	"github.com/chainreactors/cyber/core/collaboration"
)

// The observer's protocol verdicts, pinned at the public boundary. The engine's
// core/collaboration is the judge that actually runs on the executing end, so
// these are the engine rules: nine drift inputs below record the verdict where
// the retired shell copy used to decide the other way (marked driftShellX /
// driftEngineX), every other verdict was identical in both copies.

// verdictSpec is one wire shape fed through the observer.
type verdictSpec struct {
	name string
	meta map[string]any
	// wantRejected: true = the observer records the message as rejected
	// (Error set, no envelope).
	wantRejected bool
}

func envelopeVerdictSpecs() []verdictSpec {
	agent := func(name string) map[string]any { return map[string]any{"name": name} }
	return []verdictSpec{
		// Engine-conventional shapes.
		{"message plain", map[string]any{"version": 1, "event": "message", "id": "cm1",
			"source": map[string]any{"session_id": "s0"}, "target": map[string]any{"session_id": "s1"}, "interrupt": true}, false},
		{"task.result with chain", map[string]any{"version": 1, "event": "task.result", "id": "ct1",
			"target": map[string]any{"session_id": "s1"}, "chain": []string{"t1", "t2"}}, false},
		{"subagent.delegate plain", map[string]any{"version": 1, "event": "subagent.delegate", "id": "cd1",
			"target": map[string]any{"session_id": "s1"}, "call_id": "call-1", "agent": agent("scout")}, false},
		{"subagent.return plain", map[string]any{"version": 1, "event": "subagent.return", "id": "cr1",
			"source": map[string]any{"session_id": "s2"}, "call_id": "call-1", "agent": agent("scout")}, false},
		{"inbox.route plain", map[string]any{"version": 1, "event": "inbox.route", "id": "ci1",
			"target": map[string]any{"task_id": "t9"}}, false},

		// The six documented drift inputs. The first three used to be rejected
		// by the stricter shell copy; the engine (now authoritative) accepts
		// them. The last three used to be accepted; the engine rejects them.
		{"delegate without call_id (drift: shell rejected, engine accepts)", map[string]any{"version": 1, "event": "subagent.delegate", "id": "d1",
			"target": map[string]any{"session_id": "s1"}, "agent": agent("scout")}, false},
		{"return without call_id (drift: shell rejected, engine accepts)", map[string]any{"version": 1, "event": "subagent.return", "id": "r1",
			"source": map[string]any{"session_id": "s2"}, "agent": agent("scout")}, false},
		{"return without call_id or agent (drift: shell rejected, engine accepts)", map[string]any{"version": 1, "event": "subagent.return", "id": "r2",
			"source": map[string]any{"session_id": "s2"}}, false},
		{"return without source.session_id (drift: shell accepted, engine rejects)", map[string]any{"version": 1, "event": "subagent.return", "id": "r3",
			"source": map[string]any{"task_id": "t1"}, "call_id": "call-2", "agent": agent("scout")}, true},
		{"inbox.route with call_id (drift: shell accepted, engine rejects)", map[string]any{"version": 1, "event": "inbox.route", "id": "i1",
			"target": map[string]any{"session_id": "s1"}, "call_id": "call-3"}, true},
		{"message with call_id (drift: shell accepted, engine rejects)", map[string]any{"version": 1, "event": "message", "id": "m5",
			"target": map[string]any{"session_id": "s1"}, "call_id": "call-4"}, true},

		// Three further drift inputs found while re-reading both Validate
		// implementations field by field (same nature as the six above).
		{"delegate without agent (drift: shell rejected, engine accepts)", map[string]any{"version": 1, "event": "subagent.delegate", "id": "d2",
			"target": map[string]any{"session_id": "s1"}, "call_id": "call-5"}, false},
		{"message with status (drift: shell accepted, engine rejects)", map[string]any{"version": 1, "event": "message", "id": "m6",
			"target": map[string]any{"session_id": "s1"}, "status": "sending"}, true},
		{"delegate without target (drift: shell accepted, engine rejects)", map[string]any{"version": 1, "event": "subagent.delegate", "id": "d3",
			"call_id": "call-6", "agent": agent("scout")}, true},
	}
}

func TestObserverVerdictsOnEnvelopeShapes(t *testing.T) {
	specs := envelopeVerdictSpecs()
	messages := make([]map[string]any, 0, len(specs))
	for i, spec := range specs {
		messages = append(messages, map[string]any{
			"id": fmt.Sprintf("wire-%d", i), "sender": "web-id",
			"content": map[string]any{"text": spec.name}, "meta": spec.meta,
		})
	}
	page, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ioa/spaces":
			_, _ = w.Write([]byte(`[{"id":"sp-1","name":"default","nodes":[{"id":"agent-id","name":"local"}]}]`))
		case "/ioa/nodes":
			_, _ = w.Write([]byte(`[{"id":"web-id","name":"cyber.web"},{"id":"agent-id","name":"local"}]`))
		case "/ioa/spaces/sp-1/messages":
			_, _ = w.Write(page)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	obs, err := NewObserver(New(srv.URL, "tok", "local"), filepath.Join(t.TempDir(), "ioa"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := obs.Step(context.Background()); err != nil || n != len(specs) {
		t.Fatalf("step appended %d/%v, want %d", n, err, len(specs))
	}
	records, err := obs.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != len(specs) {
		t.Fatalf("records = %d, want %d", len(records), len(specs))
	}
	byWire := make(map[string]Record, len(records))
	for _, rec := range records {
		byWire[rec.IOAMessageID] = rec
	}
	for i, spec := range specs {
		rec, ok := byWire[fmt.Sprintf("wire-%d", i)]
		if !ok {
			t.Fatalf("%s: observation lost", spec.name)
		}
		if spec.wantRejected && (rec.Error == "" || rec.Envelope != nil) {
			t.Fatalf("%s: want rejected, got %+v", spec.name, rec)
		}
		if !spec.wantRejected && (rec.Error != "" || rec.Envelope == nil) {
			t.Fatalf("%s: want accepted, got error %q envelope %+v", spec.name, rec.Error, rec.Envelope)
		}
	}
}

// The engine envelope serializes empty source/target by omitting them
// (omitzero), instead of the old shell copy's literal {} (omitempty is a no-op
// on struct values). Lines with the old {} form are still on operator disks
// and must keep reading back — see the next test.
func TestRecordJSONLOmitsEmptyAddresses(t *testing.T) {
	raw, err := json.Marshal(Record{Seq: 1, IOAMessageID: "m1",
		Envelope: &collaboration.Envelope{Version: collaboration.Version, Event: collaboration.Message, ID: "e1"}})
	if err != nil {
		t.Fatal(err)
	}
	line := string(raw)
	if strings.Contains(line, `"source"`) || strings.Contains(line, `"target"`) {
		t.Fatalf("empty addresses are still serialized: %s", line)
	}
	if !strings.Contains(line, `"envelope":{"version":1,"event":"message","id":"e1"`) {
		t.Fatalf("envelope payload changed shape: %s", line)
	}
}

// Operator disks hold old JSONL with "source":{} / "target":{}. Those lines
// must keep reading back now that the shell speaks the engine envelope.
func TestJSONLWithEmptyObjectAddressesStillReadsBack(t *testing.T) {
	dir := t.TempDir()
	line := `{"seq":7,"envelope":{"version":1,"event":"task.result","id":"old-1","source":{},"target":{}},` +
		`"ioa_message_id":"wire-old","observed_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := ReadRecords(dir)
	if err != nil || len(records) != 1 {
		t.Fatalf("read = %d/%v, want 1", len(records), err)
	}
	rec := records[0]
	if rec.Envelope == nil || rec.Envelope.ID != "old-1" || rec.Envelope.Event != collaboration.TaskResult {
		t.Fatalf("old line lost its envelope: %+v", rec)
	}
	if rec.Error != "" {
		t.Fatalf("old line came back rejected: %q", rec.Error)
	}
}

// The senders build the envelope from the fields they own and never write a
// meta the engine would reject. An invalid envelope fails locally, before
// anything is sent.
func TestSendMetaRejectsInvalidEnvelopesBeforeSending(t *testing.T) {
	meta, err := sendMeta(collaboration.Envelope{Version: collaboration.Version, Event: collaboration.TaskResult,
		ID: "x", Target: collaboration.Address{SessionID: "s"}, CallID: "not-allowed"})
	if err == nil || meta != nil {
		t.Fatalf("invalid task.result envelope produced meta %v/%v", meta, err)
	}
	if _, err := sendMeta(collaboration.Envelope{Version: collaboration.Version, Event: collaboration.InboxRoute,
		ID: "x", Target: collaboration.Address{SessionID: "s"}, CallID: "not-allowed"}); err == nil {
		t.Fatal("inbox.route with a call id produced valid meta")
	}
	if _, err := sendMeta(collaboration.Envelope{Version: 2, Event: collaboration.Message}); err == nil {
		t.Fatal("unknown version produced valid meta")
	}
}

// The senders build the envelope via Meta(), so the wire key set is the
// engine's: fields that carry no information are simply absent. In particular
// an empty chain no longer lands as "chain":null — the shape neither the
// engine nor this package's own observer accepts (the old hand-built map wrote
// it unconditionally; today's caller guard keeps empty chains from arriving,
// this pins the wire shape regardless).
func TestSendMetaKeySets(t *testing.T) {
	var got sendMessage
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ioa/spaces":
			_, _ = w.Write([]byte(`[{"id":"sp-1","name":"default","nodes":[{"id":"agent-id","name":"local"}]}]`))
		case "/ioa/nodes":
			_, _ = w.Write([]byte(`[{"id":"web-id","name":"cyber.web"},{"id":"agent-id","name":"local"}]`))
		case "/ioa/spaces/sp-1/messages":
			got = sendMessage{} // decode merges into maps; start clean per request
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"msg-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "local")

	if _, err := c.SendEvent(context.Background(), "s1", "hello", false, ""); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "event", "target"} {
		if _, ok := got.Meta[key]; !ok {
			t.Fatalf("SendEvent meta misses %q: %v", key, got.Meta)
		}
	}
	if _, ok := got.Meta["interrupt"]; ok {
		t.Fatalf("interrupt=false is written to the wire: %v", got.Meta)
	}
	if len(got.Meta) != 3 {
		t.Fatalf("SendEvent meta keys = %v, want 3", got.Meta)
	}

	// An explicit event id lands as the stable application identity.
	if _, err := c.SendEvent(context.Background(), "s1", "hello", true, "evt-1"); err != nil {
		t.Fatal(err)
	}
	if got.Meta["id"] != "evt-1" || got.Meta["interrupt"] != true {
		t.Fatalf("SendEvent meta = %v", got.Meta)
	}

	if _, err := c.SendResult(context.Background(), "s1", "done", "evt-result-1", nil); err != nil {
		t.Fatal(err)
	}
	if chain, ok := got.Meta["chain"]; ok {
		t.Fatalf("SendResult with empty chain writes chain %v, want the key absent", chain)
	}
	for _, key := range []string{"version", "event", "id", "target"} {
		if _, ok := got.Meta[key]; !ok {
			t.Fatalf("SendResult meta misses %q: %v", key, got.Meta)
		}
	}
	if len(got.Meta) != 4 {
		t.Fatalf("SendResult meta keys = %v, want 4", got.Meta)
	}
}
