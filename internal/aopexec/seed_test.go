package aopexec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The seed is the .zrcin snapshot minus the control plane's bookkeeping, under
// the artifacts limits (docs/14 §8 Z1).
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
	got := map[string]bool{}
	for _, c := range seedCandidates(dir) {
		got[c.rel] = true
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
	if got := seedCandidates(t.TempDir()); got != nil {
		t.Fatalf("an empty case staged %d candidates", len(got))
	}
}

func TestSeedOrNilTruncatesToTheCap(t *testing.T) {
	dir := t.TempDir()
	zrcin := filepath.Join(dir, ".zrcin")
	if err := os.MkdirAll(zrcin, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < seedFilesLimit+4; i++ {
		name := filepath.Join(zrcin, fmt.Sprintf("f%03d.md", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(seedOrNil(true, dir)); got != seedFilesLimit {
		t.Fatalf("seed files = %d, want the %d-file cap", got, seedFilesLimit)
	}
}

// A tunnel node has no <data>/skills: the explicitly selected skills ride the
// seed under .zrcin/skills, and the prompt points at the seeded copy instead of
// this machine's path (docs/14 §8).
func TestSeedCarriesSelectedSkills(t *testing.T) {
	skillsDir := t.TempDir()
	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alpha := filepath.Join(skillsDir, "alpha")
	write(filepath.Join(alpha, "SKILL.md"), "# alpha\n")
	write(filepath.Join(alpha, "assets", "example.txt"), "asset\n")
	beta := filepath.Join(skillsDir, "beta")
	write(filepath.Join(beta, "SKILL.md"), "# beta\n")

	caseDir := t.TempDir()
	write(filepath.Join(caseDir, ".zrcin", "focus.json"), "{}")

	seed := seedOrNil(true, caseDir, filepath.Join(alpha, "SKILL.md"), filepath.Join(beta, "SKILL.md"))
	got := map[string]bool{}
	for _, f := range seed {
		got[f.Path] = true
	}
	for _, want := range []string{
		".zrcin/focus.json",
		".zrcin/skills/alpha/SKILL.md",
		".zrcin/skills/alpha/assets/example.txt",
		".zrcin/skills/beta/SKILL.md",
	} {
		if !got[want] {
			t.Errorf("seed lacks %s (got %v)", want, got)
		}
	}
	// The same file is not shipped twice when the skill is selected twice.
	if n := len(seed); n != 4 {
		t.Errorf("seed has %d files, want 4", n)
	}
	// A local turn still sends nothing: it reads the case directory directly.
	if seedOrNil(false, caseDir, filepath.Join(alpha, "SKILL.md")) != nil {
		t.Fatal("a local turn must not collect a seed")
	}
}

func TestSkillPromptPointsAtTheSeedOnRemoteNodes(t *testing.T) {
	skillsDir := t.TempDir()
	skill := filepath.Join(skillsDir, "alpha", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	local, err := skillPrompt([]string{skill}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(local, skill) || strings.Contains(local, ".zrcin/skills") {
		t.Fatalf("a local turn must reference the real path: %q", local)
	}

	remote, err := skillPrompt([]string{skill}, "/srv/case")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/srv/case", ".zrcin", "skills", "alpha", "SKILL.md")
	if !strings.Contains(remote, want) {
		t.Fatalf("remote prompt = %q, want it to name %s", remote, want)
	}
	if strings.Contains(remote, skill) {
		t.Fatalf("remote prompt leaks the control-plane path: %q", remote)
	}
}

// A real case carries dozens of knowledge-base entries on top of the run
// inputs; the seed cap must swallow that whole, and under pressure the kb
// copies truncate first -- never the selected skills (docs/14 §8).
func TestSeedCapCarriesKBAndSkillsSurvivePressure(t *testing.T) {
	caseDir := t.TempDir()
	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 40; i++ {
		write(filepath.Join(caseDir, ".zrcin", "kb", "entries", fmt.Sprintf("kb%03d.json", i)), "{}")
	}
	write(filepath.Join(caseDir, ".zrcin", "run-input.json"), `{"state":"ready"}`)
	write(filepath.Join(caseDir, ".zrcin", "inputs", "r_1", ".zrcin", "focus.json"), "{}")

	skill := filepath.Join(t.TempDir(), "alpha", "SKILL.md")
	write(skill, "# alpha\n")

	seed := seedOrNil(true, caseDir, skill)
	got := map[string]bool{}
	for _, f := range seed {
		got[f.Path] = true
	}
	if len(got) != 43 {
		t.Errorf("seed has %d files, want 43 (40 kb + inputs + run-input + skill)", len(got))
	}
	for _, want := range []string{
		".zrcin/kb/entries/kb040.json",
		".zrcin/inputs/r_1/.zrcin/focus.json",
		".zrcin/run-input.json",
		".zrcin/skills/alpha/SKILL.md",
	} {
		if !got[want] {
			t.Errorf("seed lacks %s", want)
		}
	}

	// Under cap pressure the operating context and the skills keep their slots
	// and the knowledge-base copies are the ones that give way: with room for
	// eight files the seed carries the skill, the run inputs and the manifest,
	// then only as many kb entries as fit.
	old := seedFilesLimit
	seedFilesLimit = 8
	defer func() { seedFilesLimit = old }()
	seed = seedOrNil(true, caseDir, skill)
	got = map[string]bool{}
	kb := 0
	for _, f := range seed {
		got[f.Path] = true
		if isKBCopy(f.Path) {
			kb++
		}
	}
	for _, want := range []string{
		".zrcin/skills/alpha/SKILL.md",
		".zrcin/inputs/r_1/.zrcin/focus.json",
		".zrcin/run-input.json",
	} {
		if !got[want] {
			t.Errorf("core file %s gave way under pressure: %v", want, got)
		}
	}
	if kb > 5 {
		t.Errorf("kb copies took %d of 8 slots, want at most 5", kb)
	}
}
