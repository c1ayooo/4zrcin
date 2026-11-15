package decider

import (
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// The automatic fallback request is only for "goal open + work attempted +
// nothing in flight". A fresh goal or any live direction must not create it.
func TestIdleGoalNeedsOperator(t *testing.T) {
	st := newStore(t)
	r := NewRunner(st, nil, "")
	if r.idleGoalNeedsOperator() {
		t.Fatal("a project without a goal is not idle-waiting for the operator")
	}
	if _, err := st.AddGoal(model.Goal{Statement: "open goal"}); err != nil {
		t.Fatal(err)
	}
	if r.idleGoalNeedsOperator() {
		t.Fatal("a fresh goal with no direction is not idle-waiting")
	}
	done, err := st.CreateIntent(model.Intent{Title: "finished work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateIntent(done.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
		t.Fatal(err)
	}
	if !r.idleGoalNeedsOperator() {
		t.Fatal("an open goal after work is idle-waiting")
	}
	pending, err := st.CreateIntent(model.Intent{Title: "pending work"})
	if err != nil {
		t.Fatal(err)
	}
	if r.idleGoalNeedsOperator() {
		t.Fatal("a pending direction is work, not idle-waiting")
	}
	if _, err := st.UpdateIntent(pending.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
		t.Fatal(err)
	}
	if !r.idleGoalNeedsOperator() {
		t.Fatal("the idle state returned after the direction finished")
	}
}

// The fallback is control state, so it is created without a model round and
// stays single while idle; work resumes and it is dismissed.
func TestRefreshIdleOperatorRequestCreatesAndDismissesFallback(t *testing.T) {
	st := newStore(t)
	r := NewRunner(st, nil, "")
	if _, err := st.AddGoal(model.Goal{Statement: "open goal"}); err != nil {
		t.Fatal(err)
	}
	done, err := st.CreateIntent(model.Intent{Title: "finished work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateIntent(done.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
		t.Fatal(err)
	}

	r.refreshIdleOperatorRequest()
	pending := st.PendingOperatorRequests()
	if len(pending) != 1 || !pending[0].Generic {
		t.Fatalf("fallback = %+v", pending)
	}
	r.refreshIdleOperatorRequest()
	if got := len(st.PendingOperatorRequests()); got != 1 {
		t.Fatalf("fallback duplicated: %d", got)
	}

	if _, err := st.CreateIntent(model.Intent{Title: "new work"}); err != nil {
		t.Fatal(err)
	}
	r.refreshIdleOperatorRequest()
	if got := len(st.PendingOperatorRequests()); got != 0 {
		t.Fatalf("fallback survived resumed work: %d", got)
	}
	if got, _ := st.GetOperatorRequest(pending[0].ID); got.Status != model.OperatorRequestDismissed {
		t.Fatalf("fallback status = %q", got.Status)
	}
}
