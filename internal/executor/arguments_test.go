package executor

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// The harness sends tool arguments as protocol bytes, which protojson writes as
// base64. The journal stores the original text instead: readable as text, and
// reachable by the redactor that replaces known secrets in the encoded line.
func TestPlainToolArgumentsRestoresTheOriginalText(t *testing.T) {
	command := `{"command":"curl -H 'Authorization: Bearer sk-live-0123456789abcdef' https://10.0.0.5/"}`
	raw := json.RawMessage(`{"tool_call":{"id":"c1","name":"bash","arguments":{"data":"` + b64(command) + `","media_type":"application/json"}}}`)

	out, problem := PlainToolArguments(raw)
	if problem != "" {
		t.Fatalf("a decodable payload reported %q", problem)
	}
	if !strings.Contains(string(out), "sk-live-0123456789abcdef") {
		t.Fatalf("arguments stayed encoded: %s", out)
	}
	var parsed struct {
		ToolCall struct {
			Arguments string `json:"arguments"`
		} `json:"tool_call"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("rewritten line is not valid JSON: %v\n%s", err, out)
	}
	if parsed.ToolCall.Arguments != command {
		t.Fatalf("arguments = %q, want the original text", parsed.ToolCall.Arguments)
	}
	// Everything else in the event survives the rewrite.
	if !strings.Contains(string(out), `"name":"bash"`) || !strings.Contains(string(out), `"id":"c1"`) {
		t.Fatalf("rewrite dropped sibling fields: %s", out)
	}
}

func TestPlainToolArgumentsKeepsNonJSONText(t *testing.T) {
	raw := json.RawMessage(`{"tool_call":{"name":"bash","arguments":{"data":"` + b64("plain \x00 bytes") + `","media_type":"application/octet-stream"}}}`)
	out, _ := PlainToolArguments(raw)
	// A string, escaped by the JSON encoder, not a nested object.
	if !strings.Contains(string(out), `"arguments":"plain \u0000 bytes"`) {
		t.Fatalf("non-JSON arguments were not kept as text: %s", out)
	}
}

// A call with no arguments, an event that is not a tool_call, and arguments
// that are already text need no rewrite: they are legal shapes, not anomalies.
func TestPlainToolArgumentsLeavesLegalShapesAlone(t *testing.T) {
	cases := []string{
		`{"tool_call":{"name":"bash","arguments":"{\"command\":\"id\"}"}}`, // already text
		`{"tool_call":{"name":"bash"}}`,                                    // no arguments
		`{"kind":"tool_result","tool":"bash"}`,                             // not a call
	}
	for _, c := range cases {
		if got, problem := PlainToolArguments(json.RawMessage(c)); string(got) != c || problem != "" {
			t.Errorf("rewrote %q into %q (warning %q), want it untouched", c, got, problem)
		}
	}
	if got, problem := PlainToolArguments(nil); len(got) != 0 || problem != "" {
		t.Errorf("nil raw = %q (%q), want empty", got, problem)
	}
}

// An arguments payload the protocol cannot decode is an anomaly, not something
// to pass through: the journal keeps one shape (text) and the reason is named,
// so the broken call is visible instead of silently unreadable.
func TestPlainToolArgumentsNamesAnUndecodablePayload(t *testing.T) {
	for _, c := range []string{
		`{"tool_call":{"name":"bash","arguments":{"data":"not base64!!"}}}`,  // invalid base64
		`{"tool_call":{"name":"bash","arguments":{"command":"curl x"}}}`,     // not an EncodedValue
		`{"tool_call":{"name":"bash","arguments":[1,2]}}`,                    // neither text nor object
	} {
		out, problem := PlainToolArguments(json.RawMessage(c))
		if problem == "" {
			t.Fatalf("%s passed without a warning", c)
		}
		var parsed struct {
			ToolCall struct {
				Arguments json.RawMessage `json:"arguments"`
			} `json:"tool_call"`
		}
		if err := json.Unmarshal(out, &parsed); err != nil {
			t.Fatalf("%s: rewritten line is not valid JSON: %v\n%s", c, err, out)
		}
		if len(parsed.ToolCall.Arguments) == 0 || parsed.ToolCall.Arguments[0] != '"' {
			t.Fatalf("%s: arguments kept a non-text shape: %s", c, parsed.ToolCall.Arguments)
		}
	}
	// The undecodable payload itself is kept, not dropped.
	out, _ := PlainToolArguments(json.RawMessage(`{"tool_call":{"name":"bash","arguments":{"data":"not base64!!"}}}`))
	if !strings.Contains(string(out), "not base64!!") {
		t.Fatalf("the undecodable payload was lost: %s", out)
	}
}

// ParseLine is the stream-json producer: a harness line whose arguments are
// base64 must come out with the original text in Raw, or the journal keeps the
// blind spot. (This is the wiring test; the helper test above is the mechanism.)
func TestParseLineExposesPlainArguments(t *testing.T) {
	line := `{"tool_call":{"id":"c1","name":"bash","arguments":{"data":"` + b64(`{"command":"id"}`) + `","media_type":"application/json"}}}`
	ev := ParseLine(line)
	if ev.Kind != KindToolCall || ev.CallID != "c1" {
		t.Fatalf("parse = %+v, want a tool_call", ev)
	}
	if !strings.Contains(string(ev.Raw), `"arguments"`) {
		t.Fatalf("arguments disappeared from raw: %s", ev.Raw)
	}
	var outer struct {
		ToolCall struct {
			Arguments string `json:"arguments"`
		} `json:"tool_call"`
	}
	if err := json.Unmarshal(ev.Raw, &outer); err != nil {
		t.Fatalf("raw is not valid JSON: %v\n%s", err, ev.Raw)
	}
	if outer.ToolCall.Arguments != `{"command":"id"}` {
		t.Fatalf("arguments = %q, want the original text", outer.ToolCall.Arguments)
	}
}

// The producer must also surface the anomaly: a payload that cannot be decoded
// still lands as text, and the event says why instead of the journal silently
// keeping a shape no reader understands.
func TestParseLineNamesAnUndecodablePayload(t *testing.T) {
	line := `{"tool_call":{"id":"c2","name":"bash","arguments":{"data":"not base64!!"}}}`
	ev := ParseLine(line)
	if ev.Kind != KindToolCall {
		t.Fatalf("parse = %+v, want a tool_call", ev)
	}
	if !strings.Contains(ev.Warning, "base64") {
		t.Fatalf("warning = %q, want it to name the decode failure", ev.Warning)
	}
	if strings.Contains(string(ev.Raw), `"arguments":{`) {
		t.Fatalf("the journal kept the encoded object shape: %s", ev.Raw)
	}
}
