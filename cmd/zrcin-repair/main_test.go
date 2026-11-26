package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/ledger"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// The repair tool edits a project while the control plane is stopped, so its
// argument checks are the first guard: a typo must not run against a relative
// path or a broad intent selector.
func TestRunRejectsUnsafeArguments(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no flags", nil},
		{"relative project", []string{"--project", "data/projects/p1", "--intent", "in_1"}},
		{"missing intent", []string{"--project", dir, "--intent", ""}},
		{"intent path", []string{"--project", dir, "--intent", "in_1/raw"}},
		{"dot intent", []string{"--project", dir, "--intent", "."}},
		{"dotdot intent", []string{"--project", dir, "--intent", ".."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(tc.args, &out)
			if err == nil || !strings.Contains(err.Error(), "必须提供 --project 和精确的 --intent") {
				t.Fatalf("run(%v) = %v, want the argument error", tc.args, err)
			}
			if out.Len() != 0 {
				t.Fatalf("rejected run printed a plan: %q", out.String())
			}
		})
	}
}

func TestRunRequiresLedgerSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": true})
	var out bytes.Buffer
	if err := run([]string{"--project", dir, "--intent", "in_1"}, &out); err == nil {
		t.Fatal("a paused project without its ledger must not plan a repair")
	}
}

func TestRunRefusesProjectThatIsNotPaused(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": false})
	var out bytes.Buffer
	err := run([]string{"--project", dir, "--intent", "in_1"}, &out)
	if err == nil || !strings.Contains(err.Error(), "项目必须暂停") {
		t.Fatalf("run = %v, want the paused-project refusal", err)
	}
}

func TestRunPreviewsPlanAndRefusesEmptyCommit(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": true})
	intentID := seedLedger(t, filepath.Join(dir, "ledger.sqlite"))
	// A handoff file that was already fully consumed: the plan has nothing to
	// advance, so the dry run prints it and --commit refuses to write.
	work := filepath.Join(dir, "cases", intentID)
	if err := os.MkdirAll(filepath.Join(work, ledger.HandoffDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ledger.HandoffDir, ledger.HandoffFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"--project", dir, "--intent", intentID}, &out); err != nil {
		t.Fatalf("preview: %v", err)
	}
	var plan ledger.CursorRepair
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("preview is not a plan: %v %s", err, out.String())
	}
	if plan.Before != 0 || plan.Through != 0 || len(plan.Problems) != 0 {
		t.Fatalf("unexpected empty plan: %+v", plan)
	}

	if err := run([]string{"--project", dir, "--intent", intentID, "--commit"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "没有待修复游标") {
		t.Fatalf("commit = %v, want the empty-cursor refusal", err)
	}
}

func TestRunRefusesUnknownIntent(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": true})
	seedLedger(t, filepath.Join(dir, "ledger.sqlite"))
	err := run([]string{"--project", dir, "--intent", "in_missing"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "项目中没有意图") {
		t.Fatalf("run = %v, want the unknown-intent refusal", err)
	}
}

func TestConfidenceRepairPreviewsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": true})
	seedLedger(t, filepath.Join(dir, "ledger.sqlite"))
	var out bytes.Buffer
	if err := run([]string{"--project", dir, "--confidence"}, &out); err != nil {
		t.Fatal(err)
	}
	var plan store.ConfidenceRepair
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil || plan.Version.Epoch == "" || len(plan.Changes) != 0 || len(plan.Blocked) != 0 {
		t.Fatalf("confidence preview = %s (%v)", out.String(), err)
	}
	if backups, err := filepath.Glob(filepath.Join(dir, "ledger-before-confidence-*.sqlite")); err != nil || len(backups) != 0 {
		t.Fatalf("preview wrote a backup: %v (%v)", backups, err)
	}
}

// seedDocuments writes a ledger that carries both manifest-backed documents at
// the given texts, so --manifest has an audited truth to rebuild from.
func seedDocuments(t *testing.T, path string, scope, constraints string) (uint64, uint64) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scopeDoc, err := st.SaveDocument(0, store.DocumentScope, scope, "operator")
	if err != nil {
		t.Fatal(err)
	}
	consDoc, err := st.SaveDocument(0, store.DocumentConstraints, constraints, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return scopeDoc.Revision, consDoc.Revision
}

