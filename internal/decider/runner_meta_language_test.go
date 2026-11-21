package decider

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/board"
	"github.com/kksty/tianwu/internal/model"
)

// The runner-side fragments were outside the retired-phrase list: that list only
// scanned prompt.go and decider.go, so eight commander-facing strings kept the
// word after the earlier cleanup. Assert on the products that carry them, not on
// the wording, so a reworded variant cannot slip back.
func TestRunnerFragmentsCarryNoAuthorizationWording(t *testing.T) {
	st := newStore(t)
	r := NewRunner(st, nil, "")
	if _, err := st.AddGoal(model.Goal{Statement: "朝着站点权限或 RCE 去打"}); err != nil {
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
	<-r.wake

	b := board.Build(st, 20)
	products := map[string]string{
		"idle replan":    r.idleReplanSection(b, false),
		"handoff replan": r.idleReplanSection(b, true),
		"idle detail":    r.heartbeatIdleDetail(),
	}
	for name, text := range products {
		if strings.Contains(text, "授权") {
			t.Fatalf("%s still carries authorization wording:\n%s", name, text)
		}
	}
}
