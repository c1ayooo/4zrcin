package aopexec

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/aop"
)

// Turn seed (docs/15 §8 Z1): a tunnel node has no shared filesystem, so the
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

// seedOrNil gates collection on the executor flag, keeping a local turn free
// of both the disk walk and the extra extension.
func seedOrNil(enabled bool, workDir string) []aop.SeedFile {
	if !enabled || strings.TrimSpace(workDir) == "" {
		return nil
	}
	return seedFiles(workDir)
}
