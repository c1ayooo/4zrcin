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
)

// Characterization tests pinning the shell's own v1 protocol behaviour BEFORE
// the envelope is swapped for the engine's core/collaboration. The two copies
// have drifted: three shapes below are rejected by the shell but accepted by
// the engine (marked shellStricter), and three are accepted by the shell but
// rejected by the engine (marked engineStricter). The shellStricter/engineStricter
// verdicts flip when internal/ioa adopts collaboration.Parse; every other
// verdict must survive the swap unchanged.

// verdictSpec is one wire shape fed through the observer. expectedError is the
// verdict of the CURRENT implementation, recorded before the swap.
type verdictSpec struct {
	name string
	meta map[string]any
	// wantRejected pins the current verdict: true = the observer records the
	// message as rejected (Error set, no envelope).
	wantRejected bool
}

func envelopeVerdictSpecs() []verdictSpec {
	agent := func(name string) map[string]any { return map[string]any{"name": name} }
	return []verdictSpec{
		// Engine-conventional shapes: the verdicts here must survive the swap.
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

		// The six documented drift inputs.
		{"delegate without call_id (shellStricter)", map[string]any{"version": 1, "event": "subagent.delegate", "id": "d1",
			"target": map[string]any{"session_id": "s1"}, "agent": agent("scout")}, true},
		{"return without call_id (shellStricter)", map[string]any{"version": 1, "event": "subagent.return", "id": "r1",
			"source": map[string]any{"session_id": "s2"}, "agent": agent("scout")}, true},
		{"return without call_id or agent (shellStricter)", map[string]any{"version": 1, "event": "subagent.return", "id": "r2",
			"source": map[string]any{"session_id": "s2"}}, true},
		{"return without source.session_id (engineStricter)", map[string]any{"version": 1, "event": "subagent.return", "id": "r3",
			"source": map[string]any{"task_id": "t1"}, "call_id": "call-2", "agent": agent("scout")}, false},
		{"inbox.route with call_id (engineStricter)", map[string]any{"version": 1, "event": "inbox.route", "id": "i1",
			"target": map[string]any{"session_id": "s1"}, "call_id": "call-3"}, false},
		{"message with call_id (engineStricter)", map[string]any{"version": 1, "event": "message", "id": "m5",
			"target": map[string]any{"session_id": "s1"}, "call_id": "call-4"}, false},

		// Three further drift inputs found while re-reading both Validate
		// implementations field by field (same nature as the six above).
		{"delegate without agent (shellStricter)", map[string]any{"version": 1, "event": "subagent.delegate", "id": "d2",
			"target": map[string]any{"session_id": "s1"}, "call_id": "call-5"}, true},
		{"message with status (engineStricter)", map[string]any{"version": 1, "event": "message", "id": "m6",
			"target": map[string]any{"session_id": "s1"}, "status": "sending"}, false},
		{"delegate without target (engineStricter)", map[string]any{"version": 1, "event": "subagent.delegate", "id": "d3",
			"call_id": "call-6", "agent": agent("scout")}, false},
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

// Current JSONL writes empty source/target as literal empty objects (struct
// tags use omitempty, which is a no-op on struct values). The engine envelope
// omits them; this assertion flips with the swap (intended behaviour change).
func TestRecordJSONLSerializesEmptyAddressesAsObjects(t *testing.T) {
	raw, err := json.Marshal(Record{Seq: 1, IOAMessageID: "m1",
		Envelope: &Envelope{Version: ProtocolVersion, Event: EventMessage, ID: "e1"}})
	if err != nil {
		t.Fatal(err)
	}
	line := string(raw)
	if !strings.Contains(line, `"source":{}`) || !strings.Contains(line, `"target":{}`) {
		t.Fatalf("empty addresses are not serialized as {}: %s", line)
	}
}

// Operator disks hold old JSONL with "source":{} / "target":{}. Those lines
// must keep reading back after the swap to the engine envelope.
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
	if rec.Envelope == nil || rec.Envelope.ID != "old-1" || rec.Envelope.Event != EventTaskResult {
		t.Fatalf("old line lost its envelope: %+v", rec)
	}
	if rec.Error != "" {
		t.Fatalf("old line came back rejected: %q", rec.Error)
	}
}

// Current SendEvent/SendResult hand-build the meta map. Pin the exact key sets
// and the empty-chain quirk: "chain" is written unconditionally, so an empty
// chain lands on the wire as "chain":null — a shape both the engine and the
// shell's own parser reject. The chain quirk flips with the swap.
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
	keysOf := func(m map[string]any) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out
	}

	if _, err := c.SendEvent(context.Background(), "s1", "hello", false, ""); err != nil {
		t.Fatal(err)
	}
	eventKeys := keysOf(got.Meta)
	if len(eventKeys) != 4 {
		t.Fatalf("SendEvent meta keys = %v, want 4", eventKeys)
	}
	for _, key := range []string{"version", "event", "target", "interrupt"} {
		if _, ok := got.Meta[key]; !ok {
			t.Fatalf("SendEvent meta misses %q: %v", key, eventKeys)
		}
	}

	if _, err := c.SendResult(context.Background(), "s1", "done", "evt-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Meta["chain"]; !ok {
		t.Fatalf("SendResult with empty chain drops the chain key: %v", got.Meta)
	}
	if chain := got.Meta["chain"]; chain != nil {
		t.Fatalf("SendResult with empty chain writes %v, want null", chain)
	}
	eventKeys = keysOf(got.Meta)
	if len(eventKeys) != 5 {
		t.Fatalf("SendResult meta keys = %v, want 5", eventKeys)
	}
}
