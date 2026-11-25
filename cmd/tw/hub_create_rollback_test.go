package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/project"
)

// A failed required write during project creation must roll the whole thing
// back: no project in the manager, no directory on disk, and a 5xx response
// that does not hand the operator a project_id pointing at a 404. The fault
// seam (h.createFault) fails one named step; the shared rollback path behind
// every step is the subject here.

func injectedCreateFault(failStep string) func(string) error {
	return func(step string) error {
		if step == failStep {
			return errors.New("注入：" + failStep + " 写入失败")
		}
		return nil
	}
}

func assertCreateRolledBack(t *testing.T, h *hub, mgr *project.Manager, code int, out map[string]any) {
	t.Helper()
	if code != http.StatusInternalServerError {
		t.Fatalf("create = %d %v; want 500 with nothing retained", code, out)
	}
	if out["code"] != "project_creation_rolled_back" {
		t.Fatalf("error code = %v; want project_creation_rolled_back", out["code"])
	}
	if _, has := out["project_id"]; has {
		t.Fatalf("response carries project_id %v for a project that must not exist", out["project_id"])
	}
	if got := len(mgr.All()); got != 0 {
		t.Fatalf("manager holds %d project(s) after a rolled-back create", got)
	}
	entries, err := os.ReadDir(mgr.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "p-") {
			t.Fatalf("rolled-back create left a directory behind: %s", e.Name())
		}
	}
	// The console index must not list it either.
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	w := httptest.NewRecorder()
	h.serveProjects(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), `"id"`) {
		t.Fatalf("GET /api/projects lists a project after rollback: %s", w.Body.String())
	}
}

// Options write failure: the 500 must not leave the half-created project the
// old flow produced (project on disk, 404 via /p/<id>/ because nothing was
// ever attached).
func TestCreateProjectRollsBackWhenOptionsWriteFails(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})
	h.createFault = injectedCreateFault("options")
	code, out := postProject(t, h, `{"name":"Acme","scope":"acme.example","goal":"拿到边界主机","reviews_disabled":true}`)
	assertCreateRolledBack(t, h, mgr, code, out)
	// Positive control lives in TestCreateProjectOptionsApplyBeforeAttach: the
	// same request with no injected fault attaches and returns 201.
}

// Goal write failure: reporting success without the endpoint is the exact
// "201 with nothing to judge done against" state the atomicity fix exists to
// remove.
func TestCreateProjectRollsBackWhenGoalWriteFails(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})
	h.createFault = injectedCreateFault("goal")
	code, out := postProject(t, h, `{"name":"Acme","scope":"acme.example","goal":"拿到边界主机"}`)
	assertCreateRolledBack(t, h, mgr, code, out)
}

// Description failure exercises the same rollback from the earliest optional
// write: no project survives a required-write failure at any step.
func TestCreateProjectRollsBackWhenDescriptionWriteFails(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})
	h.createFault = injectedCreateFault("description")
	code, out := postProject(t, h, `{"name":"Acme","scope":"acme.example","goal":"拿到边界主机","description":"边界与禁止动作"}`)
	assertCreateRolledBack(t, h, mgr, code, out)
}

// With no fault injected the programmatic no-goal creation keeps working: the
// rollback path must not swallow legitimate creations.
func TestCreateProjectWithoutGoalStillSucceeds(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})
	code, out := postProject(t, h, `{"name":"Acme","scope":"acme.example"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v; want 201", code, out)
	}
	if got := len(mgr.All()); got != 1 {
		t.Fatalf("manager holds %d project(s); want 1", got)
	}
	if _, err := os.Stat(filepath.Join(mgr.Root, out["id"].(string))); err != nil {
		t.Fatalf("created project directory missing: %v", err)
	}
}
