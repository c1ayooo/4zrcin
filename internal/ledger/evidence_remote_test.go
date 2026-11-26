package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/aop/tool"
	"github.com/c1ayooo/4zrcin/internal/artifact"
)

// Remote evidence settlement (docs/15 §8 Z3): the file is not on this
// machine's case directory, but the patch-23c artifact store holds it under
// the same case-relative path; settlement must admit those bytes as the
// original, with the same digest-addressed evidence identity.
func TestPreserveEvidenceFallsBackToTheArtifactStore(t *testing.T) {
	caseRoot := t.TempDir()
	workDir := t.TempDir()
	const rel = "raw/page.html"
	body := []byte("<html>proof</html>")
	if _, err := artifact.Save(caseRoot, "run_1", "intent_1", "ev_1", &tool.Artifact{
		Tool: "remote", Kind: "evidence", Target: rel, ResultId: rel, MediaType: "text/html", Data: body,
	}); err != nil {
		t.Fatalf("artifact.Save: %v", err)
	}

	preserved, err := preserveEvidence(caseRoot, workDir, "run_1", rel)
	if err != nil {
		t.Fatalf("preserveEvidence: %v", err)
	}
	sum := sha256.Sum256(body)
	if preserved.Digest != hex.EncodeToString(sum[:]) || preserved.Size != int64(len(body)) {
		t.Fatalf("preserved = %+v", preserved)
	}
	raw, err := os.ReadFile(filepath.Join(caseRoot, preserved.URI))
	if err != nil || string(raw) != string(body) {
		t.Fatalf("preserved object = %q %v", raw, err)
	}

	// Absent from both places stays the honest failure.
	if _, err := preserveEvidence(caseRoot, workDir, "run_1", "raw/missing.bin"); err == nil {
		t.Fatal("missing evidence admitted")
	}
}
