package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// ---- goals ----

func (s *Server) listGoals(w http.ResponseWriter, r *http.Request) {
	// revision is the goal document's revision, served with every goal: the goal
	// editor submits it back, and a save without the current one is refused
	// instead of silently overwriting a newer edit.
	type goalRow struct {
		model.Goal
		Revision uint64 `json:"revision"`
	}
	goals := s.Store.Goals()
	rows := make([]goalRow, 0, len(goals))
	for _, g := range goals {
		rows = append(rows, goalRow{Goal: g, Revision: s.Store.GoalRevision(g.ID)})
	}
	writeJSON(w, 200, map[string]any{"goals": rows})
}

func (s *Server) createGoal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Statement         string `json:"statement"`
		Start             string `json:"start"`
		QuantifiedTarget  string `json:"quantified_target"`
		OperatorMessageID string `json:"operator_message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	req.Statement = strings.TrimSpace(req.Statement)
	req.Start = strings.TrimSpace(req.Start)
	if req.Statement == "" {
		writeErr(w, 400, "statement 不能为空：目标要能判断真假")
		return
	}
	// Goals are the operator's definition of the endpoint. A harness session may
	// only act for the operator when it cites an explicit operator instruction:
	// authorizeGoalDefinition requires the live commander and an operator message
	// addressed to it, and the reference is frozen on the goal (InstructionRef /
	// CreatedByRun). Executors cannot write goals at all.
	ref, runID, ok := s.authorizeGoalDefinition(w, r, req.OperatorMessageID)
	if !ok {
		return
	}
	g, err := s.Store.AddGoal(model.Goal{
		Statement:      req.Statement,
		Start:          req.Start,
		InstructionRef: ref, CreatedByRun: runID,
		// The first revision is signed like every later one: the operator's own
		// endpoint, or the commander run that acted for them on an instruction.
		UpdatedBy: documentAuthor(r),
		// Kept for API compatibility with older callers; the console no longer
		// offers a "quantified criterion" input.
		QuantifiedTarget: strings.TrimSpace(req.QuantifiedTarget),
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, g)
}

// pinGoal is the operator's manual seal: one evidence row plus the reason, with
// no citation requirement beyond verified_only's admission. It is deliberately
// operator-only — a session closes its goal through completeGoal, which enforces
// the ledger's citation rules — so a guessed intent id cannot hand-stamp the
// operator's endpoint (cmd/zrcin auth also 404s /met for the harness credential).
func (s *Server) pinGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if harnessSession(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "手工盖章属于操作员：指挥官判断达成请用 complete 并引用事实。",
			"code":  "goal_operator_only",
		})
		return
	}
	var req struct {
		EvidenceID string   `json:"evidence_id"`
		Note       string   `json:"note"`
		From       []string `json:"from"`
	}
	if !decodeOptionalJSON(w, r.Body, &req) {
		return
	}
	policy := s.premisePolicy()
	if policy == model.PremiseVerifiedOnly && len(req.From) == 0 {
		for _, f := range s.Store.ListFacts("", 0) {
			if model.PremiseIssue(f, policy) != "" {
				continue
			}
			for _, eid := range f.EvidenceIDs {
				if eid == req.EvidenceID {
					req.From = []string{f.ID}
					break
				}
			}
			if len(req.From) > 0 {
				break
			}
		}
	}
	if policy == model.PremiseVerifiedOnly {
		ready := s.readiness(s.Store.Snapshot(), model.Intent{FromFactIDs: req.From}, policy, true)
		if !ready.Ready {
			writeErr(w, 400, "支持证据或当前断言未通过核验，请先检查前提")
			return
		}
	}
	g, err := s.Store.PinGoalByRunGate(id, req.EvidenceID, req.Note, req.From, "",
		store.GoalGate{ReviewsOff: s.ReviewsDisabled()}, policy)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// The operator is the final arbiter: this stamp must survive as a record
	// even after a reopen clears the goal row (see operator-actions.jsonl).
	if err := s.recordOperatorAction(stampedGoalAction(g)); err != nil {
		writeErr(w, http.StatusInternalServerError, "目标已盖章，但操作员审计写入失败："+err.Error())
		return
	}
	writeJSON(w, 200, g)
}

// reopenGoal withdraws a stamp by hand. complete and met can both pin a goal;
// when the judgement behind one turns out wrong, it must be correctable without
// inventing a second goal (D98). The reopened goal goes back into the board's
// open list with its receipt cleared; a goal that is already open is refused
// instead of being reported as "reopened".
//
// Who may withdraw: the operator always, a commander session only when a
// commander run made the stamp. The operator's manual judgement — and every goal
// stamped before MetByRun existed — must not be erasable from a session: it
// would vanish with no round log to point at. The withdrawn stamp is named in
// the round's action record before ReopenGoal clears who stamped it and why.
func (s *Server) reopenGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var goal model.Goal
	found := false
	for _, g := range s.Store.Goals() {
		if g.ID == id {
			goal, found = g, true
			break
		}
	}
	if !found {
		writeErr(w, http.StatusBadRequest, "goal "+id+" not found")
		return
	}
	if !s.authorizeGoalWithdrawal(w, r, goal) {
		return
	}
	stampedBy := goal.MetByRun
	if stampedBy == "" {
		stampedBy = "operator"
	}
	stampedAt := "未记录"
	if goal.CompletedAt != nil {
		stampedAt = goal.CompletedAt.Format(time.RFC3339)
	}
	g, err := s.Store.ReopenGoal(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAction("reopen", g.ID, fmt.Sprintf("撤销盖章（原盖章 %s，%s）：%s", stampedBy, stampedAt, goal.Note))
	// The withdrawn stamp is now the only copy of who stamped the goal and why:
	// record it before anyone can ask. See operator-actions.go.
	by := "operator"
	if who, ok := identityOf(r); ok {
		by = who.RunID
	}
	if err := s.recordOperatorAction(operatorAction{
		Action: "goal_reopen", GoalID: g.ID, By: by,
		StampedBy: stampedBy, StampedAt: stampedAt, StampedNote: goal.Note,
		MetByRun: goal.MetByRun,
	}); err != nil {
		// The stamp is already gone: put it in the response too, so the operator
		// can still see who stamped the goal and why even when the audit file
		// cannot be written.
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf(
			"盖章已撤销，但操作员审计写入失败（原盖章 %s，%s，理由：%s）：%v",
			stampedBy, stampedAt, goal.Note, err))
		return
	}
	writeJSON(w, 200, g)
}
