package bench

import (
	"strings"
	"testing"
	"time"
)

// Retry only what a repeat can fix. A business refusal repeated is a spin, and an
// unattended eight-hour run must not spin.
func TestRetryableOnlyCoversConnectClassFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "503 resource_unavailable", err: &Failure{Status: 503, Code: CodeResourceUnavailable}, want: true},
		{name: "500 internal", err: &Failure{Status: 500, Code: CodeInternalError}, want: true},
		{name: "404 task_not_found", err: &Failure{Status: 404, Code: CodeTaskNotFound}, want: false},
		{name: "409 invalid_state", err: &Failure{Status: 409, Code: CodeInvalidState, Message: "finished"}, want: false},
		{name: "422 validation", err: &Failure{Status: 422}, want: false},
	}
	for _, tc := range cases {
		if got := Retryable(tc.err); got != tc.want {
			t.Errorf("%s: Retryable = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !Retryable(&timeoutErr{}) {
		t.Error("a timeout must be retryable")
	}
}

type timeoutErr struct{}

func (*timeoutErr) Error() string { return "i/o timeout" }
func (*timeoutErr) Timeout() bool { return true }

// One goal per open challenge, a start for each container that is not up, and the
// scope written only when the address set actually changed.
func TestPlanSuppliesGoalsStartsAndScope(t *testing.T) {
	snap := NewSnapshot(time.Now(), []Challenge{
		{UniqueCode: "web_sql_injection_01", ContainerStatus: ContainerAvailable, ContainerAddr: []string{"10.0.1.5:8080"}},
		{UniqueCode: "crypto_rsa_02", ContainerStatus: ContainerStopped},
	})
	steps := Plan(State{}, snap, "challenges-1.json")
	var goals, starts int
	var scope *Step
	for i := range steps {
		switch steps[i].Kind {
		case StepEnsureGoal:
			goals++
			if steps[i].Text == "" || steps[i].Code == "" {
				t.Fatalf("goal step without text or code: %+v", steps[i])
			}
		case StepStart:
			starts++
		case StepWriteScope:
			scope = &steps[i]
		case StepStop:
			t.Fatalf("unexpected stop: %+v", steps[i])
		}
	}
	if goals != 2 || starts != 1 {
		t.Fatalf("goals=%d starts=%d, want 2 and 1", goals, starts)
	}
	if scope == nil || len(scope.Addrs) != 1 || scope.Addrs[0] != "10.0.1.5:8080" {
		t.Fatalf("scope step = %+v, want the available address only", scope)
	}

	// A second poll with the same snapshot and the work already done plans nothing
	// new: no duplicate goals, no scope rewrite (that would bump the revision and
	// spam the audit for no change).
	done := State{GoalsEnsured: map[string]bool{"web_sql_injection_01": true, "crypto_rsa_02": true},
		ScopeAddrs: []string{"10.0.1.5:8080"}}
	snap2 := NewSnapshot(time.Now(), []Challenge{
		{UniqueCode: "web_sql_injection_01", ContainerStatus: ContainerAvailable, ContainerAddr: []string{"10.0.1.5:8080"}},
		{UniqueCode: "crypto_rsa_02", ContainerStatus: ContainerAvailable, ContainerAddr: []string{"10.0.1.5:8080"}},
	})
	if steps := Plan(done, snap2, "challenges-2.json"); len(steps) != 0 {
		t.Fatalf("idle poll planned %+v, want nothing", steps)
	}
}

// Nothing available yet is not a stop: the run must first ask the platform to
// start a container. Only when every challenge was already asked to start and the
// platform still reports nothing available does the run stop — at that point the
// old address list is the only thing a commander could read, and it may point at
// other challenges' containers.
func TestPlanStartsBeforeItStops(t *testing.T) {
	stopped := NewSnapshot(time.Now(), []Challenge{{UniqueCode: "x", ContainerStatus: ContainerStopped}})
	steps := Plan(State{}, stopped, "challenges-3.json")
	var gotStart bool
	for _, st := range steps {
		if st.Kind == StepStop {
			t.Fatalf("first poll stopped instead of starting: %+v", steps)
		}
		if st.Kind == StepStart && st.Code == "x" {
			gotStart = true
		}
	}
	if !gotStart {
		t.Fatalf("plan = %+v, want a start for the stopped challenge", steps)
	}

	// Already asked, still stopped: stop without clearing the scope.
	asked := State{GoalsEnsured: map[string]bool{"x": true}, Started: map[string]bool{"x": true}}
	steps = Plan(asked, stopped, "challenges-3.json")
	if len(steps) != 1 || steps[0].Kind != StepStop || steps[0].Code != "no_available_address" {
		t.Fatalf("plan after an ineffective start = %+v, want no_available_address", steps)
	}
	if !strings.Contains(steps[0].Reason, "challenges-3.json") {
		t.Fatalf("stop reason does not name the snapshot: %q", steps[0].Reason)
	}

	// Mid-transition statuses are progress: wait, do not start again, do not stop.
	for _, status := range []string{ContainerPending, ContainerStopPending} {
		snap := NewSnapshot(time.Now(), []Challenge{{UniqueCode: "y", ContainerStatus: status,
			ContainerAddr: []string{"10.0.9.9:8080"}}})
		steps := Plan(State{GoalsEnsured: map[string]bool{"y": true}}, snap, "challenges-3.json")
		for _, st := range steps {
			if st.Kind == StepStop || st.Kind == StepStart || st.Kind == StepWriteScope {
				t.Fatalf("%s plan = %+v, want to wait", status, steps)
			}
		}
	}

	// An empty challenge list is its own fact, not a completion.
	if steps := Plan(State{}, NewSnapshot(time.Now(), nil), "challenges-3.json"); len(steps) != 1 ||
		steps[0].Code != "no_challenges" {
		t.Fatalf("empty list plan = %+v, want no_challenges", steps)
	}
}

// Everything completed is a clean finish, not a failure.
func TestPlanStopsWhenAllChallengesAreComplete(t *testing.T) {
	snap := NewSnapshot(time.Now(), []Challenge{
		{UniqueCode: "a", IsCompleted: true, ContainerStatus: ContainerStopped},
	})
	steps := Plan(State{}, snap, "challenges-4.json")
	if len(steps) != 1 || steps[0].Code != "all_completed" {
		t.Fatalf("plan = %+v, want all_completed", steps)
	}
}

// The platform's progress overwrites ours, and the difference is recorded.
func TestDiffProgressReportsWhatChanged(t *testing.T) {
	prev := map[string]Challenge{"a": {UniqueCode: "a", CorrectFlagCount: 0, IsCompleted: false},
		"b": {UniqueCode: "b", CorrectFlagCount: 2, IsCompleted: true}}
	snap := NewSnapshot(time.Now(), []Challenge{
		{UniqueCode: "a", CorrectFlagCount: 1, IsCompleted: false},
		{UniqueCode: "b", CorrectFlagCount: 2, IsCompleted: true},
	})
	diffs := DiffProgress(prev, snap)
	if len(diffs) != 1 || diffs[0].Code != "a" || diffs[0].NowCorrect != 1 {
		t.Fatalf("diffs = %+v, want one change for a", diffs)
	}
	if got := DiffProgress(DigestSnapshot(snap), snap); len(got) != 0 {
		t.Fatalf("identical snapshots reported %+v", got)
	}
}
