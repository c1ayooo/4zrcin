package aopexec

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/aop"
)

// Turn seed (docs/14 §8 Z1): a tunnel node has no shared filesystem, so the
// control plane sends the case's input files with every turn. The list is the
// workspaces's .zrcin snapshot minus the control plane's own bookkeeping, under
// the same limits patch 23c uses for artifacts.
const (
	seedFileLimit  = 8 << 20
	seedFilesLimit = 16
	seedTotalLimit = 64 << 20
)

// seedFiles collects what the node must read for this turn, or nil when there
// is nothing (an empty case sends no extension at all).
func seedFiles(workDir string) []aop.SeedFile {
	root := filepath.Join(workDir, ".zrcin")
	var paths []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree is skipped, not a failed turn
		}
		rel, relErr := filepath.Rel(workDir, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipSeedPath(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if skipSeedPath(rel, false) {
			return nil
		}
		if info, ierr := d.Info(); ierr != nil || !info.Mode().IsRegular() {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	sort.Strings(paths)
	out := make([]aop.SeedFile, 0, len(paths))
	total := 0
	for _, rel := range paths {
		if len(out) >= seedFilesLimit || total >= seedTotalLimit {
			break
		}
		abs := filepath.Join(workDir, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() || info.Size() > seedFileLimit {
			continue
		}
		if int(info.Size())+total > seedTotalLimit {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		out = append(out, aop.SeedFile{Path: rel, Mode: uint32(info.Mode().Perm()), Data: data})
		total += len(data)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// skipSeedPath keeps the control plane's own bookkeeping off the wire: the
// session pointer, read offsets, repair cursors and the media cache belong to
// the platform, not to the model's workspace. Everything else in .zrcin (the
// context pages, focus files, briefs, graph view, identity/auth fallbacks, the
// prior handoff) travels.
func skipSeedPath(rel string, isDir bool) bool {
	base := filepath.Base(rel)
	if isDir {
		return base == "media"
	}
	if base == "aop-session.json" || strings.HasSuffix(base, ".offset") {
		return true
	}
	return strings.HasPrefix(base, "cursor-repair-") && strings.HasSuffix(base, ".json")
}

// skillSeedRoot is where the explicitly selected skills are mirrored inside
// the seed. A tunnel node has no <data>/skills, and the prompt points at this
// path, so the file the model is told to read exists once the seed lands.
const skillSeedRoot = ".zrcin/skills"

// seedOrNil gates collection on the executor flag, keeping a local turn free
// of both the disk walk and the extra extension. For a tunnel node it is the
// whole turn seed: the case's .zrcin snapshot plus the explicitly selected
// skills, under one shared set of artifact limits.
func seedOrNil(enabled bool, workDir string, skills ...string) []aop.SeedFile {
	if !enabled || strings.TrimSpace(workDir) == "" {
		return nil
	}
	files := seedFiles(workDir)
	used, count := 0, len(files)
	for _, f := range files {
		used += len(f.Data)
	}
	files = append(files, skillSeedFiles(skills, seedFilesLimit-count, seedTotalLimit-used)...)
	if len(files) == 0 {
		return nil
	}
	return files
}

// skillSeedFiles mirrors the directories of the selected skill files into the
// seed. Assets travel with their SKILL.md because the prompt tells the model to
// resolve them relative to the file's directory; without them the reference
// would be a path that exists only on the control plane.
func skillSeedFiles(skills []string, maxFiles, maxBytes int) []aop.SeedFile {
	if maxFiles <= 0 || maxBytes <= 0 {
		return nil
	}
	var out []aop.SeedFile
	seen := map[string]bool{}
	count, total := 0, 0
	for _, skill := range skills {
		skill = strings.TrimSpace(skill)
		if skill == "" {
			continue
		}
		dir := filepath.Dir(skill)
		name := filepath.Base(dir)
		if name == "" || name == "." || name == string(filepath.Separator) {
			continue
		}
		root := path.Join(skillSeedRoot, name)
		if seen[root] {
			continue
		}
		seen[root] = true
		tree := seedTree(dir, root, maxFiles-count, maxBytes-total)
		for _, f := range tree {
			count++
			total += len(f.Data)
		}
		out = append(out, tree...)
		if count >= maxFiles || total >= maxBytes {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// seedTree copies one directory's regular files under relRoot, within the
// remaining file-count and byte budgets.
func seedTree(dir, relRoot string, maxFiles, maxBytes int) []aop.SeedFile {
	if maxFiles <= 0 || maxBytes <= 0 {
		return nil
	}
	var out []aop.SeedFile
	count, total := 0, 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !info.Mode().IsRegular() || info.Size() > seedFileLimit {
			return nil
		}
		if count >= maxFiles || total >= maxBytes {
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		data, derr := os.ReadFile(p)
		if derr != nil {
			return nil
		}
		if len(data)+total > maxBytes {
			return filepath.SkipAll
		}
		out = append(out, aop.SeedFile{
			Path: path.Join(relRoot, filepath.ToSlash(rel)),
			Mode: uint32(info.Mode().Perm()),
			Data: data,
		})
		count++
		total += len(data)
		return nil
	})
	return out
}

// skillSeedPath is where a selected skill file lives on the node after the
// seed lands: the same work directory, under .zrcin/skills/<dir>/<file>.
func skillSeedPath(workDir, skill string) string {
	name := filepath.Base(filepath.Dir(skill))
	return filepath.Join(workDir, ".zrcin", "skills", name, filepath.Base(skill))
}
