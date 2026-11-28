package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/rules"
)

const (
	builtinPOCYAML = "- id: builtin-poc\n  info:\n    name: Builtin POC\n    severity: high\n"
	pocYAML        = "id: mine\ninfo:\n  name: Mine\n  severity: info\nhttp:\n  - method: GET\n    path:\n      - \"{{BaseURL}}/\"\n"
)

// ruleHub is a hub whose node reported one built-in POC.
func ruleHub(t *testing.T) (*hub, string) {
	t.Helper()
	dir := t.TempDir()
	node := filepath.Join(dir, rules.NodeDir)
	if err := os.MkdirAll(node, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(node, "builtin-pocs.yaml"), []byte(builtinPOCYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return &hub{rules: rules.New(dir), cfg: hubConfig{DataDir: dir}}, dir
}

func serve(t *testing.T, handler http.HandlerFunc, method, target, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	w := httptest.NewRecorder()
	handler(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// first returns the one rule a write returned.
func first(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	rows, _ := out["rules"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rules = %v, want one", out["rules"])
	}
	row, _ := rows[0].(map[string]any)
	return row
}

func TestARuleLibraryThatIsNotConfiguredIsUnavailable(t *testing.T) {
	if code, _ := serve(t, (&hub{}).serveRules, http.MethodGet, "/api/rules", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
}

func TestServeRulesListsBothLayers(t *testing.T) {
	h, dir := ruleHub(t)
	if code, out := serve(t, h.serveRuleFile, http.MethodPost, "/api/rules/file", `{"content":`+quote(pocYAML)+`}`); code != http.StatusOK {
		t.Fatalf("write = %d %v", code, out)
	}
	code, out := serve(t, h.serveRules, http.MethodGet, "/api/rules", "")
	if code != http.StatusOK || out["matched"] != float64(2) || out["dir"] != dir {
		t.Fatalf("list = %d %v", code, out)
	}
	if code, out := serve(t, h.serveRules, http.MethodGet, "/api/rules?kind=poc&source=instance", ""); code != http.StatusOK || out["matched"] != float64(1) {
		t.Fatalf("filtered list = %d %v", code, out)
	}
	// The console's "everything" filters must not be read as a kind or a source.
	if code, out := serve(t, h.serveRules, http.MethodGet, "/api/rules?kind=all&source=all", ""); code != http.StatusOK || out["matched"] != float64(2) {
		t.Fatalf("list with the all filters = %d %v, want both rows", code, out)
	}
}

func TestRuleFileWritesRewritesAndRemoves(t *testing.T) {
	h, dir := ruleHub(t)
	code, out := serve(t, h.serveRuleFile, http.MethodPost, "/api/rules/file", `{"content":`+quote(pocYAML)+`}`)
	if code != http.StatusOK {
		t.Fatalf("write = %d %v", code, out)
	}
	row := first(t, out)
	if row["path"] != "pocs/mine.yaml" || row["revision"] == "" {
		t.Fatalf("written rule = %v", row)
	}
	revision, _ := row["revision"].(string)
	rewrite := `{"path":"pocs/mine.yaml","content":` + quote(strings.ReplaceAll(pocYAML, "Mine", "Mine v2")) + `,"revision":"stale"}`
	if code, out := serve(t, h.serveRuleFile, http.MethodPut, "/api/rules/file", rewrite); code != http.StatusConflict {
		t.Fatalf("rewrite with a stale revision = %d %v", code, out)
	}
	rewrite = `{"path":"pocs/mine.yaml","content":` + quote(strings.ReplaceAll(pocYAML, "Mine", "Mine v2")) + `,"revision":` + quote(revision) + `}`
	if code, out = serve(t, h.serveRuleFile, http.MethodPut, "/api/rules/file", rewrite); code != http.StatusOK {
		t.Fatalf("rewrite = %d %v", code, out)
	}
	row = first(t, out)
	if code, _ := serve(t, h.serveRuleFile, http.MethodDelete, "/api/rules/file", `{"path":"pocs/mine.yaml","revision":"stale"}`); code != http.StatusConflict {
		t.Fatalf("delete with a stale revision = %d", code)
	}
	if code, _ := serve(t, h.serveRuleFile, http.MethodDelete, "/api/rules/file", `{"path":"pocs/mine.yaml","revision":`+quote(row["revision"].(string))+`}`); code != http.StatusOK {
		t.Fatalf("delete = %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "pocs", "mine.yaml")); !os.IsNotExist(err) {
		t.Fatalf("the file is still there: %v", err)
	}
}

func TestRuleFileCoversABuiltInIDAndRefusesWhatIsNotARule(t *testing.T) {
	h, _ := ruleHub(t)
	code, out := serve(t, h.serveRuleFile, http.MethodPost, "/api/rules/file", `{"content":`+quote("id: builtin-poc\ninfo:\n  name: Again\n")+`}`)
	if code != http.StatusOK {
		t.Fatalf("a built-in id = %d %v", code, out)
	}
	if row := first(t, out); row["covers_builtin"] != true {
		t.Fatalf("a POC with a built-in id = %v; want it to cover the built-in", row)
	}
	if code, _ := serve(t, h.serveRuleFile, http.MethodPost, "/api/rules/file", `{"content":"just: a mapping\n"}`); code != http.StatusBadRequest {
		t.Fatalf("a mapping that is not a rule = %d", code)
	}
	if code, _ := serve(t, h.serveRuleFile, http.MethodPut, "/api/rules/file", `{"content":"id: x\n"}`); code != http.StatusBadRequest {
		t.Fatalf("a rewrite without a path = %d", code)
	}
}

func TestRuleContentReadsBothLayers(t *testing.T) {
	h, _ := ruleHub(t)
	if code, _ := serve(t, h.serveRuleFile, http.MethodPost, "/api/rules/file", `{"content":`+quote(pocYAML)+`}`); code != http.StatusOK {
		t.Fatalf("write = %d", code)
	}
	if code, out := serve(t, h.serveRuleContent, http.MethodGet, "/api/rules/content?path=pocs/mine.yaml", ""); code != http.StatusOK || out["content"] != pocYAML {
		t.Fatalf("instance content = %d %v", code, out)
	}
	if code, out := serve(t, h.serveRuleContent, http.MethodGet, "/api/rules/content?kind=poc&name=builtin-poc", ""); code != http.StatusOK || !strings.Contains(out["content"].(string), "id: builtin-poc") {
		t.Fatalf("built-in content = %d %v", code, out)
	}
	if code, _ := serve(t, h.serveRuleContent, http.MethodGet, "/api/rules/content?kind=poc&name=missing", ""); code != http.StatusNotFound {
		t.Fatalf("a missing built-in = %d, want 404", code)
	}
}

func TestImportRulesResolvesAgainstTheDataDirectory(t *testing.T) {
	h, dir := ruleHub(t)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.yaml"), []byte(pocYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := serve(t, h.importRules, http.MethodPost, "/api/rules/import", `{"path":"src/a.yaml"}`)
	if code != http.StatusOK || out["imported"] != float64(1) {
		t.Fatalf("import = %d %v", code, out)
	}
}

func TestRulesStatusSaysWhenTheNodeHasNotReported(t *testing.T) {
	h, _ := ruleHub(t)
	code, out := serve(t, h.serveRulesStatus, http.MethodGet, "/api/rules/status", "")
	if code != http.StatusOK || out["reported"] != false {
		t.Fatalf("status = %d %v", code, out)
	}
}

// quote renders a string the way a JSON body needs it.
func quote(text string) string {
	data, _ := json.Marshal(text)
	return string(data)
}
