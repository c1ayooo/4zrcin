package api

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestShellObservationCountsExecutablesNotQuotedArguments(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`echo "curl https://example.test"; curl -I example.test && env TOKEN=fixture gogo --help`, []string{"echo", "curl", "gogo"}},
		{`bash -lc 'curl example.test | katana'`, []string{"curl", "katana"}},
		{`'C:/tools/gogo.exe' --help`, []string{"gogo"}},
		{"# curl | katana\ngogo --help # ignored; spray", []string{"gogo"}},
	} {
		if got := shellCommands(tc.command); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %v != %v", tc.command, got, tc.want)
		}
	}
}

func TestLeaderboardDecodesShellAndKeepsFailureAttributionHonest(t *testing.T) {
	s, _ := newBindingServer(t)
	dir := filepath.Join(s.DataDir, "runs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString([]byte(`{"command":"echo curl; curl example.test | katana"}`))
	events := []map[string]any{{"kind": "tool_call", "tool": "bash", "call_id": "c1", "raw": map[string]any{"tool_call": map[string]any{"arguments": map[string]string{"data": data}}}}, {"kind": "tool_result", "call_id": "c1", "ok": false, "duration_ms": 30}}
	var raw []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		raw = append(raw, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.jsonl"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	rows, n := s.toolLeaderboard()
	if n != 1 {
		t.Fatal(n)
	}
	found := map[string]ToolStat{}
	for _, row := range rows {
		key := row.Tool
		if row.Local {
			key = "local:" + key
		}
		found[key] = row
	}
	if found["bash"].Failures != 1 || found["bash"].DurationMS != 30 || found["local:curl"].Calls != 1 || found["local:katana"].Calls != 1 {
		t.Fatalf("wrong observations: %+v", found)
	}
	if found["local:curl"].Completed != 0 {
		t.Fatal("pipeline failure incorrectly attributed to each executable")
	}
}
