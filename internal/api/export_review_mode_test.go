package api

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// An export has to say how the instance stood, because project data cannot: a
// project is premise_policy=verified_only while the instance may run no verifier
// at all (--no-verify, or the operator's runtime switch). The archive records
// both the boot value and the switch as it stood at export time — and the
// project policy itself is never rewritten to match.
func TestExportRecordsRuntimeReviewStateAndKeepsProjectPolicy(t *testing.T) {
	s, p := newBindingServer(t)
	policyPath := filepath.Join(p.Dir, "project.json")
	before, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("project manifest: %v", err)
	}
	if policyOf(t, before) != "verified_only" {
		t.Fatalf("fixture policy = %q, want verified_only", policyOf(t, before))
	}

	for _, tc := range []struct {
		name        string
		disabled    bool
		bootDisable bool
	}{
		{"复核关闭（--no-verify 启动）", true, true},
		{"复核开启", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.SetReviewsDisabled(tc.disabled)
			s.SetReviewBootDisabled(tc.bootDisable)

			rec := httptest.NewRecorder()
			s.exportBundle(rec, httptest.NewRequest(http.MethodPost, "/api/bundles/export", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}

			var manifest struct {
				Runtime *struct {
					ReviewEnabled      bool `json:"review_enabled"`
					ReviewBootDisabled bool `json:"review_boot_disabled"`
				} `json:"runtime"`
			}
			if err := json.Unmarshal(zipEntry(t, resp.Path, "manifest.json"), &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Runtime == nil {
				t.Fatalf("archive manifest has no runtime facts")
			}
			if got := !tc.disabled; manifest.Runtime.ReviewEnabled != got {
				t.Fatalf("review_enabled = %v, want %v", manifest.Runtime.ReviewEnabled, got)
			}
			if manifest.Runtime.ReviewBootDisabled != tc.bootDisable {
				t.Fatalf("review_boot_disabled = %v, want %v", manifest.Runtime.ReviewBootDisabled, tc.bootDisable)
			}

			// The policy axis stays untouched in both states.
			after, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := policyOf(t, after); got != "verified_only" {
				t.Fatalf("premise_policy after export = %q, want verified_only", got)
			}
		})
	}
}

func policyOf(t *testing.T, raw []byte) string {
	t.Helper()
	var doc struct {
		PremisePolicy string `json:"premise_policy"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("project manifest is not json: %v", err)
	}
	return doc.PremisePolicy
}

func zipEntry(t *testing.T, zipPath, name string) []byte {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		buf := make([]byte, f.UncompressedSize64)
		n, err := rc.Read(buf)
		if err != nil && n == 0 {
			t.Fatal(err)
		}
		return buf[:n]
	}
	t.Fatalf("archive has no %s", name)
	return nil
}

// The boot value must survive the operator opening reviews at runtime: an
// export then says "started with --no-verify, reviews open when written", which
// is exactly the pair a reader cannot reconstruct from project data. (Probe
// proposed by 排障; without it a mutation that clears the boot value at runtime
// would go unnoticed because every other case sets both switches together.)
func TestExportKeepsBootValueWhenReviewsReopenAtRuntime(t *testing.T) {
	s, _ := newBindingServer(t)
	s.SetReviewBootDisabled(true)
	s.SetReviewsDisabled(true)
	s.SetReviewsDisabled(false) // operator opens reviews; boot state is not rewritten

	enabled, bootDisabled := exportRuntimeFacts(t, s)
	if !enabled {
		t.Fatalf("review_enabled = false, want true after the operator opened reviews")
	}
	if !bootDisabled {
		t.Fatalf("review_boot_disabled = false, want the --no-verify boot value kept")
	}
}

func exportRuntimeFacts(t *testing.T, s *Server) (enabled, bootDisabled bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.exportBundle(rec, httptest.NewRequest(http.MethodPost, "/api/bundles/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Runtime *struct {
			ReviewEnabled      bool `json:"review_enabled"`
			ReviewBootDisabled bool `json:"review_boot_disabled"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(zipEntry(t, resp.Path, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Runtime == nil {
		t.Fatalf("archive manifest has no runtime facts")
	}
	return manifest.Runtime.ReviewEnabled, manifest.Runtime.ReviewBootDisabled
}
