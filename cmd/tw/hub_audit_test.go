package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// One journal line as the executor writes it: the kind probe and the
// structured guardrail fields are the whole contract appendAuditRows reads.
func auditEvent(at time.Time, action, reason, tool string) []byte {
	e := executor.Event{
		Kind:            executor.KindGuardrailDecision,
		At:              at,
		GuardrailAction: action,
		GuardrailReason: reason,
		GuardrailTool:   tool,
		GuardrailRuleID: func() string {
			for _, token := range splitTokens(reason) {
				if len(token) > 5 && token[:5] == "rule=" {
					return token[5:]
				}
			}
			return ""
		}(),
	}
	b, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return b
}

func splitTokens(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func writeJournal(t *testing.T, dataDir, runID string, lines ...[]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dataDir, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for _, line := range lines {
		b = append(b, line...)
		b = append(b, '\n')
	}
	if err := os.WriteFile(filepath.Join(dataDir, "runs", runID+".jsonl"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The audit rows are the journal's guardrail decisions with the filters
// applied. "builtin" selects the execution-side guaranteed rows (reason
// prefix), "rule" selects project scope-rule verdicts, and unknown reasons
// never count as builtin — a project cannot fake its way into (or out of) the
// guaranteed audit.
func TestAppendAuditRowsFilters(t *testing.T) {
	dataDir := t.TempDir()
	base := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	writeJournal(t, dataDir, "r1",
		auditEvent(base.Add(time.Second), "record", "builtin.high-risk command pattern", "shell"),
		auditEvent(base.Add(2*time.Second), "review", "rule=sr-2 matched 10.0.0.0/8", "http"),
		[]byte(`{"kind":"http","at":"`+base.Add(3*time.Second).Format(time.RFC3339Nano)+`"}`),
		[]byte(`not json at all`),
	)
	writeJournal(t, dataDir, "r2",
		auditEvent(base, "block", "rule=sr-1 deny list", "shell"),
	)

	all := appendAuditRows(nil, dataDir, "r1", "i1", "", "", "")
	if len(all) != 2 {
		t.Fatalf("all decisions = %d rows, want 2 (http rows and junk lines are not decisions)", len(all))
	}
	if !all[0].Builtin || all[0].Tool != "shell" || all[1].RuleID != "sr-2" || all[1].Builtin {
		t.Fatalf("rows = %+v", all)
	}

	builtin := appendAuditRows(nil, dataDir, "r1", "i1", "builtin", "", "")
	if len(builtin) != 1 || builtin[0].Tool != "shell" || !builtin[0].Builtin {
		t.Fatalf("builtin rows = %+v", builtin)
	}
	rule := appendAuditRows(nil, dataDir, "r1", "i1", "rule", "", "")
	if len(rule) != 1 || rule[0].Builtin || rule[0].RuleID != "sr-2" {
		t.Fatalf("rule rows = %+v", rule)
	}
	if got := appendAuditRows(nil, dataDir, "r1", "i1", "", "", "block"); len(got) != 0 {
		t.Fatalf("action filter returned %d rows on a record/review journal", len(got))
	}
	if got := appendAuditRows(nil, dataDir, "r1", "i1", "", "shell", ""); len(got) != 1 {
		t.Fatalf("tool filter returned %d rows, want 1", len(got))
	}
	if _, err := os.Stat(filepath.Join(dataDir, "runs", "missing.jsonl")); !os.IsNotExist(err) {
		t.Fatal("precondition: missing journal")
	}
	if got := appendAuditRows(nil, dataDir, "missing", "i1", "", "", ""); len(got) != 0 {
		t.Fatalf("missing journal returned %d rows", len(got))
	}
}

// The endpoint wires the rows to the project's own store and journals; an
// unknown project is a 404 and a non-GET is a 405.
func TestServeProjectAuditEndpoint(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})
	code, out := postProject(t, h, `{"name":"Open","scope":"open.example"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, out)
	}
	id, _ := out["id"].(string)

	r := httptest.NewRequest(http.MethodGet, "/api/projects/"+id+"/audit", nil)
	w := httptest.NewRecorder()
	h.serveProjectItem(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("audit = %d %s", w.Code, w.Body.String())
	}
	var view struct {
		Rows  []auditRow `json:"rows"`
		Total int        `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("audit is not JSON: %v (%s)", err, w.Body.String())
	}
	if view.Total != 0 || len(view.Rows) != 0 {
		t.Fatalf("fresh project audit = %+v", view)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/projects/none-such/audit", nil)
	w = httptest.NewRecorder()
	h.serveProjectItem(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown project audit = %d", w.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/projects/"+id+"/audit", nil)
	w = httptest.NewRecorder()
	h.serveProjectItem(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST audit = %d", w.Code)
	}
	_ = mgr
}
