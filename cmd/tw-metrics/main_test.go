package main

// tw-metrics tests: the read-only guarantee, the stable JSON envelope, and the
// tolerant sidecar parsing. They build a temporary ledger through the real store
// so the immutable reader is exercised against a genuine SQLite file; nothing
// here reads data/.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kksty/tianwu/internal/metrics"
	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/store"
)

// buildProject creates <dir>/ledger.sqlite with a small committed ledger and
// returns the project directory. It closes the store, so the file is static from
// the tool's point of view.
func buildProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ledger.sqlite"))
	if err != nil {
		t.Fatalf("open temp ledger: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	in, err := st.CreateIntent(model.Intent{Title: "验证边界", Description: "固定夹具"})
	if err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if _, err := st.AddFact(model.Fact{
		ID: "f_1", IntentID: in.ID, Kind: "host", Title: "10.0.0.5:8080",
		Summary: "固定摘要", Status: "unverified", Confidence: "observed",
	}); err != nil {
		t.Fatalf("add fact: %v", err)
	}
	r, err := st.AddRun(model.Run{IntentID: in.ID, Purpose: "commander", Node: "local"})
	if err != nil {
		t.Fatalf("add run: %v", err)
	}
	end := time.Now()
	if _, err := st.UpdateRun(r.ID, func(run *model.Run) {
		run.Status = model.RunDone
		run.EndedAt = &end
		run.ExitReason = "completed"
	}); err != nil {
		t.Fatalf("settle run: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close temp ledger: %v", err)
	}
	return dir
}

// dirFingerprint hashes every file name and byte in dir (one level; the tool
// only ever reads/writes there).
func dirFingerprint(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = "dir"
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		sum := sha256.Sum256(raw)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

func sameFingerprint(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestRunIsReadOnlyAndLeavesNoSideFiles(t *testing.T) {
	dir := buildProject(t)
	before := dirFingerprint(t, dir)
	var out, errOut bytes.Buffer
	if code := run([]string{"--project", dir, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	after := dirFingerprint(t, dir)
	if !sameFingerprint(before, after) {
		t.Fatalf("source project changed:\nbefore=%v\nafter=%v", before, after)
	}
	for _, name := range []string{"ledger.sqlite-wal", "ledger.sqlite-shm"} {
		if _, ok := after[name]; ok {
			t.Fatalf("read created %s next to the source ledger", name)
		}
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if !env.SourceUnchanged || len(env.ChangedFiles) != 0 {
		t.Fatalf("source_unchanged=%t changed=%v", env.SourceUnchanged, env.ChangedFiles)
	}
	if env.ReadMode != "direct_immutable" && env.ReadMode != "temp_copy" {
		t.Fatalf("unexpected read mode %q", env.ReadMode)
	}
	if len(env.Metrics) != 7 {
		t.Fatalf("metrics = %d", len(env.Metrics))
	}
	if env.Counts.Facts != 1 || env.Counts.Intents != 1 || env.Counts.Runs != 1 {
		t.Fatalf("counts = %+v", env.Counts)
	}
}

func TestJSONOutputIsStable(t *testing.T) {
	dir := buildProject(t)
	var first, firstErr bytes.Buffer
	if code := run([]string{"--project", dir, "--json"}, &first, &firstErr); code != 0 {
		t.Fatalf("first run exit %d: %s", code, firstErr.String())
	}
	var second, secondErr bytes.Buffer
	if code := run([]string{"--project", dir, "--json"}, &second, &secondErr); code != 0 {
		t.Fatalf("second run exit %d: %s", code, secondErr.String())
	}
	if first.String() != second.String() {
		t.Fatalf("JSON output is not stable:\n%s\n---\n%s", first.String(), second.String())
	}
	var env Envelope
	if err := json.Unmarshal(first.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, m := range env.Metrics {
		if m.Order != i+1 {
			t.Fatalf("metric order = %d at index %d", m.Order, i)
		}
	}
}

func TestHumanOutputListsEveryMetric(t *testing.T) {
	dir := buildProject(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"--project", dir}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, key := range []string{
		metrics.KeyHandoffReceiptValidRate,
		metrics.KeyOperatorInstructionConsumptionLatency,
		metrics.KeyFactsAfterStop,
		metrics.KeyUnconsumedVerifiedFacts,
		metrics.KeyDirectionStallRate,
		metrics.KeyEnvironmentMisjudgementRatio,
		metrics.KeyReviewQueueAndExecutionMS,
	} {
		if !strings.Contains(out.String(), key) {
			t.Fatalf("human output missing %s:\n%s", key, out.String())
		}
	}
	if !strings.Contains(out.String(), "source_unchanged=true") {
		t.Fatalf("human output does not report source_unchanged:\n%s", out.String())
	}
}

func TestReadDecisionsToleratesMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "decisions.jsonl")
	got, present, malformed, warnings := readDecisions(missing)
	if got != nil || present || malformed != 0 || len(warnings) != 0 {
		t.Fatalf("missing file = %v %t %d %v", got, present, malformed, warnings)
	}

	path := filepath.Join(dir, "decisions.jsonl")
	content := `{"run_id":"r_1","handoff":true}
{"run_id":"r_2","actions":[{"kind":"intent","id":"in_1"}]}

not-json
{"run_id":"r_3","handoff":true` // trailing partial line from a crash
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, present, malformed, _ = readDecisions(path)
	if !present || len(got) != 2 || malformed != 2 {
		t.Fatalf("parsed=%d present=%t malformed=%d", len(got), present, malformed)
	}
	if !got[0].Handoff || got[0].RunID != "r_1" {
		t.Fatalf("first decision = %+v", got[0])
	}
	if len(got[1].Actions) != 1 || got[1].Actions[0].Kind != "intent" {
		t.Fatalf("second decision = %+v", got[1])
	}
}

func TestReadOperatorScopeToleratesMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "operator-scope.jsonl")
	got, present, malformed, _ := readOperatorScope(missing)
	if got != nil || present || malformed != 0 {
		t.Fatalf("missing file = %v %t %d", got, present, malformed)
	}
	path := filepath.Join(dir, "operator-scope.jsonl")
	content := `{"v":1,"id":"os_1","kind":"stop","at":"2026-01-02T03:04:05Z","actor":"operator","entity_refs":["host:10.0.0.5"]}
{"v":1,"id":"os_2","kind":"authorization","at":"2026-01-02T03:04:05Z"}
broken`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, present, malformed, _ = readOperatorScope(path)
	if !present || len(got) != 2 || malformed != 1 {
		t.Fatalf("parsed=%d present=%t malformed=%d", len(got), present, malformed)
	}
	if got[0].Kind != "stop" || len(got[0].EntityRefs) != 1 || got[0].EntityRefs[0] != "host:10.0.0.5" {
		t.Fatalf("stop entry = %+v", got[0])
	}
}

func TestMissingLedgerFails(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	if code := run([]string{"--project", dir}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "账本不可读") {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestUsageErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("missing --project exit = %d", code)
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"--project", "x", "extra"}, &out, &errOut); code != 2 {
		t.Fatalf("positional arg exit = %d", code)
	}
}

func TestTempCopyFallbackReadsCommittedRows(t *testing.T) {
	dir := buildProject(t)
	ledger := filepath.Join(dir, "ledger.sqlite")
	db, err := readLedgerCopy(ledger)
	if err != nil {
		t.Fatalf("copy read: %v", err)
	}
	if len(db.Facts) != 1 || len(db.Runs) != 1 || db.Version != store.CurrentSchemaVersion() {
		t.Fatalf("copy read = %+v", db)
	}
	// The source directory still has exactly the files it had before the copy.
	if _, err := os.Stat(ledger + "-wal"); err == nil {
		if info, _ := os.Stat(ledger + "-wal"); info.Size() > 0 {
			t.Fatalf("copy read left source WAL data")
		}
	}
}

func TestLedgerNeedsCopy(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.sqlite")
	if err := os.WriteFile(ledger, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ledgerNeedsCopy(ledger) {
		t.Fatal("no WAL file should not require a copy")
	}
	if err := os.WriteFile(ledger+"-wal", []byte("committed rows"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !ledgerNeedsCopy(ledger) {
		t.Fatal("non-empty WAL should require a copy")
	}
}
