package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/aopexec"
	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/ledger"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/retrieval"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// ---- intents ----

type createIntentReq struct {
	ContextFactIDs     []string `json:"context_fact_ids"`
	PinnedFactIDs      []string `json:"pinned_fact_ids"`
	InterestEntityRefs []string `json:"interest_entity_refs"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Doc                string   `json:"doc"`
	Eval               string   `json:"eval"`
	MaxTurns           int      `json:"max_turns"`
	FromFactIDs        []string `json:"from_fact_ids"`
	Skills             []string `json:"skills"`
	// Node is the execution location; empty means the default node.
	Node string `json:"node"`
	// Profile is retired (the external gateway owns model routing). It is still
	// parsed so an old client gets an explicit 400 instead of a silent default.
	Profile string `json:"profile"`
}

func (s *Server) createIntent(w http.ResponseWriter, r *http.Request) {
	var req createIntentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	if req.Title == "" && req.Description == "" {
		writeErr(w, 400, "title 或 description 至少填一个")
		return
	}
	if req.Title == "" {
		req.Title = firstLine(req.Description)
	}
	req.Node = strings.TrimSpace(req.Node)
	if req.MaxTurns < 0 || req.MaxTurns > executor.MaxTurnsCap {
		// The value goes to the AOP turn request as a uint32 and to the harness
		// loop cap: a negative or absurd number is not a policy choice.
		writeErr(w, 400, fmt.Sprintf("max_turns 必须在 0–%d 之间（0 = 不设回合上限）", executor.MaxTurnsCap))
		return
	}
	if req.Node != "" {
		// Single-machine: the local host is the only execution position. An
		// explicit remote id from an old client is rejected, never silently
		// re-routed.
		if s.Project == nil {
			if !s.HasNode(req.Node) {
				writeErr(w, 400, "没有这个节点："+req.Node+"（现有："+strings.Join(s.nodeIDs(), ", ")+"）")
				return
			}
		} else if err := s.checkNode(req.Node); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	req.Profile = strings.TrimSpace(req.Profile)
	if req.Profile != "" {
		writeErr(w, 400, "模型选择由外部网关管理，请移除 profile")
		return
	}
	if err := s.checkPremiseRefs(req.FromFactIDs); err != nil {
		writePremiseError(w, err)
		return
	}
	// A no-premise direction is a genuine first touch: it runs, and the graph
	// anchors it to the project node. Holding it for operator confirmation was a
	// patch for unverified facts reaching a decision; the decision surface now
	// carries only verified facts, so there is nothing left for the hold to
	// protect, and it blocked authorized targets for no review reason.
	in, err := s.Store.CreateIntent(model.Intent{
		Title:              req.Title,
		Description:        req.Description,
		Doc:                req.Doc,
		Eval:               strings.TrimSpace(req.Eval),
		MaxTurns:           req.MaxTurns,
		FromFactIDs:        req.FromFactIDs,
		ContextFactIDs:     req.ContextFactIDs,
		PinnedFactIDs:      req.PinnedFactIDs,
		InterestEntityRefs: req.InterestEntityRefs,
		Skills:             req.Skills,
		Node:               req.Node,
		// Who created this direction is recorded, not enforced: the operator
		// and the commander share the console credential (D86), and ownership
		// checks use DelegatedByRun/participant identity, never this field.
		CreatedBy: actorOf(r),
	})
	if err != nil {
		if errors.Is(err, store.ErrFactRefMissing) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": "fact_ref_missing"})
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	s.recordAction("intent", in.ID, in.Title)
	if s.premisePolicy() == model.PremiseVerifiedOnly {
		s.SchedulePendingReviews()
	}
	writeJSON(w, 201, in)
}

// maxChatText bounds one operator chat turn. The message service allows 16 KiB;
// the console's compose box is for instructions, not reports, so one bound. It
// is the same bound the instruction object uses: three copies of "4000" with no
// reference between them drift the moment one changes.
const maxChatText = store.MaxInstructionRunes

// chatItem is one executor recipient of a chat turn: the ledger row exists (with
// its delivery state), or the direction was skipped and says why. The console
// renders it as a receipt so a direction that could not receive the sentence
// never looks like a silent success.
type chatItem struct {
	IntentID      string `json:"intent_id"`
	ParticipantID string `json:"participant_id"`
	Title         string `json:"title,omitempty"`
	Status        string `json:"status,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	// Code is the machine-readable reason when the direction was skipped. A
	// full recipient queue is "message_backpressure" so a client retries with
	// the same idempotency key instead of parsing Chinese prose.
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// chat is the operator's side of the project conversation: one sentence typed in
// the 通信 page, addressed either to the commander alone or to the commander and
// every executor that can still receive it. It is the same message service the
// participants use, so the thread, its delivery state and the run journals stay
// one record — a chat turn adds no second channel.
//
//   - commander: one ledger row + deliverToCommander. A running round gets it
//     over IOA, an idle project queues it for the next round's inbox section, a
//     paused project keeps it queued without waking anyone;
//   - all: that row plus one row per direction that is not finished. Running
//     directions get it live; pending/claimed directions carry it in their next
//     run's inbox. Done/failed/parked directions are skipped, not revived: the
//     receipt names them, and reopening one first is the explicit way to include
//     it in a later chat.
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		To             string `json:"to"`
		Text           string `json:"text"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体不是 JSON")
		return
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		to = "commander"
	}
	if to != "commander" && to != "all" {
		writeErr(w, 400, "to 只能是 commander / all")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeErr(w, 400, "消息不能为空")
		return
	}
	if len([]rune(text)) > maxChatText {
		writeErr(w, 400, fmt.Sprintf("消息太长（上限 %d 字）", maxChatText))
		return
	}
	projectID := ""
	if s.Project != nil {
		projectID = s.Project.ID
	}
	idem := func(suffix string) string {
		if req.IdempotencyKey == "" {
			return ""
		}
		return req.IdempotencyKey + ":" + suffix
	}

	commanderMsg, isNew, err := s.Store.AddMessage(model.Message{
		ProjectID:         projectID,
		FromParticipantID: collab.OperatorID,
		ToParticipantID:   collab.CommanderID,
		Kind:              "say",
		Text:              text,
		IdempotencyKey:    idem("commander"),
		Status:            model.MessageQueued,
	})
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, store.ErrMessageBackpressure) {
			s.writeBackpressure(w, collab.CommanderID, err)
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if isNew {
		commanderMsg = s.deliverToCommander(r.Context(), commanderMsg)
	}
	out := map[string]any{
		"to":        to,
		"commander": map[string]any{"message_id": commanderMsg.ID, "status": commanderMsg.Status},
		"delivered": []chatItem{},
		"skipped":   []chatItem{},
	}
	if to == "all" {
		delivered, skipped := s.chatToExecutors(r.Context(), text, projectID, req.IdempotencyKey)
		out["delivered"] = delivered
		out["skipped"] = skipped
	}
	writeJSON(w, http.StatusAccepted, out)
}

// chatToExecutors stores and delivers the executor half of one chat turn. It is
// deliberately the participant message path, not an interjection API: a queued
// row is what a direction that has not started yet will read in its next prompt,
// and a live run gets the same row delivered over IOA.
func (s *Server) chatToExecutors(ctx context.Context, text, projectID, idemBase string) ([]chatItem, []chatItem) {
	delivered := make([]chatItem, 0)
	skipped := make([]chatItem, 0)
	for _, in := range s.Store.ListIntents() {
		item := chatItem{
			IntentID:      in.ID,
			ParticipantID: collab.ParticipantID(in.ID),
			Title:         in.Title,
			Status:        in.Status,
		}
		switch in.Status {
		case model.StatusDone, model.StatusFailed, model.StatusReviewFailed, model.StatusParked:
			item.Reason = "意图已结束/停放：消息不会复活它；要它收到请先 reopen 再发"
			skipped = append(skipped, item)
			continue
		}
		key := ""
		if idemBase != "" {
			key = idemBase + ":" + in.ID
		}
		msg, isNew, err := s.Store.AddMessage(model.Message{
			ProjectID:         projectID,
			FromParticipantID: collab.OperatorID,
			ToParticipantID:   collab.ParticipantID(in.ID),
			ToGeneration:      in.Attempts,
			Kind:              "say",
			Text:              text,
			IdempotencyKey:    key,
			Status:            model.MessageQueued,
		})
		if err != nil {
			item.Reason = err.Error()
			if errors.Is(err, store.ErrMessageBackpressure) {
				item.Code = "message_backpressure"
			}
			skipped = append(skipped, item)
			continue
		}
		if isNew {
			msg = s.deliverMessage(ctx, in, msg)
		}
		item.MessageID = msg.ID
		item.Status = msg.Status
		delivered = append(delivered, item)
	}
	return delivered, skipped
}
func (s *Server) listIntents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := retrieval.Intents(s.Store, retrieval.IntentQuery{Status: r.URL.Query().Get("status"), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		retrievalError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) getIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := s.Store.GetIntent(id)
	if !ok {
		writeErr(w, 404, "intent not found")
		return
	}
	page, err := retrieval.Facts(s.Store, retrieval.FactQuery{Session: harnessSession(r), Intent: in.ID, Limit: 200})
	if err != nil {
		retrievalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"intent": in, "facts": page.Facts, "facts_total": page.Total, "facts_has_more": page.HasMore, "facts_next_cursor": page.NextCursor, "facts_query": "/api/facts?intent=" + in.ID})
}

func (s *Server) runIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if msg, blocked := s.operatorHoldRefusal(r, id); blocked {
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "code": "held_by_operator"})
		return
	}
	runID, err := s.StartRun(id)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"run_id": runID, "intent_id": id})
}

// StartRun claims an intent and dispatches it. It is exported because the
// decider uses it to start the work it just created.
func (s *Server) StartRun(id string) (string, error) {
	// Administrative interruption keeps the task alive. Prefer its preserved
	// native session; an explicit retry with resume=false still opens a fresh one.
	if in, ok := s.Store.GetIntent(id); ok && in.Status == model.StatusPending && in.RunID != "" {
		if previous, found := s.Store.GetRun(in.RunID); found && previous.Status == model.RunCanceled && (previous.CancelCause == model.CancelCausePause || previous.CancelCause == model.CancelCauseRestart) {
			work := filepath.Join(s.CaseRoot, in.ID)
			nodeID, placementErr := s.resolveNode(in.Node)
			if placementErr != nil {
				return "", placementErr
			}
			if executor.CanResume(s.Exec, nodeID, work) {
				prompt := s.continuePrompt(in, previous, "项目暂停或进程重启后恢复；按当前有效指令核对断点，再继续未完成工作", s.readWorkerNote(work))
				return s.startRunWith(id, prompt, previous.ID, true, true)
			}
			newRun, err := s.startRun(id, "", "")
			if err == nil {
				_, _ = s.Store.UpdateRun(newRun, func(r *model.Run) {
					r.Infos = append(r.Infos, "暂停恢复：原 harness 会话不可用，本轮以账本、工作区和 worker-note 重建会话")
				})
			}
			return newRun, err
		}
	}
	return s.startRun(id, "", "")
}

// SetPaused arms/disarms the project-level dispatch gate. The decider's own
// pause flag only covers the moment a round starts; a round already in flight
// must not be able to dispatch after the operator paused the project.
func (s *Server) SetPaused(paused bool) {
	// The lifecycle service owns what the pause means; bare test servers built
	// without New still write the same atomic directly.
	if s.Lifecycle != nil {
		s.Lifecycle.SetPaused(paused)
		return
	}
	s.paused.Store(paused)
}

// SetDeleting closes dispatch for a project that is being removed. It is set
// before CancelAll and cleared only when deletion is aborted.
func (s *Server) SetDeleting(deleting bool) {
	if s.Lifecycle != nil {
		s.Lifecycle.SetDeleting(deleting)
		return
	}
	s.deleting.Store(deleting)
}

// Deleting reports whether the project is inside its delete window: a settings
// write racing the removal must fail closed (409) instead of persisting into a
// tree the delete is about to remove.
func (s *Server) Deleting() bool {
	if s.Lifecycle != nil {
		return s.Lifecycle.Deleting()
	}
	return s.deleting.Load()
}

// startRun is the single dispatch path: a fresh brief, or — when prompt is not
// empty — a continued turn whose prompt is already built (D42).
func (s *Server) startRun(id, prompt, continuedFrom string) (string, error) {
	return s.startRunWith(id, prompt, continuedFrom, prompt != "", prompt != "")
}

// startRunWith is startRun with the prompt/resume decision explicit. A wake can
// be a fresh run with a control-plane-built prompt (after the old session is
// gone) or a continuation of the same trajectory; deriving resume from
// "prompt != \"\"" would force the first case into a resume that fails.
func (s *Server) startRunWith(id, prompt, continuedFrom string, resume, keepPrompt bool) (string, error) {
	return s.startRunOwned(id, prompt, continuedFrom, resume, keepPrompt, "")
}

// planRun resolves the execution location and model binding for an admitted
// direction and returns the run row whose frozen versions the CAS claim will
// persist. It is the API's half of Start: routing reads the node registry and
// the shared model connection, while admission, the claim and the launch live in
// the lifecycle service.
func (s *Server) planRun(in model.Intent, continuedFrom string) (model.Run, string, error) {
	nodeID, err := s.resolveNode(in.Node)
	if err != nil {
		return model.Run{}, "", err
	}
	run := model.Run{IntentID: in.ID, ContinuedFrom: continuedFrom, Node: nodeID, ContextWindow: s.ModelContext()}
	if s.ModelSettings != nil {
		run.Provider, run.Model, run.ContextWindow = s.ModelSettings()
	}
	if na, ok := s.Exec.(executor.NodeAware); ok {
		for _, n := range na.Nodes() {
			if n.ID == nodeID && n.Model != "" {
				run.Model = n.Model
			}
		}
	}
	if nodeID == s.LocalNode {
		if err := s.CheckLocalHost(); err != nil {
			return model.Run{}, "", err
		}
	}
	return run, nodeID, nil
}

func (s *Server) startRunOwned(id, prompt, continuedFrom string, resume, keepPrompt bool, owner string) (string, error) {
	// Admission, the CAS claim, the cancel handle and the dispatch goroutine are
	// owned by the lifecycle service; this wrapper keeps the API's start helpers
	// (and their tests) unchanged.
	return s.Lifecycle.Start(id, prompt, continuedFrom, resume, keepPrompt, owner)
}

// blockedPremises lists from_facts that no longer stand: gone, or rejected by a
// review. They are why a queued direction cannot run.
func (s *Server) blockedPremises(in model.Intent) []string {
	policy := s.premisePolicy()
	var out []string
	for _, id := range in.FromFactIDs {
		f, ok := s.Store.FactByID(id)
		switch {
		case !ok:
			out = append(out, id+"（不存在）")
		case model.PremiseIssue(f, policy) != "":
			if issue := s.premiseIssue(f, s.ReviewsDisabled()); issue != "" {
				out = append(out, id+"（"+issue+"）")
			}
		}
	}
	return out
}

// ErrNoSession means the case has no trajectory to continue. Callers fall back
// to cancel + redispatch (D16's degrade path); it is not a failure.
var ErrNoSession = errors.New("意图没有可续的轨迹（没有 session.aop.jsonl，也没有 AOP 会话）")

// Say errors. A say is not a steer: it never cancels the run, so "no live
// session" is final for this attempt and must not silently degrade into a
// redispatch (D69).
var (
	ErrNoSaySession   = errors.New("意图没有可插话的 AOP 会话（还没跑或已经结束）")
	ErrSayUnavailable = errors.New("插话通道未配置：宿主没有可用的 IOA")
	ErrSayNotRunning  = errors.New("插话只能发给正在跑的 run")
	ErrSayInvalid     = errors.New("插话请求不合法")
	ErrSayNotFound    = errors.New("意图不存在")
)

// steerIntent is the operator's version of the decider's steer action: it is
// the same call, so "what the model can do, a human can do" (D42).
func (s *Server) steerIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Note    string `json:"note"`
		RefFact string `json:"ref_fact"`
	}
	if !decodeOptionalJSON(w, r.Body, &req) {
		return
	}
	runID, err := s.SteerIntent(id, req.Note, req.RefFact)
	if errors.Is(err, ErrNoSession) {
		writeErr(w, 409, err.Error()+"；这一轮被中止时什么都没留下，重派即可")
		return
	}
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	s.recordAction("steer", id, req.Note)
	writeJSON(w, 202, map[string]any{"run_id": runID, "intent_id": id, "resumed": true})
}

// sayIntent is the operator's mid-flight message. It does not cancel the run:
// the message goes through the host's IOA bridge into the harness inbox and is
// drained by the same run (D69).
func (s *Server) sayIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Text           string `json:"text"`
		RefFact        string `json:"ref_fact"`
		Interrupt      bool   `json:"interrupt"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体不是 JSON")
		return
	}
	msgID, err := s.sayFrom(r.Context(), id, req.Text, req.RefFact, req.Interrupt, actorOf(r), req.IdempotencyKey)
	if err != nil {
		switch {
		case errors.Is(err, ErrSayInvalid):
			writeErr(w, 400, err.Error())
		case errors.Is(err, ErrSayNotFound):
			writeErr(w, 404, err.Error())
		case errors.Is(err, ErrNoSaySession), errors.Is(err, ErrSayNotRunning):
			writeErr(w, 409, err.Error())
		case errors.Is(err, ErrSayUnavailable):
			writeErr(w, 503, err.Error())
		case errors.Is(err, store.ErrMessageBackpressure):
			s.writeBackpressure(w, collab.ParticipantID(id), err)
		default:
			writeErr(w, 500, err.Error())
		}
		return
	}
	s.recordAction("say", id, req.Text)
	status := model.MessageAccepted
	if m, ok := s.Store.MessageByID(msgID); ok {
		status = m.Status
	}
	writeJSON(w, 202, map[string]any{
		"message_id": msgID,
		"intent_id":  id,
		"delivery":   status,
		"accepted":   status != model.MessageQueued,
	})
}

