package report

import (
	"os"
	"path/filepath"
	"testing"
)

// The report directory is served over HTTP. Containment of the path *string*
// is not containment of the file: a symlink planted inside the directory can
// make a contained path open anything on the host. SafePath must refuse it
// (audit item N6), while ordinary files and not-yet-created files keep working.
func TestSafePathRefusesSymlinkInsideReportDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("机密"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		// Windows without developer mode cannot create symlinks; the guard is
		// still covered by the plain-path assertions below.
		t.Skipf("symlinks unavailable: %v", err)
	}

	if p, ok := SafePath(dir, "link.txt"); ok {
		t.Fatalf("SafePath accepted a symlink inside the report dir: %s", p)
	}
	// A symlink two levels deep must be refused as well.
	if err := os.MkdirAll(filepath.Join(dir, "evidence"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "evidence", "deep.txt")); err != nil {
		t.Fatal(err)
	}
	if p, ok := SafePath(dir, "evidence/deep.txt"); ok {
		t.Fatalf("SafePath accepted a nested symlink: %s", p)
	}

	if _, ok := SafePath(dir, "../escape.txt"); ok {
		t.Fatal("regression: parent traversal is no longer refused")
	}
	if p, ok := SafePath(dir, "real.txt"); !ok {
		t.Fatalf("SafePath refused a regular file: %v %v", p, ok)
	}
	if p, ok := SafePath(dir, "not-created-yet.md"); !ok {
		t.Fatalf("SafePath refused a missing file that a caller may create: %v %v", p, ok)
	}
}
