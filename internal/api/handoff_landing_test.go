package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// Handoff landing (docs/14 §8 Z2): the sidecar brings the whole node-side file
// and it lands at the path the ledger already reads.
func TestSaveHandoffFactsAppendAndConflict(t *testing.T) {
	workDir := t.TempDir()
	s := &Server{}
	first := &executor.Artifact{Path: ".zrcin/facts.jsonl", Data: []byte("{\"a\":1}\n")}
	if err := s.saveHandoff(workDir, first); err != nil {
		t.Fatalf("first facts landing: %v", err)
	}
	second := &executor.Artifact{Path: ".zrcin/facts.jsonl", Data: []byte("{\"a\":1}\n{\"b\":2}\n")}
	if err := s.saveHandoff(workDir, second); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(workDir, ".zrcin", "facts.jsonl"))
	if err != nil || string(raw) != string(second.Data) {
		t.Fatalf("central facts = %q %v", raw, err)
	}
	// A diverged file (not a prefix extension) must not overwrite the ledger's
	// copy: the operator gets the warning instead.
	diverged := &executor.Artifact{Path: ".zrcin/facts.jsonl", Data: []byte("{\"other\":1}\n")}
	if err := s.saveHandoff(workDir, diverged); err == nil {
		t.Fatal("a diverged facts file overwrote the central copy")
	}

	note := &executor.Artifact{Path: ".zrcin/worker-note.md", Data: []byte("v1")}
	if err := s.saveHandoff(workDir, note); err != nil {
		t.Fatalf("note: %v", err)
	}
	note.Data = []byte("v2")
	if err := s.saveHandoff(workDir, note); err != nil {
		t.Fatalf("note overwrite: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(workDir, ".zrcin", "worker-note.md"))
	if string(raw) != "v2" {
		t.Fatalf("note = %q, want the later write", raw)
	}

	// Only the two handoff paths are accepted, and nothing may escape the case.
	if err := s.saveHandoff(workDir, &executor.Artifact{Path: ".zrcin/auth", Data: []byte("x")}); err == nil {
		t.Fatal("a non-handoff path landed through the handoff writer")
	}
	if err := s.saveHandoff(workDir, &executor.Artifact{Path: "../escape/facts.jsonl", Data: []byte("x")}); err == nil {
		t.Fatal("a traversal path landed")
	}
	if !isHandoffPath(".zrcin/facts.jsonl") || !isHandoffPath(".zrcin/worker-note.md") || isHandoffPath("raw/x") {
		t.Fatal("isHandoffPath misclassifies")
	}
}