// SayIntent injects text into an intent that is currently running. It is the
// message service's live-only mode, not a separate history: the sentence is
// stored as one Message row and delivered through the same path as a peer
// message (D86). A successful return means the row exists; its Status says
// whether IOA took it ("accepted") or it is still "queued" for the next run.
func (s *Server) SayIntent(ctx context.Context, id, text, refFact string, interrupt bool) (string, error) {
	return s.sayFrom(ctx, id, text, refFact, interrupt, collab.OperatorID, "")
}

// sayFrom is SayIntent with the sender and idempotency key explicit: a say from
// the commander is the same row shape as one from the operator, and a retry with
// a key must not deliver twice.
func (s *Server) sayFrom(ctx context.Context, id, text, refFact string, interrupt bool, from, idemKey string) (string, error) {
	in, ok := s.Store.GetIntent(id)
	if !ok {
		return "", fmt.Errorf("%w：%s", ErrSayNotFound, id)
	}
	if in.Status != model.StatusRunning && in.Status != model.StatusClaimed {
		return "", fmt.Errorf("%w：%s 当前 %s", ErrSayNotRunning, id, in.Status)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%w：插话必须带内容", ErrSayInvalid)
	}
	if len([]rune(text)) > 500 {
		return "", fmt.Errorf("%w：插话超过 500 字", ErrSayInvalid)
	}
	if s.Say == nil && s.SayEvent == nil {
		return "", ErrSayUnavailable
	}
	if in.RunID == "" {
		return "", ErrNoSaySession
	}
	if r, ok := s.Store.GetRun(in.RunID); !ok || r.Status != model.RunRunning {
		return "", ErrNoSaySession
	}
	refFact = strings.TrimSpace(refFact)
	var refs []string
	if refFact != "" {
		if _, ok := s.Store.FactByID(refFact); !ok {
			return "", fmt.Errorf("%w：引用的事实不存在 %s", ErrSayInvalid, refFact)
		}
		refs = []string{refFact}
	}
	if _, ok := aopexec.ReadSession(filepath.Join(s.CaseRoot, in.ID)); !ok {
		return "", ErrNoSaySession
	}
	projectID := ""
	if s.Project != nil {
		projectID = s.Project.ID
	}
	msg, isNew, err := s.Store.AddMessage(model.Message{
		ProjectID:         projectID,
		FromParticipantID: from,
		ToParticipantID:   collab.ParticipantID(id),
		ToGeneration:      in.Attempts,
		Kind:              "say",
		Text:              text,
		FactRefs:          refs,
		Interrupt:         interrupt,
		IdempotencyKey:    idemKey,
		Status:            model.MessageQueued,
	})
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			return "", fmt.Errorf("%w：%s", ErrSayInvalid, err.Error())
		}
		return "", err
	}
	if !isNew {
		// A retry of an accepted say must not deliver the same sentence twice.
		return msg.ID, nil
	}
	delivered := s.deliverMessage(ctx, in, msg)
	if delivered.Status == model.MessageUnknown {
		return delivered.ID, fmt.Errorf("消息已记录（%s），但投递结果未知：%s", delivered.ID, delivered.Error)
	}
	if _, err := s.Store.UpdateIntent(id, func(i *model.Intent) { i.SayCount++ }); err != nil {
		log.Printf("say: 消息 %s 已投递，但计数失败：%v", delivered.ID, err)
		return delivered.ID, fmt.Errorf("消息已投递，但台账记录失败：%w", err)
	}
	return delivered.ID, nil
}

