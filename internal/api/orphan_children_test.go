package api

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// A spawned child is only dispatched while its parent is alive: the scheduler
// refuses to spend a run on results nobody will consume. That refusal used to
// leave the child pending forever, which the console cannot distinguish from
// work waiting its turn. These tests pin the explicit failure and its reason.
func TestUnstartedChildrenOfASettledParentAreFailed(t *testing.T) {
	for _, status := range []string{model.StatusDone, model.StatusFailed, model.StatusReviewFailed} {
		t.Run(status, func(t *testing.T) {
			s, _, _, _ := finishedServer(t, model.StatusDone, model.RunDone, true)
			parent, child := parentWithPendingChild(t, s, "父方向（"+status+"）")

			if _, err := s.Store.UpdateIntent(parent.ID, func(i *model.Intent) { i.Status = status }); err != nil {
				t.Fatal(err)
			}
			s.pump()

			got, _ := s.Store.GetIntent(child.ID)
			if got.Status != model.StatusFailed {
				t.Fatalf("child status = %q, want failed", got.Status)
			}
			if !got.Hold {
				t.Fatal("failed orphan is not held: the next pump could dispatch it")
			}
			if got.RunID != "" {
				t.Fatalf("orphan started a run (%s) instead of being settled", got.RunID)
			}
			note, ok := lastNoteOfKind(got, model.IntentNoteOrphan)
			if !ok {
				t.Fatalf("no orphan note recorded: %+v", got.Notes)
			}
			if !strings.Contains(note.Text, parent.ID) || !strings.Contains(note.Text, status) {
				t.Fatalf("note %q does not name the parent and its final state", note.Text)
			}
		})
	}
}

// Parking is an operator action that never runs the scheduler, so it has to
// settle the children itself.
func TestParkingADirectionFailsItsUnstartedChildren(t *testing.T) {
	s, _, _, _ := finishedServer(t, model.StatusDone, model.RunDone, true)
	parent, child := parentWithPendingChild(t, s, "父方向（停放）")

	if _, err := s.ParkIntent(parent.ID, "等操作员定夺"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Store.GetIntent(child.ID)
	if got.Status != model.StatusFailed {
		t.Fatalf("child status = %q, want failed", got.Status)
	}
	note, ok := lastNoteOfKind(got, model.IntentNoteOrphan)
	if !ok || !strings.Contains(note.Text, model.StatusParked) {
		t.Fatalf("orphan note for a parked parent = %+v", got.Notes)
	}
}

// The rule is about ended parents only. A live parent must still get its child
// dispatched, and a held parent must keep its child waiting: hold is a pause,
// not an end.
func TestLiveOrHeldParentsKeepTheirChildren(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		s, ex, _, _ := finishedServer(t, model.StatusDone, model.RunDone, true)
		_, child := parentWithPendingChild(t, s, "父方向（活）")
		s.pump()
		got, _ := s.Store.GetIntent(child.ID)
		if got.Status == model.StatusFailed {
			t.Fatalf("live parent's child was failed: %+v", got.Notes)
		}
		if len(ex.requests()) == 0 && got.RunID == "" {
			t.Fatalf("live parent's child was neither dispatched nor recorded: %+v", got)
		}
	})

	t.Run("held", func(t *testing.T) {
		s, _, _, _ := finishedServer(t, model.StatusDone, model.RunDone, true)
		parent, child := parentWithPendingChild(t, s, "父方向（hold）")
		if _, err := s.Store.UpdateIntent(parent.ID, func(i *model.Intent) { i.Hold = true }); err != nil {
			t.Fatal(err)
		}
		s.pump()
		got, _ := s.Store.GetIntent(child.ID)
		if got.Status != model.StatusPending {
			t.Fatalf("held parent's child = %q, want it still pending", got.Status)
		}
	})
}

// Every pump walks the same rows, so the failure must not append a second note
// or rewrite a direction that already moved on.
func TestOrphanSettlementIsIdempotent(t *testing.T) {
	s, _, _, _ := finishedServer(t, model.StatusDone, model.RunDone, true)
	parent, child := parentWithPendingChild(t, s, "父方向（重复 pump）")
	if _, err := s.Store.UpdateIntent(parent.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
		t.Fatal(err)
	}
	s.pump()
	s.pump()

	got, _ := s.Store.GetIntent(child.ID)
	n := 0
	for _, note := range got.Notes {
		if note.Kind == model.IntentNoteOrphan {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("orphan notes = %d, want exactly one: %+v", n, got.Notes)
	}
	if got.Status != model.StatusFailed {
		t.Fatalf("child status = %q, want failed", got.Status)
	}
}

func parentWithPendingChild(t *testing.T, s *Server, title string) (model.Intent, model.Intent) {
	t.Helper()
	parent, err := s.Store.CreateIntent(model.Intent{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := s.Store.CreateSpawnIntent(model.Intent{
		Title:          title + " · 子",
		ParentID:       parent.ID,
		CreatedBy:      "pt_orphan_test",
		IdempotencyKey: "orphan-" + parent.ID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != model.StatusPending {
		t.Fatalf("fixture child started as %q, want pending", child.Status)
	}
	return parent, child
}

func lastNoteOfKind(in model.Intent, kind string) (model.IntentNote, bool) {
	for i := len(in.Notes) - 1; i >= 0; i-- {
		if in.Notes[i].Kind == kind {
			return in.Notes[i], true
		}
	}
	return model.IntentNote{}, false
}
