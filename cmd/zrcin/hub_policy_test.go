package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/project"
)

// The policy aggregate renders the fence honestly: a fence created via the
// shorthand reads back with the same mode, and an absent table is "unfenced"
// rather than a fabricated empty set.
func TestProjectPolicyAggregate(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})
	code, out := postProject(t, h, `{"name":"Fenced","scope":"fenced.example","fence":"enforced",
		"scope_rules":{"default_action":"review","rules":[{"action":"record","method":"*","host":"10.0.0.0/8"}]}}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if mgr.Get(id).FenceMode() != project.FenceEnforced {
		t.Fatal("precondition: project should be enforced")
	}

	r := httptest.NewRequest(http.MethodGet, "/api/projects/"+id+"/policy", nil)
	w := httptest.NewRecorder()
	h.serveProjectItem(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("policy = %d %s", w.Code, w.Body.String())
	}
	var view struct {
		Project   map[string]string `json:"project"`
		FenceMode string            `json:"fence_mode"`
		Audit     struct {
			Guaranteed bool `json:"guaranteed"`
		} `json:"audit"`
		ScopeRules *project.ScopeRules        `json:"scope_rules"`
		Capture    map[string]any             `json:"capture"`
		Extra      map[string]json.RawMessage `json:"-"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("policy is not JSON: %v (%s)", err, w.Body.String())
	}
	if view.FenceMode != project.FenceEnforced || !view.Audit.Guaranteed || view.Project["id"] != id {
		t.Fatalf("policy view = %+v", view)
	}
	if view.ScopeRules == nil || len(view.ScopeRules.Rules) != 1 {
		t.Fatalf("policy scope_rules = %+v", view.ScopeRules)
	}
	if view.Capture == nil && h.settings != nil {
		t.Fatal("policy capture projection missing although the store exists")
	}

	// An unfenced project: fence_mode=unfenced, scope_rules absent.
	code, out = postProject(t, h, `{"name":"Open","scope":"open.example"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, out)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/projects/"+out["id"].(string)+"/policy", nil)
	w = httptest.NewRecorder()
	h.serveProjectItem(w, r)
	var open map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &open); err != nil {
		t.Fatal(err)
	}
	if open["fence_mode"] != project.FenceUnfenced {
		t.Fatalf("open project fence_mode = %v", open["fence_mode"])
	}
	if _, ok := open["scope_rules"]; ok {
		t.Fatal("unfenced policy fabricated a scope_rules set")
	}
}
