package kb

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDeletingEmbeddedKnowledgePersistsAndDoesNotReturnOnPublication(t *testing.T) {
	l := publishLibrary(t)
	e := l.Entries()[0]
	if err := l.Delete(e.ID, "outdated"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := l.Delete(e.ID, Revision(e)); err != nil {
		t.Fatal(err)
	}
	if _, ok := findEntry(l.Entries(), e.ID); ok {
		t.Fatal("deleted entry still searchable")
	}
	c, _ := l.SaveCandidate(nomination("world-unrelated-after-delete"))
	if _, err := l.Publish(c.ID, c.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok := findEntry(l.Entries(), e.ID); ok {
		t.Fatal("unrelated publication restored deleted knowledge")
	}
	root := filepath.Dir(l.Dir())
	if _, err := Ensure(root); err != nil {
		t.Fatal(err)
	}
	// Simulate an upgraded binary materialising this embedded entry again.
	if err := writeJSONAtomic(filepath.Join(root, BuiltinDir, e.ID+".json"), e); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findEntry(reopened.Entries(), e.ID); ok {
		t.Fatal("restart restored embedded knowledge")
	}
	if err := reopened.Reload(); err != nil {
		t.Fatal(err)
	}
	if reopened.DeletedRevisions()[e.ID] == "" {
		t.Fatal("deletion lost")
	}
	readd := nomination(e.ID)
	if _, err := reopened.SaveCandidate(readd); !errors.Is(err, ErrConflict) {
		t.Fatal("unaware candidate bypassed deletion", err)
	}
	readd.BaseRevision = reopened.DeletedRevisions()[e.ID]
	draft, err := reopened.SaveCandidate(readd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Publish(draft.ID, draft.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok := findEntry(reopened.Entries(), e.ID); !ok {
		t.Fatal("explicit re-publication failed")
	}
}

func TestDeletingCandidateDoesNotRemoveItsPublishedKnowledge(t *testing.T) {
	l := publishLibrary(t)
	c, _ := l.SaveCandidate(nomination("world-kept"))
	if _, err := l.Publish(c.ID, c.Version); err != nil {
		t.Fatal(err)
	}
	if err := l.DeleteCandidate(c.ID, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("stale candidate deletion accepted")
	}
	if err := l.DeleteCandidate(c.ID, c.Version); err != nil {
		t.Fatal(err)
	}
	if rows, err := l.Candidates(); err != nil || len(rows) != 0 {
		t.Fatal("candidate remains", err)
	}
	if _, ok := findEntry(l.Entries(), c.Entry.ID); !ok {
		t.Fatal("candidate deletion cascaded into world knowledge")
	}
}