// SteerIntent stops the turn that is running on an intent and starts a
// continuation of it from the case's trajectory (D42).
//
// The note is the *whole* prompt of the new turn. Everything else — the brief,
// the file it read, the dead end it already walked — comes back from the
// trajectory, which is the point: a steered turn costs a note, not a re-brief.
func (s *Server) SteerIntent(id, note, refFact string) (string, error) {
	in, ok := s.Store.GetIntent(id)
	if !ok {
		return "", fmt.Errorf("intent %s 不存在", id)
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return "", errors.New("续话必须带指示：没有指示的续话等于重派")
	}
	if in.Status != model.StatusRunning && in.Status != model.StatusClaimed {
		return "", fmt.Errorf("意图 %s 没在运行（%s），续话无处可去", id, in.Status)
	}
	// Count continuations for observation; it does not impose a fee limit.
	if _, err := s.Store.UpdateIntent(id, func(i *model.Intent) { i.SteerCount++ }); err != nil {
		return "", err
	}
	// The trajectory is what the run leaves *when it ends*: the merged file is
	// rebuilt as the process exits. So the kill has to happen before the check —
	// asking first would answer "no trajectory" on every first turn.
	prevRun := in.RunID
	if prevRun != "" {
		_ = s.CancelRunWithCause(prevRun, model.CancelCauseSteer, "转续话（steer）")
	}
	if err := s.waitStatus(id, model.StatusPending, 30*time.Second); err != nil {
		return "", err
	}
	// Whether the case left something to continue is the transport's answer:
	// a host keeps a session for it, audit has nothing to continue (D42/D43).
	// Ask the executor that would run the next turn.
	if !executor.CanResume(s.Exec, in.Node, filepath.Join(s.CaseRoot, in.ID)) {
		return "", ErrNoSession
	}
	return s.startRun(id, steerPrompt(in, note, refFact), prevRun)
}