// The dry run prints the planned text and writes nothing: project.json stays
// byte for byte what it was.
func TestManifestRepairPreviewsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": true, "scope": "stale-scope", "description": "旧约束"})
	scopeRev, consRev := seedDocuments(t, filepath.Join(dir, "ledger.sqlite"), "10.0.0.0/24", "不要删除或修改真实数据。")
	before, err := os.ReadFile(filepath.Join(dir, "project.json"))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"--project", dir, "--manifest"}, &out); err != nil {
		t.Fatalf("preview: %v", err)
	}
	var plan struct {
		Project string `json:"project"`
		Scope   *struct {
			Before, After string
			Revision      uint64
			Changed       bool
		} `json:"scope"`
		Description *struct {
			Before, After string
			Revision      uint64
			Changed       bool
		} `json:"description"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("preview is not a plan: %v %s", err, out.String())
	}
	if plan.Project != "p1" || plan.Scope == nil || plan.Description == nil {
		t.Fatalf("plan missing fields: %s", out.String())
	}
	if plan.Scope.Before != "stale-scope" || plan.Scope.After != "10.0.0.0/24" || !plan.Scope.Changed || plan.Scope.Revision != scopeRev {
		t.Fatalf("scope plan = %+v", plan.Scope)
	}
	if plan.Description.Before != "旧约束" || plan.Description.After != "不要删除或修改真实数据。" || !plan.Description.Changed || plan.Description.Revision != consRev {
		t.Fatalf("description plan = %+v", plan.Description)
	}
	after, err := os.ReadFile(filepath.Join(dir, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the dry run rewrote project.json")
	}
}

// The commit rewrites the manifest from the ledger, keeps every other manifest
// field, and leaves a backup of the pre-repair file next to it.
func TestManifestRepairCommitRewritesFromLedger(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": true, "scope": "stale-scope", "description": "旧约束"})
	seedDocuments(t, filepath.Join(dir, "ledger.sqlite"), "10.0.0.0/24", "不要删除或修改真实数据。")

	if err := run([]string{"--project", dir, "--manifest", "--commit"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["scope"] != "10.0.0.0/24" || m["description"] != "不要删除或修改真实数据。" {
		t.Fatalf("manifest after repair = %v", m)
	}
	// The rebuild is a projection fix, not a rewrite: identity fields survive.
	if m["id"] != "p1" || m["name"] != "fixture" || m["paused"] != true {
		t.Fatalf("repair lost manifest identity fields: %v", m)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "project.json.bak-*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup after repair = %v (%v)", backups, err)
	}
}

// A rebuild is refused while the project is running (not paused), like every
// other repair mode: the manifest belongs to the stopped control plane.
func TestManifestRepairRefusesProjectThatIsNotPaused(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": false})
	err := run([]string{"--project", dir, "--manifest"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "项目必须暂停") {
		t.Fatalf("run = %v, want the paused-project refusal", err)
	}
}

// Nothing to do: the manifest already matches the ledger, so --commit refuses
// instead of rewriting the file for no reason.
func TestManifestRepairRefusesCommitWhenInSync(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{"id": "p1", "name": "fixture", "paused": true, "scope": "10.0.0.0/24", "description": "不要删除或修改真实数据。"})
	seedDocuments(t, filepath.Join(dir, "ledger.sqlite"), "10.0.0.0/24", "不要删除或修改真实数据。")
	err := run([]string{"--project", dir, "--manifest", "--commit"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "manifest 与账本一致") {
		t.Fatalf("run = %v, want the in-sync refusal", err)
	}
}

func writeManifest(t *testing.T, dir string, content map[string]any) {
	t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedLedger writes a real ledger with one pending direction and returns the
// generated intent id (the store owns the short-id scheme).
func seedLedger(t *testing.T, path string) string {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	in, err := st.CreateIntent(model.Intent{Title: "fixture"})
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return in.ID
}

// Server form: --pg-dsn points the plan (and, on commit, the confidence
// rewrite) at the project's schema in PostgreSQL; the file form keeps its
// exact open path (docs/16 §13 M0 ticket 6). Dry-run only: the plan must match
// what the same rows say in SQLite form.
func TestRunManifestPlanReadsPostgreSQL(t *testing.T) {
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 ZRCIN_TEST_PG_DSN（scripts/test-pg.sh）")
	}
	dir := filepath.Join(t.TempDir(), "p-pg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, dir, map[string]any{"id": "p-pg", "name": "fixture", "paused": true, "scope": "旧范围", "description": "旧说明"})
	// The test database is shared across runs; drop a previous run's schema.
	if err := store.DropProjectPG(dsn, "p-pg"); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenProjectPG(dsn, "p-pg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveDocument(0, store.DocumentScope, "新范围", "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveDocument(0, store.DocumentConstraints, "新说明", "operator"); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	t.Cleanup(func() { _ = store.DropProjectPG(dsn, "p-pg") })
	var stdout bytes.Buffer
	if err := run([]string{"--project", dir, "--manifest", "--pg-dsn", dsn}, &stdout); err != nil {
		t.Fatalf("run: %v", err)
	}
	var plan struct {
		Scope       *struct{ Before, After string } `json:"scope"`
		Description *struct{ Before, After string } `json:"description"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Scope == nil || plan.Scope.Before != "旧范围" || plan.Scope.After != "新范围" {
		t.Fatalf("scope plan = %+v", plan.Scope)
	}
	if plan.Description == nil || plan.Description.After != "新说明" {
		t.Fatalf("description plan = %+v", plan.Description)
	}
	// Dry-run: the manifest file is untouched.
	raw, _ := os.ReadFile(filepath.Join(dir, "project.json"))
	if !strings.Contains(string(raw), "旧范围") {
		t.Fatal("dry-run rewrote the manifest")
	}
}
