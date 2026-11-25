package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/store"
)

// The scope/constraints documents have two copies: the ledger revision (the
// audited truth) and the project manifest (the copy briefs read). A save that
// reports success must leave both equal, and a save that fails must leave both
// as they were. The save writes the manifest first and rolls it back when the
// ledger refuses; these tests drive every branch through PersistDoc, the same
// seam the hub supplies in production.

type persistRecorder struct {
	calls []persistCall
	fail  func(kind, text string, callIndex int) error
}

type persistCall struct {
	kind, text string
}

func (r *persistRecorder) persist(kind, text string) error {
	i := len(r.calls)
	r.calls = append(r.calls, persistCall{kind: kind, text: text})
	if r.fail != nil {
		return r.fail(kind, text, i)
	}
	return nil
}

// A manifest write failure commits nothing: the ledger keeps the old revision
// and the response is a 500, not a 201-ish "saved".
func TestDocumentSaveManifestFailureLeavesLedgerUntouched(t *testing.T) {
	s, p := newBindingServer(t)
	s.SetProjectSettings("10.0.0.0/24", "不要删除或修改真实数据。")
	s.PersistDoc = func(kind, text string) error { return errors.New("注入：manifest 写入失败") }

	rec := call(t, s, http.MethodPut, "/api/documents/scope", map[string]any{
		"text": "10.0.0.1/32", "revision": 0,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("save with failing manifest write = %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := p.Store.Document(store.DocumentScope); ok {
		t.Fatal("ledger gained a document revision although the manifest write failed")
	}
	if s.scopeText() != "10.0.0.0/24" {
		t.Fatalf("prompt snapshot moved to %q although nothing was committed", s.scopeText())
	}
}

// A ledger refusal rolls the manifest back: the caller is told "conflict" and
// both copies still hold the old text.
func TestDocumentSaveLedgerConflictRestoresManifest(t *testing.T) {
	s, p := newBindingServer(t)
	s.SetProjectSettings("10.0.0.0/24", "不要删除或修改真实数据。")
	rec := &persistRecorder{}
	s.PersistDoc = rec.persist

	first := call(t, s, http.MethodPut, "/api/documents/scope", map[string]any{
		"text": "10.0.0.0/24, 192.168.1.0/24", "revision": 0,
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first save = %d %s", first.Code, first.Body.String())
	}

	rec.calls = nil
	stale := call(t, s, http.MethodPut, "/api/documents/scope", map[string]any{
		"text": "stale-scope", "revision": 0,
	})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"code":"document_conflict"`) {
		t.Fatalf("stale save = %d %s", stale.Code, stale.Body.String())
	}
	// Exactly two manifest writes: the (rolled-back) new text and the restore.
	if len(rec.calls) != 2 || rec.calls[0].text != "stale-scope" {
		t.Fatalf("manifest writes = %+v", rec.calls)
	}
	if rec.calls[1].text != "10.0.0.0/24, 192.168.1.0/24" {
		t.Fatalf("manifest restore wrote %q; want the old text", rec.calls[1].text)
	}
	d, ok := p.Store.Document(store.DocumentScope)
	if !ok || d.Text != "10.0.0.0/24, 192.168.1.0/24" || d.Revision != 1 {
		t.Fatalf("ledger after rolled-back save = %+v (found %v)", d, ok)
	}
	if s.scopeText() != "10.0.0.0/24, 192.168.1.0/24" {
		t.Fatalf("prompt snapshot = %q; want the committed text", s.scopeText())
	}
}

// If the restore itself fails, the two copies have diverged. The response must
// name the divergence instead of reporting a clean conflict, and the ledger —
// which refused the write — stays the audited truth to rebuild from.
func TestDocumentSaveRestoreFailureReportsDivergence(t *testing.T) {
	s, p := newBindingServer(t)
	s.SetProjectSettings("10.0.0.0/24", "不要删除或修改真实数据。")
	rec := &persistRecorder{}
	s.PersistDoc = rec.persist

	first := call(t, s, http.MethodPut, "/api/documents/scope", map[string]any{
		"text": "10.0.0.0/24, 192.168.1.0/24", "revision": 0,
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first save = %d %s", first.Code, first.Body.String())
	}
	committed := s.scopeText()
	// Fail exactly the restore call: it is the write that carries the old text.
	rec.fail = func(kind, text string, i int) error {
		if i > 0 && text == committed {
			return errors.New("注入：manifest 恢复失败")
		}
		return nil
	}

	diverged := call(t, s, http.MethodPut, "/api/documents/scope", map[string]any{
		"text": "stale-scope", "revision": 0,
	})
	if diverged.Code != http.StatusConflict || !strings.Contains(diverged.Body.String(), `"code":"document_state_diverged"`) {
		t.Fatalf("diverged save = %d %s; calls=%+v", diverged.Code, diverged.Body.String(), rec.calls)
	}
	// The ledger refused the write, so it is unchanged and remains the truth a
	// zrcin-repair --manifest run rebuilds from.
	d, ok := p.Store.Document(store.DocumentScope)
	if !ok || d.Text != committed || d.Revision != 1 {
		t.Fatalf("ledger changed during a refused save: %+v", d)
	}
}

// The happy path: one manifest write, one ledger revision, the prompt snapshot
// follows, and the response reports the new revision.
func TestDocumentSaveHappyPathWritesManifestOnce(t *testing.T) {
	s, p := newBindingServer(t)
	s.SetProjectSettings("10.0.0.0/24", "不要删除或修改真实数据。")
	rec := &persistRecorder{}
	s.PersistDoc = rec.persist

	out := call(t, s, http.MethodPut, "/api/documents/scope", map[string]any{
		"text": "10.0.0.0/24, 192.168.1.0/24", "revision": 0,
	})
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"revision":1`) {
		t.Fatalf("save = %d %s", out.Code, out.Body.String())
	}
	if len(rec.calls) != 1 || rec.calls[0].text != "10.0.0.0/24, 192.168.1.0/24" {
		t.Fatalf("manifest writes = %+v; want exactly one save with the new text", rec.calls)
	}
	d, ok := p.Store.Document(store.DocumentScope)
	if !ok || d.Revision != 1 || d.Text != rec.calls[0].text {
		t.Fatalf("ledger and manifest diverged on a successful save: %+v vs %+v", d, rec.calls)
	}
	if s.scopeText() != "10.0.0.0/24, 192.168.1.0/24" {
		t.Fatalf("prompt snapshot = %q", s.scopeText())
	}
}