// waitStatus blocks until the intent reaches want, or the deadline passes. A
// kill is asynchronous by nature: the process may still be flushing a segment.
func (s *Server) waitStatus(id, want string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		in, ok := s.Store.GetIntent(id)
		if !ok {
			return fmt.Errorf("intent %s 在等待中消失了", id)
		}
		if in.Status == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待意图 %s 回到 %s 超时（当前 %s）", id, want, in.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// steerPrompt is deliberately short: the harness already holds the brief, the
// tool results and its own reasoning. Only what changed is sent, plus the one
// thing a resumed run can get wrong — declaring the same facts twice.
func steerPrompt(in model.Intent, note, refFact string) string {
	var b strings.Builder
	b.WriteString("## 续话：上一轮已被中止\n\n")
	b.WriteString("意图：" + in.Title + "\n\n")
	b.WriteString("你之前在这个意图上的工作仍然有效，上下文与工具结果都已恢复。现在按下面的指示继续，")
	b.WriteString("不要重做已经完成的部分。\n\n")
	b.WriteString(note)
	if refFact != "" {
		b.WriteString("\n\n依据事实：" + refFact)
	}
	b.WriteString("\n\n运行中仍按原交接契约及时追加 `.tw/facts.jsonl`；上一轮已经记过的事实不要重复声明。")
	return b.String()
}

// Constraints is the project constraint document, verbatim. Executors and
// auxiliary runs get this text and nothing about where it comes from: the
// document is re-read every round by the mechanism, not explained in a prompt.
func (s *Server) Constraints() string {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return strings.TrimSpace(s.Description)
}

// CommanderDocs is the text a commander round carries before the board: the
// constraint document, the starting point and the goal, each verbatim under its
// own heading. The goal is also in board.goals, which is a known redundancy: the
// documents are the base the prompt is built from, so they are injected whole.
func (s *Server) CommanderDocs() string {
	s.settingsMu.RLock()
	scope, desc := strings.TrimSpace(s.Scope), strings.TrimSpace(s.Description)
	s.settingsMu.RUnlock()
	var b strings.Builder
	if desc != "" {
		b.WriteString("## 约束\n" + desc + "\n\n")
	}
	if scope != "" {
		b.WriteString("## 起点\n" + scope + "\n\n")
	}
	if s.Store != nil {
		if g, ok := s.currentGoalDocument(); ok && strings.TrimSpace(g.Statement) != "" {
			b.WriteString("## 目标\n" + strings.TrimSpace(g.Statement) + "\n\n")
		}
	}
	return b.String()
}

// SetProjectSettings publishes a complete runtime snapshot after persistence.
func (s *Server) SetProjectSettings(scope, description string) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	s.Scope, s.Description = scope, description

}

