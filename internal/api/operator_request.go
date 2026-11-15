package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kksty/tianwu/internal/model"
)

type operatorInboxHandoff struct {
	IntentID string             `json:"intent_id"`
	Title    string             `json:"title"`
	Status   string             `json:"status"`
	Request  model.OperatorWait `json:"request"`
}

type operatorInboxAcceptance struct {
	GoalID         string   `json:"goal_id"`
	Statement      string   `json:"statement"`
	ReceiptChanged []string `json:"receipt_changed,omitempty"`
}

type operatorInbox struct {
	Authorization []model.OperatorRequest   `json:"authorization"`
	Handoff       []operatorInboxHandoff    `json:"handoff"`
	Acceptance    []operatorInboxAcceptance `json:"acceptance"`
	Warnings      []boardRunWarnings        `json:"warnings"`
	Total         int                       `json:"total"`
}

// operatorInbox is the console's single "what needs a human" read. It merges
// the project-level authorization requests with the per-intent human handoffs,
// stale goal receipts and run warnings that used to be scattered across pages.
// It is operator supervision: the route is hidden from harness sessions.
func (s *Server) operatorInbox(w http.ResponseWriter, r *http.Request) {
	out := operatorInbox{
		Authorization: []model.OperatorRequest{},
		Handoff:       []operatorInboxHandoff{},
		Acceptance:    []operatorInboxAcceptance{},
		Warnings:      []boardRunWarnings{},
	}
	for _, req := range s.Store.OperatorRequests() {
		if req.Status == model.OperatorRequestPending {
			out.Authorization = append(out.Authorization, req)
		}
	}
	for _, in := range s.Store.ListIntents() {
		if in.Wait == nil || in.Wait.Operator == nil || in.Wait.Operator.CompletedAt != nil {
			continue
		}
		out.Handoff = append(out.Handoff, operatorInboxHandoff{
			IntentID: in.ID, Title: in.Title, Status: in.Status, Request: *in.Wait.Operator,
		})
	}
	for _, g := range s.Store.Snapshot().Goals {
		if !g.ReceiptStale {
			continue
		}
		out.Acceptance = append(out.Acceptance, operatorInboxAcceptance{
			GoalID: g.ID, Statement: g.Statement, ReceiptChanged: g.ReceiptChanged,
		})
	}
	out.Warnings = recentHandoffWarnings(s.Store, 5, false)
	out.Total = len(out.Authorization) + len(out.Handoff) + len(out.Acceptance) + len(out.Warnings)
	writeJSON(w, 200, out)
}

// updateOperatorRequest is the commander's detail fill, also usable by the
// operator. It never marks the request resolved and never grants authorization.
func (s *Server) updateOperatorRequest(w http.ResponseWriter, r *http.Request) {
	// The operator and the commander (project mux, no participant identity) may
	// fill details. A direction's executor has a participant identity and must
	// not write the operator's inbox on another direction's behalf.
	if _, participant := identityOf(r); participant {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "补齐授权明细只能由指挥官或操作员提交。",
			"code":  "commander_or_operator_only",
		})
		return
	}
	var req struct {
		Action        string `json:"action"`
		Asset         string `json:"asset"`
		Impact        string `json:"impact"`
		Authorization string `json:"authorization"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil {
		writeErr(w, 400, "请求体不是 JSON")
		return
	}
	out, err := s.Store.UpdateOperatorRequest(r.PathValue("id"), req.Action, req.Asset, req.Impact, req.Authorization)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.recordAction("operator_request_detail", out.ID, out.Action)
	writeJSON(w, 200, map[string]any{"request": out})
}

func (s *Server) resolveOperatorRequest(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "处理操作员请求属于操作员：指挥官只能补明细，不能替操作员确认授权。",
			"code":  "operator_only",
		})
		return
	}
	var req struct {
		Resolution string `json:"resolution"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req)
	out, err := s.Store.ResolveOperatorRequest(r.PathValue("id"), req.Resolution)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.recordAction("operator_request_resolve", out.ID, strings.TrimSpace(req.Resolution))
	writeJSON(w, 200, map[string]any{"request": out})
}

// operatorRequestDetailSection is the one-shot prompt section for a request the
// operator asked the commander to enrich. It is injected before the board and
// the flags are cleared once the prompt carries them: a failed round can be
// retried by clicking again, but an unchanged round does not repeat forever.
func (s *Server) operatorRequestDetailSection() (string, []string) {
	var rows []model.OperatorRequest
	for _, req := range s.Store.OperatorRequests() {
		if req.Status == model.OperatorRequestPending && req.DetailRequested {
			rows = append(rows, req)
		}
	}
	if len(rows) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("\n## 操作员请求补齐授权明细\n操作员点了「让指挥官列明细」。只补明细，不派新方向，不要 complete/standby。\n")
	ids := make([]string, 0, len(rows))
	for _, req := range rows {
		ids = append(ids, req.ID)
		fmt.Fprintf(&b, "- request_id=%s；当前：%s\n", req.ID, strings.TrimSpace(req.Action))
		fmt.Fprintf(&b, "  用 `PUT /api/operator-requests/%s` 提交 `{\"action\":\"…\",\"asset\":\"…\",\"impact\":\"…\",\"authorization\":\"…\"}`；填写后请求会出现在操作员收件箱，它不是授权。\n", req.ID)
	}
	return b.String(), ids
}

// requestOperatorDetails asks the commander for one round that fills the
// concrete fields. The request id rides the next prompt once; it does not spend
// a round unless the operator clicks.
func (s *Server) requestOperatorDetails(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "让指挥官列明细属于操作员请求。",
			"code":  "operator_only",
		})
		return
	}
	if s.paused.Load() {
		writeErr(w, http.StatusConflict, "项目已暂停：先继续项目，再让指挥官列明细")
		return
	}
	if s.Decider == nil {
		writeErr(w, http.StatusConflict, "没有自动指挥官：不能请求明细")
		return
	}
	out, err := s.Store.RequestOperatorDetails(r.PathValue("id"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Decider.Wake("operator")
	writeJSON(w, http.StatusAccepted, map[string]any{"request": out, "woken": true})
}
