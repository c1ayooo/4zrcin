package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/api"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
)

// The import route in server form must accept the same .twproj package the
// desktop form does, restore the snapshot into the project's PostgreSQL schema
// and register the project — docs/16 §13 M0 ticket 5, docs/17 C11's route half.
func TestProjectImportHandlerServerForm(t *testing.T) {
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 ZRCIN_TEST_PG_DSN（scripts/test-pg.sh）")
	}
	// Source is a desktop-form instance: the package format is shared, so a
	// package exported on a laptop must import on the server untouched.
	sourceData := t.TempDir()
	sourceMgr, err := project.Open(filepath.Join(sourceData, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := sourceMgr.Create("迁移测试", "example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Store.Close() })
	if err := os.MkdirAll(filepath.Join(p.CaseDir, "in_1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.CaseDir, "in_1", "proof.txt"), []byte("proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.AddEvidence(model.Evidence{URI: "in_1/proof.txt", Digest: "c1cda26362828b69266512052b97cb3729e3b052e4ade47c0a1e3383defe73c7", IntentID: "in_1"}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	sourceHub := &hub{
		ctx:  context.Background(),
		cfg:  hubConfig{DataDir: sourceData, NoDecider: true},
		mgr:  sourceMgr,
		kits: map[string]*kit{p.ID: {proj: p, srv: api.New(p.Store, nil, p.Dir, p.CaseDir)}},
	}
	rec := httptest.NewRecorder()
	sourceHub.exportProject(rec, httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/export", nil), p.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.Code, rec.Body.String())
	}
	var exported struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(sourceData, "exports", exported.Name)
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}

	targetData := t.TempDir()
	targetMgr, err := project.OpenPooledDSN(filepath.Join(targetData, "projects"), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, tp := range targetMgr.All() {
			_ = tp.Store.Close()
		}
	})
	targetHub := &hub{
		ctx:  context.Background(),
		cfg:  hubConfig{DataDir: targetData, NoDecider: true, Nodes: transferNode(t)},
		mgr:  targetMgr,
		kits: map[string]*kit{},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	rec = httptest.NewRecorder()
	targetHub.importProject(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	got := targetMgr.Get(p.ID)
	if got == nil {
		t.Fatalf("imported project %s is not registered", p.ID)
	}
	t.Cleanup(func() { _ = got.Store.Close() })
	if !got.IsPaused() {
		t.Fatal("imported project must stay paused")
	}
	if _, err := os.Stat(filepath.Join(got.CaseDir, "in_1", "proof.txt")); err != nil {
		t.Fatalf("imported evidence missing: %v", err)
	}
	// The ledger lives in PostgreSQL: no SQLite file may be published beside the
	// project files.
	if _, err := os.Stat(filepath.Join(got.Dir, "ledger.sqlite")); !os.IsNotExist(err) {
		t.Fatal("server-form import must not publish a ledger.sqlite file")
	}
	snap := got.Store.Snapshot()
	if len(snap.Evidence) != 1 || snap.Evidence[0].URI != "in_1/proof.txt" {
		t.Fatalf("snapshot evidence = %+v", snap.Evidence)
	}

	// Importing the same package again must be refused, not merged.
	req = httptest.NewRequest(http.MethodPost, "/api/projects/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	rec = httptest.NewRecorder()
	targetHub.importProject(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second import = %d %s", rec.Code, rec.Body.String())
	}
}
