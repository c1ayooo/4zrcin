// Package state stores the decider's short memory (docs/03).
//
// It is the decider's own notebook: the model may append, rewrite, delete from
// or clear it (PUT /api/state is a full overwrite). There is deliberately no
// hard size cap and no prompt-side budget either — the model decides how much
// it needs, and the round prompt gives one soft reminder about what belongs in
// the note (D106). The ledger is still the source of truth; this file is a
// reading note, not a second graph.
package state

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// template is the shape a new note starts from. Every section asks for a
// judgement or a clue the next round can act on; facts, evidence and assets
// stay in the ledger and are only cited here by id. There is deliberately no
// "已确认的事实" or "环境 / 凭据" inventory section: those turned real notes
// into a stale second copy of the ledger (D106). Existing notes are not
// migrated.
const template = `# 目标与证据进度（还缺什么）
- （无）

# 判断与结论（事实只引用 id / 证据位置，不抄正文）
- （无）

# 待验证线索（为什么值得验、缺什么）
- （无）

# 已排除（理由）
- （无）

# 活跃方向与停放（id、卡点、下一步）
- （无）

# 下一个动作
- （无）
`

// File is a state.md on disk.
//
// The mutex is not decoration: the same file is written from two goroutines —
// the decider's memory tool inside a round and PUT /api/state from the console.
// Append is a read-modify-write; without the lock one writer overwrote the
// other's paragraph and both returned success.
type File struct {
	mu   sync.Mutex
	path string
}

func New(path string) *File { return &File{path: path} }

func (f *File) Path() string { return f.path }

// Load returns the template when the file does not exist yet, so the console
// and the decider always have a shapely memory to work from.
func (f *File) Load() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return template, nil
		}
		return "", err
	}
	return string(b), nil
}

// Current reads the file as-is: a missing file is empty, not the template.
// Append uses this so a first append does not concatenate onto the template.
func (f *File) Current() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.currentLocked()
}

func (f *File) currentLocked() (string, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// Append adds text under the current memory. The model owns compaction; there
// is no hard cap, so a long notebook is the decider's choice, not a write error.
func (f *File) Append(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, err := f.currentLocked()
	if err != nil {
		return err
	}
	if cur != "" && !strings.HasSuffix(cur, "\n") {
		cur += "\n"
	}
	return f.saveLocked(cur + text)
}

// Clear empties the memory; the ledger is untouched.
func (f *File) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveLocked("")
}

// Save rewrites the file atomically. There is no size check: the decider's
// notebook is bounded by its own judgement and the prompt budget, not by a
// program constant.
func (f *File) Save(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveLocked(text)
}

func (f *File) saveLocked(text string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
