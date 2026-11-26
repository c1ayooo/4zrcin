package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// fenceHub mirrors runtimeSettingsHub: the smallest hub that serves
// /api/fence — a temp legacy store so no real home file is read or written,
// exercised through the hub's own root switch, which is what makes it an
// operator surface.
func fenceHub(t *testing.T) (*hub, *httptest.ResponseRecorder, *settings.Store) {
	t.Helper()
	dir := t.TempDir()
	legacy := filepath.Join(dir, "settings.json")
	st, err := settings.LoadAt(legacy, "")
	if err != nil {
		t.Fatal(err)
	}
	h := &hub{settings: st, kits: map[string]*kit{}}
	rec := httptest.NewRecorder()
	return h, rec, st
}

func callFence(t *testing.T, h *hub, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/fence", nil)
	} else {
		req = httptest.NewRequest(method, "/api/fence", strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The instance fence endpoint: GET returns the stored rows (an empty fence is
// an empty object, not an error), PUT normalizes and persists — a wildcard
// spelling lands as the bare domain, a missing action becomes deny, ids are
// generated — and an invalid row is rejected without landing half a fence.
func TestFenceEndpointRoundTrip(t *testing.T) {
	h, rec, store := fenceHub(t)

	if rec := callFence(t, h, http.MethodGet, ""); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"fence":{}}` {
		t.Fatalf("empty GET = %d %s", rec.Code, rec.Body.String())
	}

	rec = callFence(t, h, http.MethodPut, `{
		"enabled_commands": true,
		"commands": [{"id": "shutdown-line", "pattern": "shutdown\\s+-h\\b", "desc": "关机", "action": "deny"}],
		"enabled_domains": true,
		"domains": [{"host": "*.gov.example"}]
	}`)
	if rec.Code != 200 {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"shutdown-line"`) || !strings.Contains(rec.Body.String(), `"host":"gov.example"`) {
		t.Fatalf("PUT response lost the normalized rows: %s", rec.Body.String())
	}
	stored := store.FenceOf()
	if !stored.EnabledCommands || len(stored.Commands) != 1 || stored.Commands[0].ID != "shutdown-line" {
		t.Fatalf("stored fence = %+v", stored)
	}
	if len(stored.Domains) != 1 || stored.Domains[0].Host != "gov.example" {
		t.Fatalf("stored domains = %+v", stored.Domains)
	}

	// An invalid row (an uncompilable pattern) is a 400 and stores nothing.
	rec = callFence(t, h, http.MethodPut, `{"enabled_commands":true,"commands":[{"id":"x","pattern":"(","action":"deny"}]}`)
	if rec.Code != 400 {
		t.Fatalf("invalid PUT = %d %s", rec.Code, rec.Body.String())
	}
	if after := store.FenceOf(); len(after.Commands) != 1 {
		t.Fatalf("the rejected PUT landed anyway: %+v", after)
	}
	// A switch-only PUT keeps the stored rows: flipping a red line off must
	// not silently clear what the operator wrote.
	rec = callFence(t, h, http.MethodPut, `{"enabled_commands":false}`)
	if rec.Code != 200 {
		t.Fatalf("switch-only PUT = %d %s", rec.Code, rec.Body.String())
	}
	after := store.FenceOf()
	if after.EnabledCommands || len(after.Commands) != 1 || after.Commands[0].ID != "shutdown-line" {
		t.Fatalf("switch-only PUT changed the rows: %+v", after)
	}
	if rec := callFence(t, h, http.MethodDelete, ""); rec.Code != 405 {
		t.Fatalf("DELETE = %d", rec.Code)
	}
}

// The preset load endpoint: one POST appends the download / delete / upload
// rows through the same normalization a hand save runs, a second POST is a
// no-op (idempotent by id and pattern), and an operator row that already
// covers a pattern is kept — never duplicated or overwritten.
func TestFencePresetEndpoint(t *testing.T) {
	h, _, store := fenceHub(t)
	// An operator row whose pattern matches a preset: the load must skip it.
	if err := store.SetFence(settings.Fence{EnabledCommands: true, Commands: []settings.GlobalCommandRule{
		{ID: "op-wget", Pattern: `\bwget\b`, Desc: "操作员自己的 wget 行", Action: settings.FenceActionReview},
	}}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fence/preset", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("preset load = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Fence settings.Fence `json:"fence"`
		Added int            `json:"added"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Added != len(settings.PresetCommands())-1 {
		t.Fatalf("added = %d, want %d (the operator's wget row is kept)", out.Added, len(settings.PresetCommands())-1)
	}
	if len(out.Fence.Commands) != len(settings.PresetCommands()) {
		t.Fatalf("commands = %d, want %d", len(out.Fence.Commands), len(settings.PresetCommands()))
	}
	kept := false
	for _, r := range out.Fence.Commands {
		if r.ID == "op-wget" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the operator's own row was dropped")
	}

	// A second load is a no-op.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/fence/preset", nil))
	if rec2.Code != 200 {
		t.Fatalf("second load = %d", rec2.Code)
	}
	var again struct {
		Added int `json:"added"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &again)
	if again.Added != 0 {
		t.Fatalf("second load added %d rows, want 0", again.Added)
	}
	if got := store.FenceOf(); len(got.Commands) != len(settings.PresetCommands()) {
		t.Fatalf("stored fence = %d rows", len(got.Commands))
	}
}
