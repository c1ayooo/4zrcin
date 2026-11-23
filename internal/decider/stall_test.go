package decider

import (
	"context"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

func stallAlerted(d *Decision) bool {
	for _, note := range d.Notes {
		if strings.Contains(note, "没有进展") {
			return true
		}
	}
	return false
}

// 用例①: a dry direction gets one alert at the threshold and no repeat after
// it. 用例②: one round with real ledger progress zeroes the count, and the next
// alert requires another full episode. The alert is an observation only: no
// direction is created, stopped or held for it.
func TestStallEscalatesOnceAndResetsOnProgress(t *testing.T) {
	st := newStore(t)
	r := NewRunner(st, nil, "")
	r.ModelName = "fixture"
	r.Commander = func(context.Context, string, bool) (RoundResult, error) { return RoundResult{}, nil }
	round := func() *Decision {
		d := r.Run(context.Background(), "manual", true)
		if d == nil || d.Error != "" {
			t.Fatalf("round did not run: %+v", d)
		}
		return d
	}

	for i := 1; i < stallEscalationRounds; i++ {
		if stallAlerted(round()) {
			t.Fatalf("escalated at round %d, before the threshold %d", i, stallEscalationRounds)
		}
	}
	d := round()
	if !stallAlerted(d) {
		t.Fatalf("round %d did not escalate: %+v", stallEscalationRounds, d.Notes)
	}
	if got := r.Status().Stalled; got != stallEscalationRounds {
		t.Fatalf("visible stall count = %d, want %d", got, stallEscalationRounds)
	}
	if d = round(); stallAlerted(d) {
		t.Fatalf("the alert repeated on the next round: %+v", d.Notes)
	}
	if got := r.Status().Stalled; got != stallEscalationRounds+1 {
		t.Fatalf("count after the alert round = %d, want %d", got, stallEscalationRounds+1)
	}

	// A fact is the ledger moving: the episode ends and the counter resets.
	if _, err := st.AddFact(model.Fact{Title: "新的侦察结果"}); err != nil {
		t.Fatal(err)
	}
	d = round()
	if stallAlerted(d) {
		t.Fatalf("a progress round escalated: %+v", d.Notes)
	}
	if got := r.Status().Stalled; got != 0 {
		t.Fatalf("count after progress = %d, want 0", got)
	}

	for i := 1; i < stallEscalationRounds; i++ {
		if stallAlerted(round()) {
			t.Fatalf("new episode escalated at round %d, before the threshold", i)
		}
	}
	if !stallAlerted(round()) {
		t.Fatal("the new episode did not escalate after a full dry run")
	}

	// Observation only: the alert created no direction and did not pause.
	if intents := st.ListIntents(); len(intents) != 0 {
		t.Fatalf("stall observation created a direction: %+v", intents)
	}
	if r.Paused() {
		t.Fatal("stall observation paused the decider")
	}
	// A new runner on the same ledger resumes the count instead of showing a
	// fresh zero and hiding a direction that has been dry for the whole episode.
	restarted := NewRunner(st, nil, "")
	if got := restarted.Status().Stalled; got != stallEscalationRounds {
		t.Fatalf("count after runner restart = %d, want %d", got, stallEscalationRounds)
	}
}
