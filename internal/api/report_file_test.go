package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A report directory contains the report's markdown plus copies of evidence the
// model chose to include. Those copies are attacker-influenced (a captured page
// can contain script) and are served from the console's own origin, so the
// endpoint must never let the browser treat them as an unsandboxed document.
func TestReportFileSandboxesBrowserDocuments(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reports", "rep_1")
	for rel, body := range map[string]string{
		"index.md":              "# 报告\n\n- 结论\n",
		"evidence/capture.html": "<script>alert(document.cookie)</script>",
		"evidence/icon.svg":     "<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>",
		"evidence/raw.txt":      "plain text",
	} {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(nil, nil, t.TempDir(), root)
	serve := func(rel string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/reports/rep_1/file", nil)
		req.SetPathValue("id", "rep_1")
		if rel != "" {
			q := req.URL.Query()
			q.Set("path", rel)
			req.URL.RawQuery = q.Encode()
		}
		rec := httptest.NewRecorder()
		s.serveReportFile(rec, req)
		return rec
	}

	for _, test := range []struct {
		path string
		want string
	}{
		{"evidence/capture.html", "text/html"},
		{"evidence/icon.svg", "image/svg+xml"},
	} {
		res := serve(test.path)
		if res.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", test.path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Content-Type"); got != test.want {
			t.Errorf("%s Content-Type = %q, want %q", test.path, got, test.want)
		}
		if got := res.Header().Get("Content-Security-Policy"); got != "sandbox" {
			t.Errorf("%s CSP = %q, want sandbox (the bytes are attacker-influenced)", test.path, got)
		}
		if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s nosniff = %q", test.path, got)
		}
	}

	// The report itself stays readable as markdown, and the default path is
	// still index.md (the console opens it without a query).
	for _, rel := range []string{"index.md", ""} {
		res := serve(rel)
		if res.Code != http.StatusOK {
			t.Fatalf("markdown %q = %d %s", rel, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
			t.Errorf("markdown %q = %q", rel, got)
		}
	}

	// A plain-text artifact has no document type; ServeFile may label it, but
	// it is never sniffed into an executable document.
	res := serve("evidence/raw.txt")
	if res.Code != http.StatusOK {
		t.Fatalf("text = %d", res.Code)
	}
	if got := res.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("plain text unexpectedly sandboxed: %q", got)
	}
	if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("plain text nosniff = %q", got)
	}
}