func (s *Server) scopeText() string {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return strings.TrimSpace(s.Scope)
}

// maxRunsLimit is the cap this project actually enforces: its own setting when
// the manifest carries one, otherwise the instance default from --max-runs. The
// console edits it per project, so it is read at dispatch time, not cached.
func (s *Server) maxRunsLimit() int {
	if s.Project != nil {
		if v, ok := s.Project.MaxRunsOverride(); ok {
			return v
		}
	}
	return s.MaxRuns
}

// runningIntentCount is the concurrency the cap applies to: verifier/report
// runs are purpose runs and must not block an attack direction.
func (s *Server) runningIntentCount() int {
	n := 0
	for _, r := range s.Store.Runs() {
		if r.Status == model.RunRunning && r.Purpose == "" {
			n++
		}
	}
	return n
}

// RunningIntentCount is runningIntentCount for the hub's instance-wide
// --max-workers accounting: it must use the same definition as the per-project
// cap, or the two caps would disagree about what a worker is.
func (s *Server) RunningIntentCount() int { return s.runningIntentCount() }

// HasNode reports whether the executor knows this node id. An executor without
// a registry has exactly one location, so only "local" (or "") makes sense.
func (s *Server) HasNode(id string) bool {
	if na, ok := s.Exec.(executor.NodeAware); ok {
		for _, n := range na.Nodes() {
			if n.ID == id {
				return true
			}
		}
		return false
	}
	return id == "local"
}

func (s *Server) nodeIDs() []string {
	if na, ok := s.Exec.(executor.NodeAware); ok {
		out := make([]string, 0, len(na.Nodes()))
		for _, n := range na.Nodes() {
			out = append(out, n.ID)
		}
		return out
	}
	return []string{"local"}
}

func (s *Server) parkIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSON(w, r.Body, &req) {
		return
	}
	in, err := s.ParkIntent(id, req.Reason)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	s.recordAction("park", id, req.Reason)
	writeJSON(w, 200, in)
}

// ParkIntent sets a direction aside. It refuses a live run: writing parked now
// would be overwritten by finish() when the run ends, and the operator's
// decision would silently disappear. Cancel first, then park.
func (s *Server) ParkIntent(id, reason string) (model.Intent, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return model.Intent{}, errors.New("停放必须写理由：没有理由的停放等于丢工作")
	}
	in, ok := s.Store.GetIntent(id)
	if !ok {
		return model.Intent{}, fmt.Errorf("意图 %s 不存在", id)
	}
	if in.Status == model.StatusRunning || in.Status == model.StatusClaimed {
		return model.Intent{}, errors.New("意图正在运行：先 cancel，等它回到 pending，再 park")
	}
	// Snapshot the premises that are not usable now. The park reason is prose
	// and goes stale when a review later passes; this snapshot is the machine
	// input for the "premise is ready now" notice and the commander attention.
	blocked, claims := s.parkedBlockedPremises(in)
	parkedAt := time.Now()
	updated, applied, err := s.Store.UpdateIntentIfCurrent(id, in.Status, in.RunID, func(i *model.Intent) {
		i.Status = model.StatusParked
		i.ParkReason = reason
		i.Hold = true
		i.Wait = nil
		i.ParkedPremiseFactIDs = blocked
		i.ParkedPremiseClaims = claims
		i.ParkedAt = &parkedAt
	})
	if err != nil {
		return model.Intent{}, err
	}
	if !applied {
		// A dispatch/retry took the direction between the read above and this
		// write. Writing parked anyway would be overwritten by finish() and the
		// operator would never learn the park did not take.
		return model.Intent{}, fmt.Errorf("意图 %s 已被派发或结算：停放没有生效，刷新后再决定", id)
	}
	// A parked direction will not consume its children's results, and parking
	// does not go through the scheduler, so settle the unstarted ones here.
	s.failUnstartedChildren(updated)
	return updated, nil
}

// parkedBlockedPremises is the park-time snapshot of unready hard premises.
// It deliberately skips byte verification: the point is which assertions were
// blocked, not re-hashing every original file on a park click. The dispatch
// gate still performs the full readiness check before a run.
func (s *Server) parkedBlockedPremises(in model.Intent) ([]string, map[string]uint64) {
	if s.Store == nil || len(in.FromFactIDs) == 0 {
		return nil, nil
	}
	ready := s.readiness(s.Store.Snapshot(), in, s.premisePolicy(), false)
	var ids []string
	claims := map[string]uint64{}
	for _, p := range ready.Premises {
		if p.Usable {
			continue
		}
		ids = append(ids, p.ID)
		claims[p.ID] = p.ClaimVersion
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return ids, claims
}

func (s *Server) reopenIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, err := s.ReopenIntent(id)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, in)
}

