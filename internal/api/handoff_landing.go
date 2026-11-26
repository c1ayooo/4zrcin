package api

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// Handoff files (docs/15 §8 Z2): a tunnel node without a shared filesystem
// writes its declarations and notes under .zrcin; the artifact sidecars bring
// them back, and they must land at the same case-relative path the ledger's
// ingest already reads — not in the .native artifact store.
const (
	handoffFacts = ".zrcin/facts.jsonl"
	handoffNote  = ".zrcin/worker-note.md"
)

// isHandoffPath reports whether a sidecar path is one of the two handoff files.
func isHandoffPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	return rel == handoffFacts || rel == handoffNote
}

// saveHandoff lands one handoff sidecar inside the central case directory.
//
// facts.jsonl is append-only on the node, and the sidecar carries the whole
// file: it replaces the central copy only when the central copy is a prefix of
// it. A shorter or diverged file is a conflict — the operator's ledger is not
// overwritten silently, the run gets a warning instead.
func (s *Server) saveHandoff(workDir string, art *executor.Artifact) error {
	rel := filepath.ToSlash(filepath.Clean(strings.TrimSpace(art.Path)))
	dest := filepath.Join(workDir, filepath.FromSlash(rel))
	if inside, err := pathWithin(workDir, dest); err != nil || !inside {
		return fmt.Errorf("交接文件路径越界：%s", art.Path)
	}
	switch rel {
	case handoffFacts:
		existing, err := os.ReadFile(dest)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("读取中心交接文件失败：%w", err)
		}
		if len(existing) > 0 && !bytes.HasPrefix(art.Data, existing) {
			return fmt.Errorf("交接前缀冲突：节点上的 facts.jsonl 与中心已有内容不一致（中心 %d 字节，节点 %d 字节），不覆盖", len(existing), len(art.Data))
		}
		return writeHandoffFile(dest, art.Data)
	case handoffNote:
		return writeHandoffFile(dest, art.Data)
	default:
		return fmt.Errorf("不是交接文件：%s", art.Path)
	}
}

// pathWithin answers "is child inside parent".
func pathWithin(parent, child string) (bool, error) {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// writeHandoffFile replaces one handoff file atomically.
func writeHandoffFile(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".handoff-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) //nolint:errcheck
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dest)
}
