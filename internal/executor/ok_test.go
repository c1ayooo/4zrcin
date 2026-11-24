package executor

import (
	"encoding/json"
	"strings"
	"testing"
)

// The outcome bit has to be readable as text: a failed tool_result used to lose
// its false to omitempty, so a human or a model reading the journal could not
// tell a failure from an ordinary text event.
func TestOKBitSurvivesMarshallingWhenFalse(t *testing.T) {
	failed := Event{Kind: KindToolResult, Tool: "bash"}
	failed.OK = BoolPtr(false)
	line, err := json.Marshal(failed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"ok":false`) {
		t.Fatalf("a failed tool_result lost its ok bit: %s", line)
	}

	// A kind that carries no outcome writes no bit at all: "not applicable" and
	// "failed" must stay distinguishable.
	text := Event{Kind: KindMessage, Role: "assistant", Text: "hi"}
	line, err = json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(line), `"ok"`) {
		t.Fatalf("a text event grew an outcome bit: %s", line)
	}

	// A row without the bit carries no outcome at all: it is neither a success
	// nor a failure, so it must not be counted as one.
	var absent Event
	if err := json.Unmarshal([]byte(`{"kind":"tool_result","tool":"bash"}`), &absent); err != nil {
		t.Fatal(err)
	}
	if absent.IsOK() || absent.NotOK() {
		t.Fatalf("row without the bit = IsOK %v NotOK %v, want neither", absent.IsOK(), absent.NotOK())
	}

	// Positive control: a recorded success is not a failure.
	ok := Event{Kind: KindToolResult, Tool: "bash"}
	ok.OK = BoolPtr(true)
	if !ok.IsOK() || ok.NotOK() {
		t.Fatalf("recorded success = IsOK %v NotOK %v, want IsOK", ok.IsOK(), ok.NotOK())
	}
}