// ReopenIntent brings a parked direction back. It must not race the old run: if
// the previous run is still live, reopening would make a second run possible on
// the same intent (the invariant is one run per intent).
func (s *Server) ReopenIntent(id string) (model.Intent, error) {
	in, ok := s.Store.GetIntent(id)
	if !ok {
		return model.Intent{}, fmt.Errorf("意图 %s 不存在", id)
	}
	if in.Status != model.StatusParked {
		return model.Intent{}, fmt.Errorf("意图 %s 不是停放状态（%s）", id, in.Status)
	}
	if in.RunID != "" {
		if r, ok := s.Store.GetRun(in.RunID); ok && r.Status == model.RunRunning {
			return model.Intent{}, errors.New("上一轮还在运行：先 cancel，再 reopen")
		}
	}
	return s.Store.ReopenIntent(id)
}

// cancelIntent is the sticky stop. The actor is derived from the authenticated
// surface, not from a request field: an operator token is the operator, and a
// harness session on the project mux can only be the commander (executors have
// no write route there). HeldBy is what later decides whether a restart may
// override this stop.
func (s *Server) cancelIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSON(w, r.Body, &req) {
		return
	}
	actor := model.HoldActorOperator
	if harnessSession(r) {
		actor = model.HoldActorCommander
	}
	runID, err := s.CancelIntentBy(id, req.Reason, actor)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	s.recordAction("cancel", id, req.Reason)
	writeJSON(w, 202, map[string]any{"intent_id": id, "run_id": runID, "held": true, "held_by": actor})
}

// CancelIntent is the sticky stop without a known actor (internal callers and
// tests). The HTTP surface must use CancelIntentBy so the hold records who made
// the decision.
func (s *Server) CancelIntent(id, reason string) (string, error) {
	return s.CancelIntentBy(id, reason, model.HoldActorUnknown)
}

// CancelIntentBy is the sticky stop: it holds the direction before stopping the
// run, so finish()'s "canceled -> pending" (D19) cannot be picked up by the
// next dispatchPending. An explicit retry/run is what clears Hold.
func (s *Server) CancelIntentBy(id, reason, actor string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", errors.New("取消必须写理由：没有理由的停止等于丢工作")
	}
	if actor == "" {
		actor = model.HoldActorUnknown
	}
	in, ok := s.Store.GetIntent(id)
	if !ok {
		return "", fmt.Errorf("意图 %s 不存在", id)
	}
	if in.Status == model.StatusDone || in.Status == model.StatusParked || in.Status == model.StatusFailed || in.Status == model.StatusReviewFailed {
		return "", fmt.Errorf("意图 %s 已经是 %s：用 retry 或 park，不要 cancel", id, in.Status)
	}
	if _, err := s.Store.UpdateIntent(id, func(i *model.Intent) {
		i.Hold = true
		i.HeldBy = actor
		i.HoldReason = reason
		i.Wait = nil // a cancelled direction is not waiting for anybody
		if i.Status == model.StatusWaiting {
			// Waiting has no run to cancel; failed+Hold is the shape an explicit
			// retry later picks up. It says "stopped by a human", not "the work
			// failed on its own", and the hold stops the pump from re-opening it.
			i.Status = model.StatusFailed
		}
		if i.Status == model.StatusReviewing {
			// The review phase must not publish done/failed over an explicit
			// operator stop. Its final CAS sees a non-reviewing status and backs
			// off; the original run already settled successfully, so this is a
			// human-held failure, not a harness failure.
			i.Status = model.StatusFailed
		}
	}); err != nil {
		return "", err
	}
	// The scheduler may have claimed the direction between the read above and
	// this hold. Decide what to stop from the row as it is now: the old snapshot
	// said "pending, nothing to cancel", so a cancel that raced dispatch reported
	// success and left the run it had just started going.
	cur, ok := s.Store.GetIntent(id)
	if !ok {
		return "", fmt.Errorf("意图 %s 不存在", id)
	}
	// Canceling a direction cancels its whole subtree: a child writing into a
	// case nobody watches is exactly the orphan this operation must not leave.
	s.cancelDescendants(id, reason, actor)
	if cur.Status == model.StatusRunning || cur.Status == model.StatusClaimed {
		if cur.RunID != "" {
			// The console labels this stop "止损取消" from the structured cause,
			// so the reason only needs the prefix when the caller did not already
			// write one. Stacking it produced "止损：止损：…" in run.exit_reason.
			stopReason := reason
			if !strings.HasPrefix(stopReason, "止损：") {
				stopReason = "止损：" + stopReason
			}
			if err := s.CancelRunWithCause(cur.RunID, model.CancelCauseStopLoss, stopReason); err != nil {
				// The run may have finished between the status read and the
				// cancel; Hold is already set, so there is nothing left to stop.
				if after, ok := s.Store.GetIntent(id); !ok || (after.Status != model.StatusRunning && after.Status != model.StatusClaimed) {
					return cur.RunID, nil
				}
				return "", err
			}
		}
		return cur.RunID, nil
	}
	// pending / failed: there is no run to stop; holding it is the whole effect.
	return "", nil
}

// retryError carries a machine-readable reason for a refused retry. The console
// reacts to the code: resume_unavailable must switch the dialog to "重派新会话";
// retry_conflict means the direction cannot be retried at all right now.
type retryError struct {
	code string
	msg  string
}

func (e *retryError) Error() string { return e.msg }

// operatorHoldRefusal refuses a commander's restart-like write on a direction
// the operator stopped. The commander's own stops, automatic liveness stops and
// legacy rows are not blocked here; an operator stop is a human decision that a
// retry must not silently undo (the operator may retry, or the commander may
// register an operator request first). The operator path is exempt.
func (s *Server) operatorHoldRefusal(r *http.Request, intentID string) (string, bool) {
	if !harnessSession(r) {
		return "", false
	}
	in, ok := s.Store.GetIntent(intentID)
	if !ok || !in.Hold || in.HeldBy != model.HoldActorOperator {
		return "", false
	}
	return "意图已由操作员停止：请让操作员重试，或先发 operator request 说明为什么需要再跑", true
}

func (s *Server) retryIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Note   string `json:"note"`
		Resume bool   `json:"resume"`
	}
	if !decodeOptionalJSON(w, r.Body, &req) {
		return
	}
	if msg, blocked := s.operatorHoldRefusal(r, id); blocked {
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "code": "held_by_operator"})
		return
	}
	runID, redelivered, err := s.retryIntentRun(id, req.Note, req.Resume)
	if err != nil {
		var re *retryError
		if errors.As(err, &re) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": re.msg, "code": re.code})
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAction("retry", id, req.Note)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"intent_id": id, "run_id": runID, "resume": req.Resume, "redelivered": redelivered,
	})
}

