package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/board"
	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/decider"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// ---- state ----

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) && !s.caller(r).agent {
		writeErr(w, 404, "not found")
		return
	}
	if c := s.caller(r); c.agent && c.participant != collab.CommanderID {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	note, err := s.State.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if raw := r.URL.Query().Get("version"); raw != "" {
		version, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || version == 0 {
			writeErr(w, 400, "invalid note version")
			return
		}
		var ok bool
		note, ok = s.Store.NotebookVersion(version)
		if !ok {
			writeErr(w, 404, "note version not found")
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"state":   note.State,
		"path":    s.State.Path(),
		"version": note.Version, "receipt": note.NoteRef,
		"written_at":    note.WrittenAt,
		"fact_versions": note.FactVersions, "fact_claim_versions": note.FactClaimVersions,
		"stale_fact_refs": s.Store.StaleNotebookFacts(note),
		"run_id":          s.caller(r).runID,
	})
}

func (s *Server) putState(w http.ResponseWriter, r *http.Request) {
	c := s.caller(r)
	if harnessSession(r) && !c.agent {
		writeErr(w, 404, "not found")
		return
	}
	if c.agent && c.participant != collab.CommanderID {
		writeErr(w, 404, "not found")
		return
	}
	var req struct {
		State        string            `json:"state"`
		Version      *uint64           `json:"version"`
		RunID        string            `json:"run_id"`
		FactVersions map[string]uint64 `json:"fact_versions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	if req.Version == nil {
		writeErr(w, http.StatusBadRequest, "version 必填：先 GET /api/state")
		return
	}
	writer, runID := "operator", ""
	if c.agent {
		if req.RunID == "" || req.RunID != c.runID {
			writeErr(w, http.StatusConflict, "run_id 必须匹配当前指挥官轮次")
			return
		}
		// An empty body cannot be a handoff receipt: it carries no conclusion,
		// lead or next step, so accepting it would erase the memory this round
		// was supposed to preserve and still look like a written handoff.
		if strings.TrimSpace(req.State) == "" {
			writeErr(w, http.StatusBadRequest, "交接笔记不能写空：写清目标进度、结论、线索与下一步；空正文不产生回执")
			return
		}
		writer, runID = "commander", c.runID
	} else if req.RunID != "" {
		writeErr(w, http.StatusBadRequest, "操作员不能代指挥官写交接回执")
		return
	}
	note, projectionWarning, err := s.State.Replace(*req.Version, req.State, writer, runID, req.FactVersions)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNoteConflict) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	s.recordActionFor(runID, "state", strconv.FormatUint(note.Version, 10), firstLine(req.State))
	writeJSON(w, 200, map[string]any{"saved": true, "bytes": len(req.State), "version": note.Version,
		"receipt": note.NoteRef, "projection_warning": projectionWarning})
}

// supportPurpose reports whether a run is operator-side support work: report
// writing and verification. Commander and executor sessions only do the test
// itself, so these runs (and the report/review workflow behind them) are
// invisible on their API surface.
func supportPurpose(purpose string) bool {
	return purpose == "report" || purpose == "verifier"
}

// dismissRunWarnings is the operator's "ignore" for a finished direction's
// non-fatal handoff diagnostics. It is not a deletion: the rows stay on the run
// and the run detail can still show them; the status-page count and the board's
// handoff_warnings stop including this run. A failed or still-running direction
// is refused: its warnings are still the next attempt's hint.
func (s *Server) dismissRunWarnings(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	id := r.PathValue("id")
	run, ok := s.Store.GetRun(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if run.IntentID == "" {
		writeErr(w, http.StatusConflict, "只对意图 run 提供交接告警忽略")
		return
	}
	if in, ok := s.Store.GetIntent(run.IntentID); ok && in.Status != model.StatusDone {
		writeErr(w, http.StatusConflict, "只对已结束（done）意图的交接告警提供忽略")
		return
	}
	updated, err := s.Store.UpdateRun(id, func(r *model.Run) {
		r.WarningsDismissed = true
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	runs := s.Store.Runs()
	// The agent surface is the battlefield: a report or verification run is the
	// operator's post-processing, not the session's work. harnessSession covers
	// both the participant mux and the commander's project-mux credential.
	if harnessSession(r) {
		visible := make([]model.Run, 0, len(runs))
		for _, run := range runs {
			if supportPurpose(run.Purpose) {
				continue
			}
			visible = append(visible, run)
		}
		runs = visible
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

// guardInfo is the watchdog's public pulse for the console: cadence, idle
// threshold, and when it last swept. It is observation only — the guard has no
// model and no policy; the page must not have to guess its numbers.
type guardInfo struct {
	IntervalSeconds    int        `json:"interval_seconds"`
	IdleTimeoutSeconds int        `json:"idle_timeout_seconds,omitempty"`
	LastTickAt         *time.Time `json:"last_tick_at,omitempty"`
}

// boardRunWarnings is one run's handoff problems as the board shows them. The
// board is the operator's first look, so "why is this evidence missing" must be
// answerable there without opening the run list (task-17).
type boardRunWarnings struct {
	Run      string   `json:"run"`
	IntentID string   `json:"intent_id,omitempty"`
	Warnings []string `json:"warnings"`
}

// recentHandoffWarnings returns the latest runs that recorded warnings, newest
// first. It is bounded so a ledger with many noisy runs cannot grow the board
// response; the full list stays on each run row.
func recentHandoffWarnings(st *store.Store, limit int, hideSupport bool) []boardRunWarnings {
	if st == nil || limit <= 0 {
		return nil
	}
	runs := st.Runs()
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt) })
	out := make([]boardRunWarnings, 0, limit)
	for _, r := range runs {
		// Report/verification warnings are operator-side post-processing: the
		// commander/executor surface must not learn that these runs exist
		// (docs/01-architecture.md §8), and a warning names the run.
		if hideSupport && supportPurpose(r.Purpose) {
			continue
		}
		if len(r.Warnings) == 0 || r.WarningsDismissed {
			continue
		}
		out = append(out, boardRunWarnings{Run: r.ID, IntentID: r.IntentID, Warnings: r.Warnings})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// sessionDeciderStatus is the only decider state a harness session sees: the
// operational facts it can act on (is the commander enabled, is it stopped, why).
// Everything else in decider.Status — round cadence, wake batches, token and
// uptime accounting, live activity — is operator supervision (docs §8), so the
// session response ships a separate shape instead of those fields as zeros.
type sessionDeciderStatus struct {
	Enabled      bool   `json:"enabled"`
	Paused       bool   `json:"paused,omitempty"`
	PausedReason string `json:"paused_reason,omitempty"`
}

func (s *Server) getBoard(w http.ResponseWriter, r *http.Request) {
	db := s.Store.Snapshot()
	v := board.BuildFromDB(db, 20)
	var operatorFrontier []operatorFrontierItem
	if !harnessSession(r) {
		operatorFrontier = s.operatorFrontier(db, v.Frontier)
	}
	st := decider.Status{Enabled: false}
	if s.Decider != nil {
		st = s.Decider.Status()
	}
	// The guard is control-plane supervision: cadence, idle sweep and last tick
	// are operator-visible, never part of a commander/executor's battlefield.
	var guard *guardInfo
	if !harnessSession(r) {
		g := guardInfo{IntervalSeconds: int(watchInterval / time.Second)}
		if s.IdleTimeout > 0 {
			g.IdleTimeoutSeconds = int(s.IdleTimeout / time.Second)
		}
		if n := s.lastWatch.Load(); n > 0 {
			t := time.Unix(0, n)
			g.LastTickAt = &t
		}
		guard = &g
	}
	// Review/report queue state is operator-side post-processing: the commander
	// and executors only see facts and their review verdicts, not the queue that
	// produces reports. A zeroed summary keeps the response shape stable.
	reviews := reviewSummary{}
	if !harnessSession(r) {
		reviews = s.reviewSummary()
	}
	// Pause kind is a machine code, not prose: the console must not infer "who
	// paused" from the reason text. The operator board also carries the whole
	// action contract (available pause/resume/append-goal plus the counters they
	// were decided from); a harness session never sees it — dispatch decisions
	// are not part of the battlefield.
	projectPaused := s.paused.Load()
	pauseKind := ""
	var projectActions *ProjectActions
	if !harnessSession(r) {
		pa := s.projectActions()
		projectPaused = pa.State.Paused
		if projectPaused {
			pauseKind = pa.State.Kind
		}
		projectActions = &pa
	} else if projectPaused {
		if ps := s.projectState(); ps.Paused {
			pauseKind = ps.Kind
		} else {
			projectPaused = false
		}
	}
	deciderView := any(st)
	digest := board.Digest(v)
	// The fence block attaches after the digest on purpose: a newly pending
	// asset must not wake a round by itself (the pending flow is quiet — the
	// operator approves from the communication page, the approval — not the
	// refusal — is what wakes). The commanders and executors read the standing
	// from the board they already receive each round.
	if s.Project != nil {
		v.Fence = s.boardFenceBlock(db)
	}
	if harnessSession(r) {
		deciderView = sessionDeciderStatus{Enabled: st.Enabled, Paused: st.Paused, PausedReason: st.PausedReason}
		if s.ReviewsDisabled() {
			v = board.ForSessionOpen(v)
		} else {
			v = board.ForSession(v)
		}
	}
	writeJSON(w, 200, struct {
		board.View
		Decider          any    `json:"decider"`
		ProjectPaused    bool   `json:"project_paused"`
		ProjectPauseKind string `json:"project_pause_kind,omitempty"`
		// ProjectActions is the operator's action contract: pause / resume /
		// append_goal, each with the machine code and one sentence saying why it
		// is or is not available, plus the counters those decisions read. The
		// console renders buttons from this instead of deriving "已收口" from
		// paused + goal count.
		ProjectActions *ProjectActions  `json:"project_actions,omitempty"`
		Guard          *guardInfo       `json:"guard,omitempty"`
		Usage          board.TokenUsage `json:"usage"`
		// Digest is the value the decider compares with its last round
		// (board.Digest). The console shows it next to that stored digest, so an
		// operator can tell "the board changed, a round is due" from "nothing
		// new, the heartbeat will skip" without guessing.
		Digest string `json:"digest"`
		// HandoffWarnings are the latest runs' non-fatal handoff problems (e.g.
		// evidence paths that could not be registered). Bounded; the full list
		// is on each run in /api/runs (task-17).
		HandoffWarnings  []boardRunWarnings     `json:"handoff_warnings,omitempty"`
		Reviews          reviewSummary          `json:"reviews"`
		OperatorFrontier []operatorFrontierItem `json:"operator_frontier,omitempty"`
	}{View: v, Decider: deciderView, ProjectPaused: projectPaused, ProjectPauseKind: pauseKind,
		OperatorFrontier: operatorFrontier,
		ProjectActions:   projectActions,
		Guard:            guard,

		Usage:           board.Tokens(s.Store, time.Now()),
		Digest:          digest,
		HandoffWarnings: recentHandoffWarnings(s.Store, 5, harnessSession(r)), Reviews: reviews})
}

func (s *Server) triggerDecide(w http.ResponseWriter, r *http.Request) {
	if s.Decider == nil {
		writeErr(w, 503, "指挥官未启用（--no-decider）")
		return
	}
	if s.paused.Load() || s.Decider.Paused() {
		writeErr(w, http.StatusConflict, "项目或指挥官已暂停：请先显式继续，再请求指挥官轮次")
		return
	}
	if !s.Decider.Status().Enabled {
		writeErr(w, 503, "模型未配置：在「设置」里填 API 地址 / Key / 模型")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSON(w, r.Body, &req) {
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		req.Reason = "operator"
	}
	// The endpoint is "one global decision round, now". Whether a reason label
	// counts as forced is an admission detail (isForced matches three literals),
	// so a caller-supplied label must not decide it: a custom reason such as
	// "review" was queued, silently dropped by the round's attention gate, and
	// still answered 202. The label stays in the response and the log.
	s.Decider.Wake("operator")
	writeJSON(w, 202, map[string]any{"queued": true, "reason": req.Reason})
}

// deciderPause is the kill switch for unattended runs: the loop keeps running
// (watchdog, ledger, console), but no model call happens until Resume.
func (s *Server) deciderPause(w http.ResponseWriter, r *http.Request) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.Decider == nil {
		writeErr(w, 503, "指挥官未启用")
		return
	}
	s.Decider.Pause()
	writeJSON(w, 200, s.Decider.Status())
}

func (s *Server) deciderResume(w http.ResponseWriter, r *http.Request) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.Decider == nil {
		writeErr(w, 503, "指挥官未启用")
		return
	}
	if s.paused.Load() {
		writeErr(w, http.StatusConflict, "项目仍处于暂停状态：请先继续项目")
		return
	}
	s.Decider.Resume()
	s.Decider.Wake("operator")
	writeJSON(w, 200, s.Decider.Status())
}

// projectPause is the whole-project switch: the commander stops taking rounds
// *and* every live executor run is canceled. It cancels runs, not intents, so
// the directions go back to pending instead of being held; projectResume lets
// the commander dispatch them again.
func (s *Server) projectPause(w http.ResponseWriter, r *http.Request) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.closeoutCancel != nil {
		s.closeoutCancel()
	}
	// Record the structured cause before the gate flips: a reader that sees
	// paused=true must never have to wait for the cause to be written.
	s.pauseCause = pauseCauseManual
	// Gate dispatch first: a round already past the decider's pause check may
	// still be finishing, and it must not start a new run after this point.
	s.SetPaused(true)
	if s.Decider != nil {
		s.Decider.Pause()
	}
	canceled := s.CancelProjectWorkWithCause(model.CancelCausePause, "项目暂停：停止运行与指挥官；复核有自己的开关（D137）")
	if err := s.setProjectPausedKind(true, pauseKindManualPause); err != nil {
		writeErr(w, http.StatusInternalServerError, "本次运行已暂停，但无法保存暂停状态："+err.Error())
		return
	}
	out := map[string]any{"paused": true, "canceled": canceled}
	if s.Decider != nil {
		out["decider"] = s.Decider.Status()
	}
	writeJSON(w, 200, out)
}

// commanderClaim reports whether a request states the commander role. The
// project mux authenticates every harness session with the same derived token,
// so the gate can only see "a session"; the caller must either carry the
// commander's per-run identity or use the explicit actor claim the mailbox path
// already uses. The standby handler additionally requires a live commander round
// to attribute the request to, which an executor session does not have.
func commanderClaim(r *http.Request) bool {
	if id, ok := identityOf(r); ok && id.ParticipantID == collab.CommanderID {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Tw-Actor")), collab.CommanderID)
}

// projectStandby is the commander's explicit "stop this engagement" action. The
// operator's switch is projectPause; a commander round that judges the engagement
// should stop needs the same global stop, otherwise it can only park directions
// and the ledger keeps waking it for the parked result. That is the exact waste
// an unreachable-target project hit: it had already decided to stop, replied to
// the operator and parked its only direction, and the next round still ran only
// to conclude "nothing to do".
//
// It is a real pause, not a hidden quiet flag: the dispatch gate closes, the
// decider stops taking rounds, executing directions are canceled and the
// manifest keeps paused=true across a restart. Resume stays operator-only — the
// agent surface never exposes projectResume or append-goal.
//
// The asking round finishes normally: it is excluded from the cancellation
// snapshot and Runner.StandbyFor does not cancel it, so its answer, state.md
// update and delivery checkpoint still land. The commission and the console show
// why the project is parked (pause_kind=commander_standby), and the operator's
// explicit 继续项目 is the only way back.
func (s *Server) projectStandby(w http.ResponseWriter, r *http.Request) {
	if !harnessSession(r) {
		writeErr(w, http.StatusForbidden, "只有指挥官会话可以请求停止项目；操作员请用 /api/project/pause")
		return
	}
	// Every harness session carries the same derived agent token, so the auth
	// gate can only classify "a session", never "the commander". A global stop
	// belongs to the commander (docs §2/§6): require the commander claim and a
	// live commander round the request can be attributed to. An executor has
	// neither. This is a claimed-role boundary for trusted sessions, not a
	// sandbox (D78); the operator's pause route stays separate.
	if !commanderClaim(r) {
		writeErr(w, http.StatusForbidden, "全局停止只能由指挥官轮次请求；执行者请把判断交回指挥官")
		return
	}
	var req struct {
		Reason     string `json:"reason"`
		StopReason string `json:"stop_reason"`
	}
	if !decodeOptionalJSON(w, http.MaxBytesReader(w, r.Body, 4096), &req) {
		return
	}
	req.StopReason = strings.ToLower(strings.TrimSpace(req.StopReason))
	switch req.StopReason {
	case "", stopReasonGoalAchieved, stopReasonBlocked, stopReasonLowValue:
	default:
		writeErr(w, http.StatusBadRequest, "stop_reason 只能是 goal_achieved / blocked / low_value")
		return
	}
	detail := clipRunes(req.Reason, 300)
	reason := "指挥官请求停止项目"
	if detail != "" {
		reason += "：" + detail
	} else {
		reason += "：等待操作员确认后继续"
	}

	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.paused.Load() {
		// Pause is already in effect (another pause path won the race): the
		// commander's request is satisfied, and its cause is not overwritten.
		out := map[string]any{"paused": true, "already_paused": true}
		if s.Decider != nil {
			out["decider"] = s.Decider.Status()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// The stop must be attributable to the round that asked for it: checking
	// before the gate flips keeps a stray request from parking the project with
	// no session to explain it.
	run, live := s.liveCommanderRun()
	if !live {
		writeErr(w, http.StatusConflict, "没有进行中的指挥官轮次：全局停止只能由发起请求的那一轮认定")
		return
	}
	// A claim of "goal achieved" is checked against the ledger, not the reason
	// prose. The commander must close the goal first; otherwise the project would
	// park with a "done" note and an unmet goal, which is exactly the ambiguous
	// state the operator then has to reconcile by hand.
	if req.StopReason == stopReasonGoalAchieved {
		if s.Store == nil {
			writeErr(w, http.StatusConflict, "这个项目没有账本：不能用 goal_achieved")
			return
		}
		goals := s.Store.Goals()
		active := 0
		for _, g := range goals {
			if !g.Closed {
				active++
			}
		}
		if active == 0 {
			writeErr(w, http.StatusConflict, "项目没有目标：不能用 goal_achieved，请用 blocked 或 low_value")
			return
		}
		for _, g := range goals {
			if !g.Closed && !g.Met {
				writeErr(w, http.StatusConflict,
					"目标 "+g.ID+" 仍未达成：先用 POST /api/goals/"+g.ID+"/complete 盖章，再请求停止；或用 blocked/low_value")
				return
			}
		}
	}
	// The structured cause is recorded before the gate flips, like projectPause:
	// a reader that sees paused=true must never have to wait for the cause.
	s.pauseCause = pauseCauseCommander
	s.SetPaused(true)
	// This round asked for the stop; cutting it would discard the very answer
	// and checkpoint that make the stop auditable.
	skip := run.ID
	if s.Decider != nil {
		s.Decider.StandbyFor(reason)
	}
	canceled := s.CancelProjectWorkExcept(model.CancelCausePause, reason, skip)
	action := reason
	if req.StopReason != "" {
		action += "（stop_reason=" + req.StopReason + "）"
	}
	s.recordAction("standby", "", action)
	if err := s.setProjectPausedKind(true, pauseKindCommanderStandby); err != nil {
		writeErr(w, http.StatusInternalServerError, "项目已进入停止状态，但无法保存暂停："+err.Error())
		return
	}
	log.Printf("指挥官请求停止项目：%s（跳过本轮 run %s，取消 %d 个在跑 run）", detail, skip, canceled)
	out := map[string]any{"paused": true, "canceled": canceled, "reason": reason}
	if req.StopReason != "" {
		out["stop_reason"] = req.StopReason
	}
	if s.Decider != nil {
		out["decider"] = s.Decider.Status()
	}
	writeJSON(w, http.StatusOK, out)
}

// AutoPause pauses a project whose chartered work is complete. It is the
// decider's "nothing left to attend to" signal, not an operator action: it
// gates dispatch, pauses the commander, cancels what a pause owns and writes
// the paused flag to the manifest, but it never starts or resumes anything.
// The reason is kept so the console can say why the switch flipped.
func (s *Server) AutoPause(reason string) {
	if s == nil || s.paused.Load() || s.closing.Load() || s.deleting.Load() {
		return
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.paused.Load() {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "目标已完成：项目自动暂停；新增工作后点「继续项目」"
	}
	// A completion stand-by is the auto-pause, not an operator action. The cause
	// is structured so append-and-continue can resume exactly this kind without
	// reading the reason prose.
	s.pauseCause = pauseCauseCompletion
	// Gate dispatch before anything else: a round already past the decider's
	// pause check must not start a new run after this point.
	s.SetPaused(true)
	if s.Decider != nil {
		s.Decider.PauseFor(reason)
	}
	canceled := s.CancelProjectWorkWithCause(model.CancelCausePause, reason)
	if err := s.setProjectPausedKind(true, pauseKindCompletionStandby); err != nil {
		log.Printf("项目自动暂停：本次已暂停，但保存暂停状态失败：%v", err)
	}
	log.Printf("项目自动暂停：%s（取消 %d 个在跑 run）", reason, canceled)
}

// setProjectPaused persists the manifest's paused flag. A server without a
// project (bare test fixtures) has nowhere to persist it. The injectable seam
// exists for tests: a durable write failure must leave the runtime pause and the
// manifest saying the same thing instead of silently diverging.
func (s *Server) setProjectPaused(paused bool) error {
	return s.setProjectPausedKind(paused, "")
}

// setProjectPausedKind persists the pause with its structured cause. The kind is
// what survives a control-plane restart: "commander requested stop" must not
// come back as "the operator paused the project". An empty kind when pausing
// keeps whatever the manifest already carries (a pause is a transition, not a
// reclassification); resuming always clears it.
func (s *Server) setProjectPausedKind(paused bool, kind string) error {
	if s.persistPausedKind != nil {
		return s.persistPausedKind(paused, kind)
	}
	if s.persistPaused != nil {
		return s.persistPaused(paused)
	}
	if s.Project == nil {
		return nil
	}
	return s.Project.SetPausedWithKind(paused, kind)
}

// RestorePauseKind re-applies the persisted pause cause after a boot attach. The
// console's pause badge and append-and-continue both classify from the runtime
// cause, so without this a restart would relabel a commander stop as manual.
func (s *Server) RestorePauseKind(kind string) {
	switch kind {
	case pauseKindCommanderStandby:
		s.pauseCause = pauseCauseCommander
	case pauseKindCompletionStandby:
		s.pauseCause = pauseCauseCompletion
	case pauseKindManualPause:
		s.pauseCause = pauseCauseManual
	case pauseKindRestart:
		s.pauseCause = pauseCauseRestart
	}
}

// resumeProjectRuntimeLocked re-arms the runtime after the manifest write has
// already succeeded. The caller holds controlMu, which is what serialises this
// against projectPause/AutoPause; the runtime flags are only flipped after the
// durable flag is written, so a failed write leaves the project paused as the
// manifest still says.
func (s *Server) resumeProjectRuntimeLocked() {
	s.SetPaused(false)
	if s.Decider != nil {
		s.Decider.Resume()
	}
	s.pauseCause = ""
	// Resume is also the point where work interrupted by a restart may continue:
	// the boot path keeps the project paused on purpose, so this is the first
	// moment a replay is what the operator actually asked for.
	go s.redispatchInterrupted()
}

// projectResume re-arms the whole project after projectPause. It is explicit:
// nothing resumes the loop automatically.
func (s *Server) projectResume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GoalID string `json:"goal_id"`
	}
	if !decodeOptionalJSON(w, http.MaxBytesReader(w, r.Body, 4096), &req) {
		return
	}
	req.GoalID = strings.TrimSpace(req.GoalID)

	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	// One verdict serves the console's button and this handler: the button must
	// never say "可以继续" while the request comes back 409 for another reason.
	// projectStateLocked reads the pause cause under the lock we already hold.
	before := s.projectStateLocked()
	verdict := s.resumeActionLocked(before, s.projectFactsLocked())
	if verdict.ReasonCode == reasonCloseoutInProgress {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": verdict.Reason,
			"code":  verdict.ReasonCode,
		})
		return
	}
	var scope *resumeScope
	if req.GoalID != "" {
		if s.Store == nil {
			writeErr(w, http.StatusBadRequest, "这个项目没有账本，不能按目标继续")
			return
		}
		found := false
		for _, g := range s.Store.Goals() {
			if g.ID != req.GoalID {
				continue
			}
			found = true
			if g.Closed {
				writeErr(w, http.StatusConflict, "目标已关闭，请追加新目标")
				return
			}
			if g.Met {
				writeErr(w, http.StatusConflict, "目标已达成：要继续请在同一个项目里追加新目标")
				return
			}
			scope = &resumeScope{GoalID: g.ID, Statement: g.Statement}
			break
		}
		if !found {
			writeErr(w, http.StatusBadRequest, "没有这个目标："+req.GoalID)
			return
		}
	}
	// Foolproofing for the one click that spends a round for nothing: a closed
	// project has no goal to judge and no direction to dispatch, so the forced
	// commander round would only re-read a board that says "done" and settle
	// back into stand-by. The rule and the code both come from the shared
	// verdict, so the console can grey the button with the same sentence. A
	// provider fault is excluded there: resume is that pause's recovery path.
	// The verdict is the single judge for both the console button and this
	// handler. Without the old `before.Paused` pre-condition, a resume that
	// arrives while the project is already running skipped the refusal, rewrote
	// the manifest and forced a commander round with nothing new to act on, while
	// the button said "项目没有暂停" (already_running). Now both answer with the same
	// code, and a refused resume touches neither the manifest nor the decider.
	if !verdict.Enabled {
		body := map[string]any{
			"error":   verdict.Reason,
			"code":    verdict.ReasonCode,
			"paused":  before.Paused,
			"resumed": false,
		}
		if before.Paused {
			body["next"] = []string{"append_goal", "message"}
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	if err := s.setProjectPausedKind(false, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "无法保存继续状态，项目保持暂停："+err.Error())
		return
	}
	// Attach the named open goal to the next commander round only after the
	// durable resume succeeded: a failed manifest write must leave both the
	// project and the scope untouched.
	if scope != nil {
		s.setResumeScope(*scope)
	}
	s.resumeProjectRuntimeLocked()
	if s.Decider != nil {
		s.Decider.Wake("operator")
	}
	// Resume is where a pause's suspended children and waits continue: the
	// scheduler was gated while paused, so it must be kicked explicitly.
	go s.pump()
	out := map[string]any{"paused": false}
	if scope != nil {
		out["scope_goal_id"] = scope.GoalID
	}
	if s.Decider != nil {
		out["decider"] = s.Decider.Status()
	}
	writeJSON(w, 200, out)
}

func (s *Server) listDecisions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	ds, err := decider.ReadLog(filepath.Join(s.DataDir, "decisions.jsonl"), limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"decisions": ds})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if harnessSession(r) {
		if run, ok := s.Store.GetRun(id); ok && supportPurpose(run.Purpose) {
			writeErr(w, 404, "run not found")
			return
		}
	}
	if err := s.CancelRun(id); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"canceled": id})
}

// CancelRun stops a live run on the operator's behalf.
func (s *Server) CancelRun(runID string) error {
	return s.cancelWithCause(runID, model.CancelCauseOperator, "已取消（人工或重派）")
}

// CancelRunWithCause stops a live run and records the structured cause. Callers
// that are not the operator (steer, stop-loss, watchdog) must use this so the
// console can distinguish a continuation from a failure.
func (s *Server) CancelRunWithCause(runID, cause, reason string) error {
	return s.cancelWithCause(runID, cause, reason)
}

// CancelRunWithReason is the watchdog's version: the run log should say "idle
// for 15m", not "a human did it".
func (s *Server) CancelRunWithReason(runID, reason string) error {
	return s.cancelWithCause(runID, model.CancelCauseWatchdog, reason)
}

func (s *Server) cancelWithCause(runID, cause, reason string) error {
	// Record only a cancel that actually happened. A run can settle between the
	// caller's live-set read and this call; writing the metric before the
	// lifecycle call would count a natural finish as a control-plane cancel.
	stop := ""
	if s.Metrics != nil {
		if run, ok := s.Store.GetRun(runID); ok {
			stop = run.StopReason
		}
	}
	if err := s.Lifecycle.CancelRunWithCause(runID, cause, reason); err != nil {
		return err
	}
	if s.Metrics != nil {
		s.Metrics.Cancel(runID, cause, reason, stop)
	}
	return nil
}

func (s *Server) cancelReason(runID string) string {
	return s.Lifecycle.CancelReason(runID)
}

func (s *Server) cancelCause(runID string) string {
	return s.Lifecycle.CancelCause(runID)
}

// setCancelCause records why a run ended for paths that do not go through
// cancelWithCause (today: the context deadline in dispatch).
func (s *Server) setCancelCause(runID, cause string) {
	s.Lifecycle.SetCancelCause(runID, cause)
}

func (s *Server) wasCanceled(runID string) bool {
	return s.Lifecycle.WasCanceled(runID)
}

// BeginShutdown seals this server's admission before canceling runs. A child
// settling to pending must not be picked up by the pump during process exit.
// This is transient lifetime state, independent of the durable project pause.
func (s *Server) BeginShutdown() int {
	n := s.Lifecycle.BeginShutdown()
	s.reviewPumpMu.Lock()
	if s.reviewPumpCancel != nil {
		s.reviewPumpCancel()
	}
	s.reviewPumpMu.Unlock()
	// The commander's session credential belongs to this runtime. The collab
	// registry is process-wide, so leaving it resolvable after shutdown would
	// keep a live commander token for a project that no longer runs.
	s.RevokeCommanderIdentity()
	return n
}

// CancelAll cancels the current run snapshot without sealing new admission.
// Process shutdown must use BeginShutdown; deletion seals with SetDeleting.
func (s *Server) CancelAll() int {
	return s.CancelAllWithCause(model.CancelCauseRestart, "项目停止：取消所有运行")
}

// CancelAllWithCause is CancelAll with the structured reason recorded on every
// stopped run. A purpose run settles as canceled with that cause, never as a
// failed executor: stopping the project is the control plane's decision.
func (s *Server) CancelAllWithCause(cause, reason string) int {
	return s.Lifecycle.CancelAllWithCause(cause, reason)
}

// CancelProjectWorkWithCause stops what the project pause owns: intent runs and
// report runs. Verifier runs are deliberately not in this snapshot — a review
// is an operator-triggered re-proof and has its own switch (D137). Shutdown and
// delete use CancelAllWithCause and do stop reviews.
