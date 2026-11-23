package api

import (
	"log"
	"time"

	"github.com/kksty/tianwu/internal/model"
)

// maxRedispatches bounds how many times one direction may be restarted after an
// unplanned interruption. It is a stop-loss, not a scheduler: three restarts
// with no result means the direction needs a human or the commander, and a
// fourth automatic run would only spend tokens on the same wall.
const maxRedispatches = 3

// interrupted says which cancellation causes mean "the work was interrupted"
// rather than "somebody decided to stop".
//
// Explicit stops (operator, stop-loss, worker kill, model stall) hold the intent
// and are never restarted: a hold is a decision, and CancelIntent sets it before
// cancelling, so the operator's cancel cannot be undone by this path. Pause,
// steer and review pause have their own continuation semantics.
//
// An empty cause is an interruption we could not classify — historically it was
// recorded as "operator", which silently lost the work (22 of 49 cancelled runs
// in the TSecBench 2026-11-22 package were never retried).
func interrupted(cause string) bool {
	switch cause {
	case "", model.CancelCauseWatchdog, model.CancelCauseRestart:
		return true
	}
	return false
}

// redispatchesUsed counts the earlier runs of this direction that ended in an
// interruption. The cap is on interruptions, not on runs: a direction that
// finishes and is later reopened by the commander starts fresh.
func (s *Server) redispatchesUsed(intentID string) int {
	n := 0
	for _, r := range s.Store.Runs() {
		if r.IntentID == intentID && r.Status == model.RunCanceled && interrupted(r.CancelCause) {
			n++
		}
	}
	return n
}

// redispatchAfterCancel restarts a direction whose run the watchdog killed, so
// the work does not depend on the commander happening to notice it is missing.
//
// Only the watchdog qualifies at runtime. The other cancellation causes are
// decisions (operator, stop-loss, worker kill, model stall hold the intent) or
// have their own continuation (steer, pause, review pause). Restart is handled at
// resume instead: the project-stop path cancels every run with cause=restart, and
// restarting them from inside that loop would fight the shutdown.
func (s *Server) redispatchAfterCancel(runID, intentID string) {
	if s.paused.Load() || s.closing.Load() || s.deleting.Load() {
		return
	}
	run, ok := s.Store.GetRun(runID)
	if !ok || run.Status != model.RunCanceled || run.CancelCause != model.CancelCauseWatchdog {
		return
	}
	s.redispatchOne(runID, intentID, run.CancelCause)
}

// redispatchInterrupted restarts directions that were interrupted by the process
// going away. It runs after an explicit project resume, never at boot: the boot
// path deliberately keeps the project paused, and a restart must not silently
// replay a whole night of work before anyone looks at it.
func (s *Server) redispatchInterrupted() {
	if s.paused.Load() || s.closing.Load() || s.deleting.Load() {
		return
	}
	for _, in := range s.Store.ListIntents() {
		if in.Status != model.StatusPending || in.Hold || in.Aborted || in.Wait != nil {
			continue
		}
		last, ok := s.Store.GetRun(in.RunID)
		if !ok || last.Status != model.RunCanceled {
			continue
		}
		if last.CancelCause != model.CancelCauseRestart && last.CancelCause != "" {
			continue
		}
		s.redispatchOne(last.ID, in.ID, last.CancelCause)
	}
}

// redispatchOne is the guarded restart: every check here is a reason a restart
// would be wrong, and the budget is checked before the run is started.
func (s *Server) redispatchOne(runID, intentID, cause string) {
	in, ok := s.Store.GetIntent(intentID)
	if !ok || in.Status != model.StatusPending || in.Hold || in.Aborted || in.Wait != nil {
		return
	}
	if in.RunID != "" {
		if live, ok := s.Store.GetRun(in.RunID); ok && live.Status == model.RunRunning {
			return
		}
	}
	used := s.redispatchesUsed(intentID)
	if used > maxRedispatches {
		s.noteRedispatch(runID, intentID, used, cause, "超过重派上限，留给指挥官决定")
		return
	}
	if _, err := s.StartRun(intentID); err != nil {
		log.Printf("重派 %s（第 %d 次）：%v", intentID, used, err)
		s.noteRedispatch(runID, intentID, used, cause, "启动失败："+err.Error())
		return
	}
	s.noteRedispatch(runID, intentID, used, cause, "")
}

// noteRedispatch writes the decision to the cancelled run's journal. The run is
// over, so the line is appended on disk: it is the only place a review can see
// why the same direction ran again.
func (s *Server) noteRedispatch(runID, intentID string, attempt int, cause, skip string) {
	line := map[string]any{
		"kind": "run_redispatch", "at": time.Now(), "run_id": runID, "intent_id": intentID,
		"attempt": attempt, "cause": cause, "max": maxRedispatches,
	}
	if skip != "" {
		line["skipped"] = skip
	}
	if err := s.appendJournalOnDisk(runID, line); err != nil {
		log.Printf("run %s: 记录重派失败：%v", runID, err)
	}
}