// retryPreview answers the console's retry dialog before the operator commits:
// can this direction continue its old harness session, and how many messages
// would a retry have to carry again? It is counts plus the marker's session id,
// never message bodies or credentials.
func (s *Server) retryPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := s.Store.GetIntent(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "意图不存在")
		return
	}
	counts := s.Store.RedeliveryCounts(collab.ParticipantID(id))
	out := map[string]any{
		"intent_id":             in.ID,
		"status":                in.Status,
		"can_resume":            false,
		"resume_error":          "",
		"resume_session":        "",
		"resume_redeliver":      counts.Resume,
		"new_session_redeliver": counts.Fresh,
	}
	switch {
	case in.Status != model.StatusFailed && in.Status != model.StatusPending && in.Status != model.StatusReviewFailed:
		out["resume_error"] = "当前状态 " + in.Status + " 不能重试"
	case in.RunID == "":
		out["resume_error"] = "没有可续的上一次 run"
	default:
		run, found := s.Store.GetRun(in.RunID)
		sess, hasSession := aopexec.ReadSession(filepath.Join(s.CaseRoot, id))
		switch {
		case !found:
			out["resume_error"] = "上一次 run 记录不存在"
		case run.Status != model.RunFailed && run.Status != model.RunCanceled:
			out["resume_error"] = "上一次 run 状态是 " + run.Status + "，没有可续的失败轨迹"
		case !hasSession:
			out["resume_error"] = "原 harness 会话已不存在（宿主重启或会话被清理）"
		default:
			out["can_resume"] = true
			out["resume_session"] = sess.Session
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// RetryIntent re-dispatches a failed or held direction. It is the explicit
// "walk this path again" that replaces automatic retry. resume=true continues
// the previous harness session (its brief, tool results and reasoning are still
// there) instead of starting a fresh one; the prompt then carries only the
// structured failure of the last run and what changed.
func (s *Server) RetryIntent(id, note string, resume bool) (string, error) {
	runID, _, err := s.retryIntentRun(id, note, resume)
	return runID, err
}

func (s *Server) retryIntentRun(id, note string, resume bool) (string, int, error) {

	in, ok := s.Store.GetIntent(id)
	if !ok {
		return "", 0, &retryError{"retry_conflict", fmt.Sprintf("意图 %s 不存在", id)}
	}
	if in.Status != model.StatusFailed && in.Status != model.StatusPending && in.Status != model.StatusReviewFailed {
		return "", 0, &retryError{"retry_conflict",
			fmt.Sprintf("意图 %s 是 %s，只有 failed / review_failed / pending 的意图可以 retry", id, in.Status)}
	}
	// The note is the decider's commentary for the operator and the next
	// decision, not an executor constraint. It goes into Intent.Notes, which
	// BuildPrompt never reads; appending it to Doc put the note in the
	// executor's constraint section and made the prompt strip headings by name.
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return "", 0, &retryError{"retry_refused", "retry 的说明超过 500 字：备注要短"}
	}
	// A resume is only possible when the previous attempt really left a session
	// the host can continue. Refusing here is the explicit half of "retry with
	// memory": a silent fresh session would drop exactly the context the
	// operator asked to keep.
	var prev model.Run
	if resume {
		if in.RunID != "" {
			prev, _ = s.Store.GetRun(in.RunID)
		}
		switch {
		case prev.ID == "":
			return "", 0, &retryError{"resume_unavailable", "没有可续的上一次 run：请改选「重派新会话」"}
		case prev.Status != model.RunFailed && prev.Status != model.RunCanceled:
			return "", 0, &retryError{"resume_unavailable",
				fmt.Sprintf("上一次 run 是 %s，没有可续的失败轨迹：请改选「重派新会话」", prev.Status)}
		case !aopexec.CanResume(filepath.Join(s.CaseRoot, id)):
			return "", 0, &retryError{"resume_unavailable",
				"原 harness 会话已不存在（宿主重启或会话被清理）：请改选「重派新会话」"}
		}
	}
	_, prepared, err := s.Store.UpdateIntentIfCurrent(id, in.Status, in.RunID, func(i *model.Intent) {
		if note != "" {
			i.Notes = append(i.Notes, model.IntentNote{
				At:   time.Now(),
				Kind: model.IntentNoteRetry,
				Text: note,
			})
		}
		i.Status = model.StatusPending
		i.Hold = true // Reserve preparation for this explicit retry, not the pump.
		i.ClaimedBy = ""
		// A retry is a fresh judgement; the old park snapshot is no longer a
		// statement about this attempt.
		i.ParkReason = ""
		i.ParkedPremiseFactIDs = nil
		i.ParkedPremiseClaims = nil
		i.ParkedAt = nil
	})
	if err != nil {
		return "", 0, err
	}
	if !prepared {
		return "", 0, &retryError{"retry_conflict", "意图已由其他请求接管，请刷新后查看当前运行"}
	}
	if resume {
		prompt := s.retryResumePrompt(in, prev, note)
		runID, startErr := s.startRunWith(id, prompt, prev.ID, true, true)
		if startErr != nil {
			// Do not leave a pending direction behind: the pump would turn an
			// explicit resume into a silent fresh dispatch. Only undo the
			// transition this call made: a concurrent retry/continue may have
			// claimed the direction while this one was starting, and
			// overwriting that would show a live run as failed (and a later
			// retry could then claim it again as a second run).
			s.rollbackRetryReserve(in)
			return "", 0, &retryError{"retry_refused", "续跑未能启动：" + startErr.Error()}
		}
		counts := s.Store.RedeliveryCounts(collab.ParticipantID(id))
		return runID, counts.Resume, nil
	}
	// A fresh session has no conversation memory at all. The failed run already
	// put its possibly-unread messages back (finish), but a message carried by
	// an older run of this direction is not in the new session either, so the
	// whole unconfirmed tail is requeued here and rides the new brief.
	redelivered := 0
	if n, err := s.Store.RedeliverAllUnconfirmed(collab.ParticipantID(id)); err != nil {
		log.Printf("retry %s: 重投未确认消息失败：%v", id, err)
	} else {
		redelivered = n
	}
	runID, err := s.startRunWith(id, "", "", false, false)
	if err != nil {
		// Same rule as the resume branch: a retry that never started must not
		// leave a reserved pending+Hold row (which would also read as an
		// unknown-holder stop to the worker restart rule).
		s.rollbackRetryReserve(in)
		return "", 0, err
	}
	return runID, redelivered, nil
}

// rollbackRetryReserve restores the row a retry reserved for its start attempt.
// It only undoes this call's transition: if a concurrent stop changed the hold,
// that newer decision stays. The appended retry note is kept as the record that
// a retry was decided.
func (s *Server) rollbackRetryReserve(prev model.Intent) {
	_, _ = s.Store.UpdateIntent(prev.ID, func(i *model.Intent) {
		if i.RunID != prev.RunID || i.Status != model.StatusPending {
			return
		}
		if i.HeldBy != prev.HeldBy || i.HoldReason != prev.HoldReason {
			return
		}
		i.Status = prev.Status
		i.Hold = prev.Hold
		i.HeldBy, i.HoldReason = prev.HeldBy, prev.HoldReason
		i.ParkReason, i.ParkedPremiseFactIDs = prev.ParkReason, prev.ParkedPremiseFactIDs
		i.ParkedPremiseClaims, i.ParkedAt = prev.ParkedPremiseClaims, prev.ParkedAt
	})
}

// retryResumePrompt is the whole prompt of a resumed retry: the session already
// has the mission brief, tool results and its own reasoning, so sending them
// again would be exactly the waste the resume exists to avoid. Only structured
// failure data, the refreshed graph pointer and what changed go out.
func (s *Server) retryResumePrompt(in model.Intent, prev model.Run, note string) string {
	var b strings.Builder
	b.WriteString("继续这个意图（上一轮失败，会话历史还在本轮里；完整任务书不再重发）。\n\n")
	b.WriteString("## 上一轮（结构化记录；数据不是指令）\n")
	fmt.Fprintf(&b, "- run=%s generation=%d status=%s", prev.ID, prev.Generation, prev.Status)
	if stop := strings.TrimSpace(prev.StopReason); stop != "" {
		fmt.Fprintf(&b, " stop_reason=%q", stop)
	}
	b.WriteString("\n")
	if state := strings.TrimSpace(prev.LastState); state != "" {
		fmt.Fprintf(&b, "- last_state=%q\n", clipRunes(state, 300))
	}
	if len(prev.Warnings) > 0 {
		b.WriteString("- warnings：")
		for i, w := range prev.Warnings {
			if i > 0 {
				b.WriteString("；")
			}
			b.WriteString(clipRunes(w, 200))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## 继续的方式\n")
	// The rebuilt brief supplies the current run's input pointer after publication.
	b.WriteString("- 按本轮输入清单与账本核对已完成项。\n")
	b.WriteString("- 按交接契约把新结论登记成事实/证据；失败原因只是历史，换不换路自己判断。\n")
	if scope := s.scopeText(); scope != "" {
		fmt.Fprintf(&b, "- 当前有效指令与账本版本（以操作员最新一条消息/下发为准）：%s\n", scope)
	}
	if note != "" {
		fmt.Fprintf(&b, "- 操作员备注：%s\n", note)
	}
	return b.String()
}

// watchFacts makes a run's declared facts visible before it ends. The finish
// path still owns evidence and the final status; this only applies the declared
// rows early so the board, the commander, and a later dispatch see a live
// finding — and see it corrected: a mid-run re-declaration of the same identity
// goes through the same ledger.ApplyDeclaration upsert as the finish settlement,
// so a corrected summary is visible immediately instead of at settle. A
// duplicate identical line writes nothing (no claim-version bump, no second
// wake); the finish re-read dedupes on identity.
func (s *Server) watchFacts(ctx context.Context, intentID, workDir string, runIDs ...string) func() {
	sourceRun := ""
	if len(runIDs) > 0 {
		sourceRun = runIDs[0]
	}
	var mu sync.Mutex
	offset := ledger.Consumed(workDir)
	read := func() {
		mu.Lock()
		defer mu.Unlock()
		next, err := s.readLiveDeclarations(intentID, workDir, sourceRun, offset)
		if err != nil {
			// Do not advance the cursor: the next tick re-reads the same range.
			// Rows already committed by this pass dedupe on identity without a
			// claim-version bump, so the retry is cheap and idempotent.
			log.Printf("live facts %s: %v（游标保留，下一轮重读）", intentID, err)
			return
		}
		offset = next
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
				read()
			}
		}
	}()
	var once sync.Once
	return func() {
		// Stop the ticker before the finish path runs. The two used to overlap:
		// both did FindFactByTitle then AddFact, so a declaration written just
		// before the run ended could be inserted twice (2026-11 audit). The
		// final read settles the trailing line the ticker has not seen yet.
		once.Do(func() {
			close(stop)
			<-done
			read()
		})
	}
}

// readLiveDeclarations applies the handoff lines from offset and returns the
// offset a following call should start at. It reads without a persisted commit:
// the finish path still owns facts.offset, so re-reading a line here is the
// normal duplicate case, not an error.
//
// A failure leaves the returned offset unchanged even if earlier lines in this
// pass committed; they dedupe on the retry. Advancing past the failed line would
// silently drop a declaration, which is the failure mode the cursor rule exists
// to prevent.
func (s *Server) readLiveDeclarations(intentID, workDir, sourceRun string, offset int64) (int64, error) {
	decl, err := ledger.ReadDeclaredFrom(s.CaseRoot, workDir, offset)
	if err != nil {
		return offset, err
	}
	for _, df := range decl.Facts {
		// Playbooks are project knowledge, not live findings: they land in the
		// playbook table when the run settles, and adding them here would count
		// them as facts on the board (D70).
		if df.Kind == "playbook" {
			continue
		}
		res, err := ledger.ApplyDeclaration(s.Store, intentID, sourceRun, df, nil)
		if err != nil {
			return offset, err
		}
		if !res.Changed {
			continue
		}
		// The set of rows the readers see changed: route it to related
		// directions and wake the commander, exactly as the old add-only path
		// did for a new fact.
		s.routeAndDeliver()
		if s.Decider != nil {
			s.wakeCommanderIfNeeded("fact")
		}
	}
	return decl.Consumed, nil
}
