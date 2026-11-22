package api

import (
	"fmt"
	"strings"

	"github.com/kksty/tianwu/internal/model"
)

// BenchAuthor is the ledger author for writes made by the benchmark runner. It
// is deliberately not the operator and not a run id: the runner may narrow the
// target set for a measured run, and the ledger has to say so in a way that a
// later reader cannot mistake for the operator's own instruction.
const BenchAuthor = "bench"

// BenchActorHeader is the one header the benchmark runner may use to name
// itself. It is read in exactly one place — documentAuthor — so it changes a
// signature, not a permission: the write routes still admit only the operator's
// credential, and a participant identity is refused before the author is ever
// computed.
const BenchActorHeader = "X-Tw-Actor"

// AppendBenchGoal adds one goal for one challenge, idempotently.
//
// The key is derived from the challenge's unique_code so a supervisor that polls
// the platform every few seconds cannot pile up duplicate goals: the same
// challenge finds the goal it created and reports created=false. The statement
// is stable across container restarts (addresses go into the scope document, not
// here), so replacing a damaged environment never rewrites a goal's receipt.
func (s *Server) AppendBenchGoal(uniqueCode, statement, start string) (model.Goal, bool, error) {
	uniqueCode = strings.TrimSpace(uniqueCode)
	if uniqueCode == "" {
		return model.Goal{}, false, fmt.Errorf("bench: goal needs a challenge unique_code")
	}
	if strings.TrimSpace(statement) == "" {
		return model.Goal{}, false, fmt.Errorf("bench: goal for %q needs a statement", uniqueCode)
	}
	return s.Store.AddGoalIdempotent(model.Goal{
		Statement:      statement,
		Start:          start,
		IdempotencyKey: "bench-goal:" + uniqueCode,
		UpdatedBy:      BenchAuthor,
	})
}
