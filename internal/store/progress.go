package store

import (
	"encoding/json"

	"github.com/kksty/tianwu/internal/model"
)

// RoundProgress is one completed decision round's view of the durable journal:
// did the engagement move since the previous counted round, and how many
// consecutive rounds have moved nothing.
//
// It is the same journal differential the attention router reads
// (attention.go), narrowed to the three events that mean a direction actually
// advanced: a new fact, a new intent, or an intent reaching a settled status.
// Nothing here judges whether the work was useful; it only asks whether the
// ledger grew. A round that only chatted, updated a note or failed a tool is a
// no-progress round, which is exactly the silence this counter exists to expose.
type RoundProgress struct {
	Rounds        int // consecutive no-progress rounds, including this one
	Escalate      bool
	NewFacts      int
	NewIntents    int
	ClosedIntents int
}

// ObserveDecisionRound records one decision round against the journal watermark
// the previous round consumed. The watermark and the counter live in Memory,
// not in the runner: a restart must resume the count instead of starting from
// zero and hiding a direction that has been idling for hours. It writes through
// flushNoRev because bookkeeping must not wake the commander (the same reason
// ReviewCursors and the attention checkpoint use it).
//
// The caller decides which rounds count. A handoff round deliberately
// dispatches nothing, and a round that never reached the model is not a
// decision, so neither may advance this counter (see decider.Runner).
func (s *Store) ObserveDecisionRound(limit int) (RoundProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := Memory{Epoch: s.readEpoch, Checkpoints: map[string]Checkpoint{}}
	if s.db.Memory != nil {
		mem = *s.db.Memory
	}
	out := RoundProgress{}
	for _, c := range mem.History {
		if c.Seq <= mem.StallThrough {
			continue
		}
		switch {
		case c.Kind == "fact" && c.Operation == "create":
			out.NewFacts++
		case c.Kind == "intent" && c.Operation == "create":
			out.NewIntents++
		case c.Kind == "intent" && c.Operation == "update" && intentClosed(c):
			out.ClosedIntents++
		}
	}
	progressed := out.NewFacts+out.NewIntents+out.ClosedIntents > 0
	if progressed {
		mem.StallRounds = 0
		// A stall episode ends here: the next dry run starts a fresh episode
		// and may escalate again once it has earned a full limit.
		mem.StallEscalated = false
	} else {
		mem.StallRounds++
	}
	out.Rounds = mem.StallRounds
	if !progressed && limit > 0 && mem.StallRounds >= limit && !mem.StallEscalated {
		// One visible alert per stall episode: the flag is durable, so neither
		// the following round nor a restart repeats the same escalation.
		mem.StallEscalated = true
		out.Escalate = true
	}
	mem.StallThrough = mem.Seq
	s.db.Memory = &mem
	return out, s.flushNoRev("memory")
}

// StallRounds is the durable consecutive no-progress count. It is read when a
// runner starts so the console shows the reason a direction went quiet before
// the current process existed.
func (s *Store) StallRounds() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db.Memory == nil {
		return 0
	}
	return s.db.Memory.StallRounds
}

// intentClosed recognises the status transition itself, not a later edit of an
// already-settled intent: only an update that changed "status" may count, so a
// run-id fix on a finished direction does not fake progress for another round.
func intentClosed(c Change) bool {
	changed := false
	for _, f := range c.Fields {
		if f == "status" {
			changed = true
			break
		}
	}
	if !changed {
		return false
	}
	var row struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(c.Value, &row) != nil {
		return false
	}
	return model.Settled(row.Status)
}
