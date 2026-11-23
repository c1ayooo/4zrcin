package bundle

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// An archive must be able to say how the instance was running when it was
// written: project data only carries premise_policy, and a project can be
// verified_only while the instance runs no verifier (the 2026-11-22 run).
func TestManifestRecordsRuntimeReviewState(t *testing.T) {
	caseRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(caseRoot, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "case.zip")
	m, err := Export(caseRoot, "", zipPath, RuntimeFacts{ReviewEnabled: false, ReviewBootDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Runtime == nil || m.Runtime.ReviewEnabled || !m.Runtime.ReviewBootDisabled {
		t.Fatalf("manifest runtime = %+v, want review_enabled=false boot_disabled=true", m.Runtime)
	}

	raw := readArchiveManifest(t, zipPath)
	var decoded Manifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Runtime == nil {
		t.Fatalf("archive manifest lost the runtime facts: %s", raw)
	}
	if decoded.Runtime.ReviewEnabled {
		t.Fatalf("review_enabled = true, want false: %s", raw)
	}
	if !decoded.Runtime.ReviewBootDisabled {
		t.Fatalf("review_boot_disabled = false, want true: %s", raw)
	}
}

// Older callers (and archives written before this field existed) must read as
// unknown, not as "reviews ran": a missing key is the only honest encoding.
func TestManifestWithoutRuntimeFactsStaysUnknown(t *testing.T) {
	caseRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(caseRoot, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "case.zip")
	if _, err := Export(caseRoot, "", zipPath); err != nil {
		t.Fatal(err)
	}
	raw := readArchiveManifest(t, zipPath)
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["runtime"]; ok {
		t.Fatalf("archive without runtime facts must omit the key, got %s", raw)
	}
}

func readArchiveManifest(t *testing.T, zipPath string) []byte {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != ManifestFile {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		buf := make([]byte, f.UncompressedSize64)
		if _, err := rc.Read(buf); err != nil && err.Error() != "EOF" {
			t.Fatal(err)
		}
		return buf
	}
	t.Fatalf("archive has no %s", ManifestFile)
	return nil
}
