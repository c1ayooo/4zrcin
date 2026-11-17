package api

import (
	"encoding/json"
	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/model"
	"net/http"
	"strings"
)

// A source reference proves who supplied the instruction, not that every
// interpretation of its prose is correct. The operator can inspect both the
// message and the durable goal change. Only a live commander may act for them.
func (s *Server) authorizeGoalDefinition(w http.ResponseWriter, r *http.Request, messageID string) (string, string, bool) {
	if !harnessSession(r) {
		return "", "", true
	}
	c := s.caller(r)
	run, live := s.Store.GetRun(c.runID)
	msg, exists := s.Store.MessageByID(strings.TrimSpace(messageID))
	if !c.agent || c.participant != collab.CommanderID || !live || run.Purpose != "commander" || run.Status != model.RunRunning || !exists || msg.FromParticipantID != collab.OperatorID || msg.ToParticipantID != collab.CommanderID || strings.TrimSpace(msg.Text) == "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "目标变更必须由操作员执行，或由当前指挥官引用操作员发给指挥官的明确指令 operator_message_id", "code": "goal_operator_only"})
		return "", "", false
	}
	return msg.ID, c.runID, true
}

func (s *Server) updateGoal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Statement         string `json:"statement"`
		Start             string `json:"start"`
		OperatorMessageID string `json:"operator_message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	ref, run, ok := s.authorizeGoalDefinition(w, r, req.OperatorMessageID)
	if !ok {
		return
	}
	g, err := s.Store.EditGoal(r.PathValue("id"), req.Statement, req.Start, ref, run)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	s.recordAction("goal_update", g.ID, g.Statement)
	writeJSON(w, 200, g)
}

func (s *Server) closeGoal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason            string `json:"reason"`
		OperatorMessageID string `json:"operator_message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	ref, run, ok := s.authorizeGoalDefinition(w, r, req.OperatorMessageID)
	if !ok {
		return
	}
	g, err := s.Store.CloseGoal(r.PathValue("id"), req.Reason, ref, run)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	s.recordAction("goal_close", g.ID, g.ClosedReason)
	writeJSON(w, 200, g)
}
