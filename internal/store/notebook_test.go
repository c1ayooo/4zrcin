package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

func noteStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func noteRun(t *testing.T, s *Store) model.Run {
	t.Helper()
	r, err := s.AddRun(model.Run{Purpose: "commander", CommanderRound: 7})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNotebookCASReceiptsAndRecovery(t *testing.T) {
	s := noteStore(t)
	run := noteRun(t, s)
	f, err := s.AddFact(model.Fact{Kind: "note", Title: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := s.FactVersionsAt(s.Watermark())
	if err != nil {
		t.Fatal(err)
	}
	beforeRev, beforeHash := s.Revision(), s.ContentHash()
	note, err := s.SaveNotebook(0, "判断，引用 "+f.ID, "commander", run.ID, refs)
	if err != nil {
		t.Fatal(err)
	}
	if note.Version != 1 || note.Round != 7 || note.RunID != run.ID || note.SHA256 != NoteHash(note.State) || note.FactClaimVersions[f.ID] != f.ClaimVersion {
		t.Fatalf("bad receipt: %+v", note)
	}
	if s.Revision() != beforeRev || s.ContentHash() != beforeHash {
		t.Fatal("private note became shared news")
	}
	if _, err := s.SaveNotebook(0, "stale", "operator", "", nil); !errors.Is(err, ErrNoteConflict) {
		t.Fatalf("stale overwrite: %v", err)
	}
	if _, ok := s.ValidNoteHandoff(run.ID, 0); ok {
		t.Fatal("unfinished run proved a handoff")
	}
	if _, err := s.UpdateRun(run.ID, func(r *model.Run) { r.Status = model.RunDone }); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ValidNoteHandoff(run.ID, 0); !ok {
		t.Fatal("successful writer lost its receipt")
	}
	if _, ok := s.ValidNoteHandoff("other", 0); ok {
		t.Fatal("another round borrowed receipt")
	}
	if _, ok := s.ValidNoteHandoff(run.ID, 1); ok {
		t.Fatal("preexisting version satisfied handoff")
	}
	if _, err := s.SaveNotebook(1, "operator correction", "operator", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ValidNoteHandoff(run.ID, 0); ok {
		t.Fatal("operator overwrite satisfied commander's handoff")
	}
	if _, err := s.SaveNotebook(2, "late", "commander", run.ID, nil); !errors.Is(err, ErrNoteConflict) {
		t.Fatalf("settled writer accepted: %v", err)
	}
	refs[f.ID] = 999
	note.FactVersions[f.ID] = 999
	old, ok := s.NotebookVersion(1)
	if !ok || old.FactVersions[f.ID] != 1 {
		t.Fatalf("history shares mutable references: %+v", old)
	}
	path := s.Path()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	old, ok = reopened.NotebookVersion(1)
	if !ok || old.SHA256 != NoteHash(old.State) || old.RunID != run.ID {
		t.Fatal("note history lost across restart")
	}
	savedRun, _ := reopened.GetRun(run.ID)
	if len(savedRun.NoteWrites) != 1 || savedRun.NoteWrites[0] != old.NoteRef {
		t.Fatal("writer receipt lost across restart")
	}
	view, v := reopened.ReadView()
	if len(view.Notebooks) != 0 {
		t.Fatal("private note leaked into shared snapshot")
	}
	if view, err = reopened.ReadAt(v); err != nil || len(view.Notebooks) != 0 {
		t.Fatal("private note leaked into frozen snapshot")
	}
	changes, _, err := reopened.ChangeWindow(v, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Kind == "notebook" {
			t.Fatal("private note leaked into changes")
		}
	}
}

func TestConcurrentNotebookReplacementHasOneWinner(t *testing.T) {
	s := noteStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, text := range []string{"a", "b"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			_, err := s.SaveNotebook(0, text, "operator", "", nil)
			errs <- err
		}(text)
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrNoteConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 || len(s.History("notebook", "commander")) != 1 {
		t.Fatal("raced writes both succeeded or left extra versions")
	}
}

func TestNotebookRejectsStaleFactAndRollsBackFailedCommit(t *testing.T) {
	s := noteStore(t)
	run := noteRun(t, s)
	f, err := s.AddFact(model.Fact{Kind: "note", Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	refs, _ := s.FactVersionsAt(s.Watermark())
	if _, err := s.UpdateFact(f.ID, func(f *model.Fact) { f.Title = "second" }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveNotebook(0, "stale source", "commander", run.ID, refs); !errors.Is(err, ErrNoteConflict) {
		t.Fatalf("stale fact accepted: %v", err)
	}
	if _, err := s.sqlite.db.Exec(`CREATE TRIGGER reject_note BEFORE INSERT ON objects WHEN NEW.kind='notebook' BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveNotebook(0, "failed commit", "commander", run.ID, nil); err == nil {
		t.Fatal("failed transaction reported success")
	}
	if _, ok := s.Notebook(); ok {
		t.Fatal("failed transaction left a note")
	}
	run, _ = s.GetRun(run.ID)
	if len(run.NoteWrites) != 0 || len(s.History("notebook", "commander")) != 0 {
		t.Fatal("failed transaction left a receipt")
	}
}
