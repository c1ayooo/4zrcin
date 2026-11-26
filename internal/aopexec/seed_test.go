package aopexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The seed is the .zrcin snapshot minus the control plane's bookkeeping, under
// the artifacts limits (docs/15 §8 Z1).
func TestSeedFilesFiltersBookkeepingAndMedia(t *testing.T) {
	dir := t.TempDir()
	zrcin := filepath.Join(dir, ".zrcin")
	files := map[string]string{
		"context.json":            `{"pages":1}`,
		"briefs/in_1.md":          "brief",
		"focus.json":              "{}",
		"facts.jsonl":             "{}\n",
		"worker-note.md":          "note",
		"identity.json":           "{}",
		"auth":                    "Bearer x",
		"aop-session.json":        "{}",
		"facts.offset":            "12",
		"cursor-repair-2026.json": "{}",
		"media/record.png":        "png",
		"graph.json":              "{}",
	}
	for rel, content := range files {
		path := filepath.Join(zrcin, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed := seedFiles(dir)
	got := map[string]bool{}
	for _, f := range seed {
		got[f.Path] = true
	}
	for _, want := range []string{".zrcin/context.json", ".zrcin/briefs/in_1.md", ".zrcin/focus.json", ".zrcin/facts.jsonl", ".zrcin/worker-note.md", ".zrcin/identity.json", ".zrcin/auth", ".zrcin/graph.json"} {
		if !got[want] {
			t.Errorf("seed lacks %s", want)
		}
	}
	for _, banned := range []string{".zrcin/aop-session.json", ".zrcin/facts.offset", ".zrcin/cursor-repair-2026.json", ".zrcin/media/record.png"} {
		if got[banned] {
			t.Errorf("seed shipped control-plane bookkeeping %s", banned)
		}
	}
	if seedOrNil(false, dir) != nil {
		t.Fatal("a local turn must not collect a seed")
	}
	if got := seedFiles(t.TempDir()); got != nil {
		t.Fatalf("an empty case sent %d files", len(got))
	}
}

func TestSeedFilesRespectsLimits(t *testing.T) {
	dir := t.TempDir()
	zrcin := filepath.Join(dir, ".zrcin")
	if err := os.MkdirAll(zrcin, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < seedFilesLimit+4; i++ {
		name := filepath.Join(zrcin, "f"+strings.Repeat("0", 2)+string(rune('a'+i))+".md")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(seedFiles(dir)); got != seedFilesLimit {
		t.Fatalf("seed files = %d, want the %d-file cap", got, seedFilesLimit)
	}
}
