package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/api"
	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/node"
	"github.com/kksty/tianwu/internal/project"
)

func transferNode(t *testing.T) *node.Registry {
	t.Helper()
	n, err := node.New([]node.Spec{{ID: "local", AOP: "http://127.0.0.1:1", Token: "test"}}, "local")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProjectExportAndImportHandlers(t *testing.T) {
	sourceData := t.TempDir()
	mgr, err := project.Open(filepath.Join(sourceData, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("迁移测试", "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.CaseDir, "in_1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.CaseDir, "in_1", "proof.txt"), []byte("proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.AddEvidence(model.Evidence{URI: "in_1/proof.txt", IntentID: "in_1"}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	srv := api.New(p.Store, nil, p.Dir, p.CaseDir)
	sourceHub := &hub{
		ctx:  context.Background(),
		cfg:  hubConfig{DataDir: sourceData, NoDecider: true},
		mgr:  mgr,
		kits: map[string]*kit{p.ID: {proj: p, srv: srv}},
	}
	rec := httptest.NewRecorder()
	sourceHub.exportProject(rec, httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/export", nil), p.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.Code, rec.Body.String())
	}
	var exported struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Name == "" || !strings.HasSuffix(exported.Name, ".twproj") {
		t.Fatalf("export name = %q", exported.Name)
	}
	archive := filepath.Join(sourceData, "exports", exported.Name)
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	download := httptest.NewRecorder()
	sourceHub.serveExport(download, httptest.NewRequest(http.MethodGet, "/api/exports/"+exported.Name, nil))
	if download.Code != http.StatusOK || !strings.HasPrefix(download.Body.String(), "PK") {
		t.Fatalf("download = %d len=%d", download.Code, download.Body.Len())
	}

	// Import into an independent instance. The archive is uploaded as the body,
	// which is the console path; the JSON path is covered by the same function.
	targetData := t.TempDir()
	targetMgr, err := project.Open(filepath.Join(targetData, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	targetHub := &hub{
		ctx:  context.Background(),
		cfg:  hubConfig{DataDir: targetData, NoDecider: true, Nodes: transferNode(t)},
		mgr:  targetMgr,
		kits: map[string]*kit{},
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
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
	if !got.IsPaused() {
		t.Fatal("imported project must stay paused")
	}
	if _, err := os.Stat(filepath.Join(got.CaseDir, "in_1", "proof.txt")); err != nil {
		t.Fatalf("imported evidence missing: %v", err)
	}

	// A running project cannot be exported: the archive must describe one state.
	if err := p.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	sourceHub.exportProject(rec, httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/export", nil), p.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("export while running = %d %s", rec.Code, rec.Body.String())
	}
}
