package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (l *Library) DeletedRevisions() map[string]string {
	out := map[string]string{}
	for _, e := range l.snapshotEntries() {
		if e.Deleted {
			out[e.ID] = Revision(e)
		}
	}
	return out
}

// Delete hides an entry in this instance with a local tombstone. The binary's
// bundle stays immutable and no repository asset is touched, so the same
// knowledge is still served by every other instance and after a rebuild.
func (l *Library) Delete(id, revision string) error {
	if !validID(id) {
		return fmt.Errorf("无效知识 ID")
	}
	l.editMu.Lock()
	defer l.editMu.Unlock()
	current, ok := findEntry(l.snapshotEntries(), id)
	if !ok || current.Deleted {
		return os.ErrNotExist
	}
	if revision != Revision(current) {
		return ErrConflict
	}
	disk, err := open(filepath.Dir(l.dir), true)
	if err != nil {
		return err
	}
	stored, ok := findEntry(disk.entries, id)
	if !ok || Revision(stored) != revision {
		return ErrConflict
	}
	target, err := l.entryPath(id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tombstone := Entry{ID: id, Deleted: true, UpdatedAt: &now, origin: OriginInstance, covers: l.hasBuiltin(id)}
	if err := writeJSONAtomic(target, tombstone); err != nil {
		return err
	}
	l.installEntry(tombstone)
	return nil
}

func (l *Library) DeleteCandidate(id string, version int) error {
	l.editMu.Lock()
	defer l.editMu.Unlock()
	c, err := l.readCandidate(id)
	if err != nil {
		return err
	}
	if c.Version != version {
		return ErrConflict
	}
	path, err := l.candidatePath(id)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
