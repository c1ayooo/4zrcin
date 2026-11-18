package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRetentionHarness(t *testing.T, dir, body string) string {
	t.Helper()
	harness := filepath.Join(dir, "cyber.yaml")
	if err := os.WriteFile(harness, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return harness
}

func loadRetentionStore(t *testing.T, harness string) *Store {
	t.Helper()
	st, err := LoadAt(filepath.Join(filepath.Dir(harness), "settings.json"), harness)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

const retentionModelSection = `llm:
    active_profile: fast
    providers:
        - id: fast
          provider: openai
          base_url: http://fast:8080
          api_key: fast-secret
          model: fast-model
`

// Without an explicit value the console shows the 2 GiB default, the range the
// form may accept and where the number came from. The JSON field names are the
// front-end contract.
func TestTrafficRetentionDefaults(t *testing.T) {
	dir := t.TempDir()
	st := loadRetentionStore(t, writeRetentionHarness(t, dir, retentionModelSection))

	retention := st.TrafficRetention()
	if retention.Bytes != 2<<30 || retention.Default != 2<<30 {
		t.Fatalf("bytes/default = %d/%d, want 2 GiB", retention.Bytes, retention.Default)
	}
	if retention.Min != 2*(8<<20) {
		t.Fatalf("min = %d, want 16 MiB (2x default body_max_bytes)", retention.Min)
	}
	if retention.Max != MaxTrafficBodyRetentionBytes {
		t.Fatalf("max = %d, want 1 TiB", retention.Max)
	}
	if retention.Explicit {
		t.Fatal("default reported as explicit")
	}
	if !strings.Contains(retention.Source, "默认") {
		t.Fatalf("source does not say default: %q", retention.Source)
	}

	raw, err := json.Marshal(st.View())
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	// The wire value is the explicit override or 0; the console renders 0 as
	// "default 2 GiB", so the generated default never looks operator-set.
	if view["traffic_body_retention_bytes"] != float64(0) {
		t.Fatalf("view traffic_body_retention_bytes = %v, want 0 (no explicit value)", view["traffic_body_retention_bytes"])
	}
	if view["traffic_body_retention_effective_bytes"] != float64(2<<30) {
		t.Fatalf("view effective = %v, want 2 GiB", view["traffic_body_retention_effective_bytes"])
	}
	for _, key := range []string{
		"traffic_body_retention_bytes", "traffic_body_retention_effective_bytes",
		"traffic_body_retention_default", "traffic_body_retention_min_bytes",
		"traffic_body_retention_max_bytes", "traffic_body_retention_explicit",
		"traffic_body_retention_source",
	} {
		if _, ok := view[key]; !ok {
			t.Fatalf("view JSON lacks %q: %s", key, raw)
		}
	}
}

// An explicit operator value and its body limits win over the defaults, and the
// generated host runtime document carries them to the scan.
func TestTrafficRetentionReadsExplicitOperatorValue(t *testing.T) {
	dir := t.TempDir()
	body := retentionModelSection + `traffic:
    body_storage: disk
    body_max_bytes: 16777216
    body_retention_bytes: 68719476736
agent:
    timeout: 60
`
	harness := writeRetentionHarness(t, dir, body)
	st := loadRetentionStore(t, harness)

	retention := st.TrafficRetention()
	if !retention.Explicit || retention.Bytes != 64<<30 {
		t.Fatalf("retention = %+v, want explicit 64 GiB", retention)
	}
	if retention.Min != 32<<20 {
		t.Fatalf("min = %d, want 2x explicit body_max_bytes (32 MiB)", retention.Min)
	}
	if !strings.Contains(retention.Source, "body_retention_bytes") {
		t.Fatalf("source does not name the operator key: %q", retention.Source)
	}

	runtimePath, err := st.RuntimeConfig(filepath.Join(dir, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDoc := string(data)
	for _, want := range []string{
		"body_storage: disk",
		"body_max_bytes: 16777216",
		"body_retention_bytes: 68719476736",
		"timeout: 60",
	} {
		if !strings.Contains(runtimeDoc, want) {
			t.Fatalf("runtime config missing %q:\n%s", want, runtimeDoc)
		}
	}
}

// A save writes only traffic.body_retention_bytes: other traffic fields and
// other sections keep their values, and 0 removes the explicit key again.
func TestSetTrafficBodyRetentionPreservesOtherSections(t *testing.T) {
	dir := t.TempDir()
	body := retentionModelSection + `traffic:
    body_storage: none
    body_max_bytes: 16777216
agent:
    timeout: 60
extensions:
    mcp:
        mcpServers:
            ida:
                command: idalib-mcp
`
	harness := writeRetentionHarness(t, dir, body)
	st := loadRetentionStore(t, harness)

	if err := st.SetTrafficBodyRetention(128 << 30); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	saved := string(data)
	for _, want := range []string{
		"body_retention_bytes: 137438953472",
		"body_storage: none",
		"body_max_bytes: 16777216",
		"timeout: 60",
		"idalib-mcp",
	} {
		if !strings.Contains(saved, want) {
			t.Fatalf("saved harness config missing %q:\n%s", want, saved)
		}
	}
	retention := st.TrafficRetention()
	if !retention.Explicit || retention.Bytes != 128<<30 {
		t.Fatalf("retention after save = %+v", retention)
	}
	runtimePath, err := st.RuntimeConfig(filepath.Join(dir, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeData, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runtimeData), "body_retention_bytes: 137438953472") {
		t.Fatalf("runtime config did not pick up the saved retention:\n%s", runtimeData)
	}

	// 0 = remove the override and fall back to the default.
	if err := st.SetTrafficBodyRetention(0); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "body_retention_bytes:") {
		t.Fatalf("0 did not remove the explicit value:\n%s", data)
	}
	after := st.TrafficRetention()
	if after.Explicit || after.Bytes != after.Default {
		t.Fatalf("after reset = %+v, want default and not explicit", after)
	}
}

func TestValidateTrafficBodyRetention(t *testing.T) {
	dir := t.TempDir()
	harness := writeRetentionHarness(t, dir, retentionModelSection)
	st := loadRetentionStore(t, harness)

	invalid := []struct {
		name  string
		bytes int64
		hint  string
	}{
		{"negative", -1, "负数"},
		{"below default minimum", 2*(8<<20) - 1, "至少"},
		{"above cap", 1<<40 + 1, "1 TiB"},
	}
	before, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			err := st.SetTrafficBodyRetention(c.bytes)
			if err == nil {
				t.Fatalf("value %d accepted", c.bytes)
			}
			if !strings.Contains(err.Error(), c.hint) {
				t.Fatalf("error %q does not mention %q", err, c.hint)
			}
		})
	}
	for _, valid := range []int64{0, 2 * (8 << 20), 1 << 40} {
		if err := st.ValidateTrafficBodyRetention(valid); err != nil {
			t.Fatalf("value %d rejected: %v", valid, err)
		}
	}
	after, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a rejected save changed the operator file:\n%s", after)
	}
}

// The lower bound follows the operator's explicit body_max_bytes, not the
// compiled-in default: 16 MiB bodies need at least a 32 MiB retention budget.
func TestTrafficRetentionMinFollowsExplicitBodyMax(t *testing.T) {
	dir := t.TempDir()
	body := retentionModelSection + `traffic:
    body_max_bytes: 16777216
`
	st := loadRetentionStore(t, writeRetentionHarness(t, dir, body))
	if got := st.TrafficRetention().Min; got != 32<<20 {
		t.Fatalf("min = %d, want 32 MiB", got)
	}
	if err := st.ValidateTrafficBodyRetention(32<<20 - 1); err == nil {
		t.Fatal("value below 2x explicit body_max_bytes accepted")
	}
	if err := st.ValidateTrafficBodyRetention(32 << 20); err != nil {
		t.Fatalf("boundary value rejected: %v", err)
	}
}
