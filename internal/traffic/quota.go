package traffic

// Project-level HTTP body quota.
//
// Two different 2 GiB budgets exist and they are not the same thing:
//
//   - the harness scan node's local traffic store (Option.TrafficOptions, YAML
//     `traffic.body_retention_bytes`, default 2 GiB). It is instance/node-wide
//     and bounds the harness's own capture directory, not anything Tianwu reads.
//   - Tianwu's per-project copy of the original Flow bytes under
//     `<CaseRoot>/.http`, written by SaveCapture. Each project owns its case
//     directory, so that copy needs its own per-project budget.
//
// ProjectHTTPQuota tracks the second one. The first scan walks the directory
// once (existing projects predate the budget); later saves only add the bytes
// they wrote, so a capture-heavy run does not rescan the tree per flow. The walk
// is lazy and mutex-guarded: a read-only console, a test server or a project
// with no traffic never pays for it.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// DefaultProjectHTTPBytes is the per-project original-HTTP-body budget (2 GiB).
// The harness's node-level budget has the same number but a different scope and
// a different owner; see the package comment above.
const DefaultProjectHTTPBytes = int64(2 << 30)

// ProjectHTTPQuota bounds one project's preserved original Flow bytes. A max of
// 0 (or any non-positive value) means unlimited.
type ProjectHTTPQuota struct {
	// mu guards used/scanned and is also held for the whole quota-aware save, so
	// two concurrent captures cannot both pass the check and then exceed the
	// budget. File writes happen under it; capture writes are already serialised
	// by the API's artifact lock, so this only adds the budget's own ordering.
	mu      sync.Mutex
	dir     string // <cases>/.http
	max     int64
	used    int64
	scanned bool
}

// NewProjectHTTPQuota returns the tracker for one project's case root.
func NewProjectHTTPQuota(cases string, max int64) *ProjectHTTPQuota {
	return &ProjectHTTPQuota{dir: filepath.Join(cases, ".http"), max: max}
}

// Max is the configured budget in bytes (0 = unlimited). It is fixed at
// construction, so it needs no lock.
func (q *ProjectHTTPQuota) Max() int64 {
	if q == nil {
		return 0
	}
	return q.max
}

// Unlimited reports whether this tracker enforces anything.
func (q *ProjectHTTPQuota) Unlimited() bool { return q == nil || q.max <= 0 }

// Used returns the bytes currently counted for `<cases>/.http`, walking the
// directory on the first call and using the in-process delta afterwards. The
// error is only returned when the walk itself cannot complete; a missing
// directory is an empty project, not an error.
func (q *ProjectHTTPQuota) Used() (int64, error) {
	if q == nil {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.usedLocked()
}

// usedLocked lazily walks the capture directory. Callers hold q.mu.
func (q *ProjectHTTPQuota) usedLocked() (int64, error) {
	if q.scanned {
		return q.used, nil
	}
	q.used = 0
	err := filepath.WalkDir(q.dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		q.used += info.Size()
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		q.used = 0
	} else if err != nil {
		return 0, err
	}
	q.scanned = true
	return q.used, nil
}

// addLocked records bytes this process wrote. Callers hold q.mu and have called
// usedLocked first, so the base value is known.
func (q *ProjectHTTPQuota) addLocked(n int64) {
	if q == nil || !q.scanned || n <= 0 {
		return
	}
	q.used += n
}

// invalidateLocked forgets the cached base after a partial write. The next Used
// walks the directory again, so a half-written capture cannot make the budget
// silently under-count. Callers hold q.mu; a no-op when nothing was scanned yet.
func (q *ProjectHTTPQuota) invalidateLocked() {
	if q == nil {
		return
	}
	q.scanned = false
}
