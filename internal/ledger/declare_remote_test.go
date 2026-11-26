package ledger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/aop/tool"
	"github.com/c1ayooo/4zrcin/internal/artifact"
)

// A tunnel node's declared evidence exists only in the artifact store (keyed
// <run>/<case-relative path>): the finish settle must accept it as the fact's
// evidence even though no such file exists on this machine (docs/14 §8 Z3).
func TestFinalSettleAcceptsArtifactBackedEvidence(t *testing.T) {
	casesRoot := t.TempDir()
	workDir := filepath.Join(casesRoot, "in_test")
	if err := os.MkdirAll(filepath.Join(workDir, ".zrcin"), 0o755); err != nil {
		t.Fatal(err)
	}
	runID := "r_remote"
	decl := `{"kind":"note","title":"远程证据","summary":"s","evidence":["proof.txt"],"confidence":"observed","proof":"dynamic"}` + "\n"
	if err := os.WriteFile(filepath.Join(workDir, ".zrcin", "facts.jsonl"), []byte(decl), 0o600); err != nil {
		t.Fatal(err)
	}
	// The node shipped the bytes; no such file exists under workDir.
	if _, err := artifact.Save(casesRoot, runID, "in_test", "evt-1", &tool.Artifact{
		ResultId: "proof.txt", Tool: "remote", Kind: "evidence", Target: "proof.txt",
		Data: []byte("SEED-OK bytes"),
	}); err != nil {
		t.Fatal(err)
	}

	declared, err := ReadDeclaredFinal(casesRoot, workDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(declared.Facts) != 1 {
		t.Fatalf("facts = %d", len(declared.Facts))
	}
	df := declared.Facts[0]
	if len(df.Evidence) != 1 || df.Evidence[0] != "proof.txt" {
		t.Fatalf("evidence = %v, want proof.txt settled from the artifact store", df.Evidence)
	}
	for _, w := range declared.Warnings {
		if len(w) > 0 {
			t.Errorf("settle warned: %s", w)
		}
	}

	// Without a run the same handoff keeps the path as a reference only: a
	// repair or a preview has no run to ask for the bytes.
	plain, err := ReadDeclared(casesRoot, workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Facts) != 1 || len(plain.Facts[0].Evidence) != 0 {
		t.Fatalf("run-less settle must not claim evidence: %+v", plain.Facts)
	}
	if len(plain.Warnings) == 0 {
		t.Fatal("the run-less settle must say the bytes are missing")
	}
}
