package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/kb"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/skills"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// ---- dispatch ----

// archiveBrief writes the final prompt of a new dispatch to
// `<case>/.zrcin/briefs/<runID>.md` and returns its case-relative path. It is the
// operator's record of what the run was asked: multiple runs on one direction
// keep one file each, and a retried direction does not overwrite the old one.
//
// The file is 0600 inside a 0700 directory because it is private operator
// material, and it is passed through redactSecrets first: the live prompt may
// carry a credential in a section the journal already redacts, and an archived
// copy must not become the one place it survives (task-13 boundary, task-25).
func (s *Server) archiveBrief(workDir, runID, prompt string) (string, error) {
	dir := filepath.Join(workDir, ".zrcin", "briefs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, runID+".md")
	if err := os.WriteFile(path, []byte(s.redactSecrets(prompt)), 0o600); err != nil {
		return "", err
	}
	// The mode above is the creation request; POSIX must also get it on an
	// existing file (a retried run id never reuses a path, but a test or a
	// restored case can). Windows ignores the bits; the ACL there is the
	// operator's, and the file is inside the case.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.CaseRoot, path)
	if err != nil {
		return filepath.ToSlash(path), nil
	}
	return filepath.ToSlash(rel), nil
}

func (s *Server) dispatch(base context.Context, in model.Intent, runID, nodeID string, resume bool, prompt string, keepPrompt bool) {
	if base.Err() != nil {
		s.finish(runID, in.ID, model.RunCanceled, s.cancelReason(runID), executor.Result{}, 0)
		return
	}
	workDir := filepath.Join(s.CaseRoot, in.ID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		s.finish(runID, in.ID, model.RunFailed, "mkdir: "+err.Error(), executor.Result{}, 0)
		return
	}
	// The ask file is how a running executor reaches the decider mid-run; the
	// directory has to exist before the harness starts, not when it first asks.
	_ = os.MkdirAll(filepath.Join(workDir, ".zrcin"), 0o755)
	logDir := filepath.Join(s.DataDir, "runs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, runID+".jsonl")
	// One writer for this run's log, registered before the harness starts and
	// closed after it returns: concurrent message delivery and injection audit
	// append through the same journal instead of opening a second encoder.
	if _, err := s.openJournal(runID, logPath); err != nil {
		s.finish(runID, in.ID, model.RunFailed, "log: "+err.Error(), executor.Result{}, 0)
		return
	}
	defer s.closeJournal(runID)
	// First line, before the harness starts: a dispatch that dies in the dial phase
	// must still leave an attributable run (see noteRunStarted).
	s.noteRunStarted(runID, in.ID, "dispatch", workDir)
	defer func() { _, _ = s.Store.UpdateRun(runID, func(r *model.Run) { r.LogPath = logPath }) }()

	ctx := base
	if s.Timeout > 0 {
		var tcancel context.CancelFunc
		ctx, tcancel = context.WithTimeout(base, s.Timeout)
		defer tcancel()
	}
	declarationStart := int64(0)
	if info, err := os.Stat(filepath.Join(workDir, ".zrcin", "facts.jsonl")); err == nil {
		declarationStart = info.Size()
	} else if !os.IsNotExist(err) {
		s.finish(runID, in.ID, model.RunFailed, "读取声明边界失败："+err.Error(), executor.Result{}, 0)
		return
	}
	if _, err := s.Store.UpdateRun(runID, func(r *model.Run) { r.DeclarationStart = &declarationStart }); err != nil {
		s.finish(runID, in.ID, model.RunFailed, "记录声明边界失败："+err.Error(), executor.Result{}, 0)
		return
	}
	stopFacts := s.watchFacts(ctx, in.ID, workDir, runID)
	defer stopFacts()

	started := time.Now()
	skillPaths, skillErr := skills.Resolve(
		append(append([]string{}, s.Skills...), in.Skills...),
		s.SkillsDir,
	)
	if skillErr != nil {
		s.finish(runID, in.ID, model.RunFailed, "准备技能失败："+skillErr.Error(), executor.Result{}, 0)
		return
	}
	policy := s.premisePolicy()
	db, gaps, inputErr := s.Store.PrepareRunInput(runID, in, policy, store.DispatchGate{
		ReviewsOff: s.ReviewsDisabled(),
	})
	if inputErr != nil {
		s.finish(runID, in.ID, model.RunFailed, "准备输入失败："+inputErr.Error(), executor.Result{}, 0)
		return
	}
	// Admission is recorded, not enforced: an engagement is not stopped because a
	// premise cannot be cited yet. The gap goes to the board glance and the
	// journal now, while the file is open, so later readers see which direction
	// ran thin instead of a silent veto.
	if len(gaps) > 0 {
		s.noteDispatchGaps(runID, in.ID, gaps)
	}
	brief := s.Constraints()
	inputErr = publishRunInput(workDir, runID, db, brief, in.ID)
	graphOK, focusOK := inputErr == nil, inputErr == nil
	if inputErr != nil {
		log.Printf("run %s: 发布输入快照失败：%v", runID, inputErr)
	}
	// The executor's file tools are confined to the case; a KB in <data>/kb is
	// outside them. Copy it inside .zrcin so `read`/`glob` work without a shell
	// (Windows nodes often have cmd.exe, not grep).
	if s.KBDir != "" {
		var err error
		if s.KB != nil {
			err = s.KB.CopyTo(filepath.Join(workDir, ".zrcin", "kb"))
		} else {
			err = kb.CopyTo(s.KBDir, filepath.Join(workDir, ".zrcin", "kb"))
		}
		if err != nil {
			log.Printf("run %s: 复制知识库失败：%v", runID, err)
		}
	}
	// The current instruction set is frozen with the input and rendered before
	// the task material: operator guidance is the strongest stable constraint,
	// and a long prompt must not bury it behind the evidence contract.
	instructions := instructionSection(store.ApplicableInstructions(db, in.ID, false), runID)
	s.NoteInjected(runID, "instruction", "", len(instructions), "instructions")
	// A replacement session reads the operator's own words, not the previous
	// model's account of them. The text comes from this task book (the same
	// bytes the prompt already carries) and the pointer comes from the run row,
	// which only the control plane can read.
	handoffAuth := handoffAuthorization{Briefing: brief, Instructions: instructions}
	if s.Project != nil {
		handoffAuth.BriefRevision = s.Project.BriefRevision()
	}
	if in.Handoff != nil {
		handoffAuth.SourceRun = in.Handoff.FromRun
		if src, ok := s.Store.GetRun(in.Handoff.FromRun); ok {
			handoffAuth.SourceBrief = src.BriefPath
			// The ids come from the replaced run's frozen set, not from a fresh
			// query: the point is what that session actually read.
			for id := range src.InstructionVersions {
				handoffAuth.InstructionIDs = append(handoffAuth.InstructionIDs, id)
			}
			sort.Strings(handoffAuth.InstructionIDs)
		}
	}
	waitSection := s.readyWaitSection(in)
	collabSection := s.collaborationSection(in)
	accessSection, accessErr := s.accessSection(workDir)
	if accessErr != nil {
		s.finish(runID, in.ID, model.RunFailed, "准备登录材料失败："+accessErr.Error(), executor.Result{}, 0)
		return
	}
	// A fresh dispatch may be the recovery of a recycled worker: the note the
	// previous run wrote before it closed is the only cross-turn memory the
	// control plane has for this direction. slimPrompt is the one automatic
	// recovery after the harness reported a context limit: same hard material,
	// optional summaries replaced by ids/pointers (context_limit.go).
	slimPrompt := s.takeSlimRetry(in.ID)
	// Derived from this direction's own run journals, read-only: the executor
	// gets its own命令原文 as a reminder, never as a fact or a coverage claim.
	probes := s.probeSection(in, db)
	switch {
	case slimPrompt:
		prompt = buildPromptWithoutOptionalSummaries(db, in, brief, s.KBDir, s.Timeout, graphOK, focusOK, instructions, probes, s.ReviewsDisabled())
		if in.Handoff != nil {
			prompt += handoffSection(db, in, *in.Handoff, handoffAuth) + "\n\n" + handoffIndexNote
		}
		s.noteContextSlimRetry(runID,
			"harness 报告上下文超限：本次改用精简任务书（可选摘要省略，硬约束与交接契约保留；按本轮输入清单与 fact ID 读取）")
	case !keepPrompt:
		prompt = BuildPromptWithInstructionsVisible(db, in, brief, s.KBDir, s.Timeout, graphOK, focusOK,
			s.readWorkerNote(workDir), instructions, probes, s.ReviewsDisabled())
		if in.Handoff != nil {
			prompt += handoffSection(db, in, *in.Handoff, handoffAuth) + "\n\n" + handoffIndexNote
		}
	default:
		// A continued session still holds the task it was started with, and that
		// task moves: instructions get corrected, acceptance changes, a premise is
		// withdrawn, an operator steers the direction. The session's own history
		// keeps the old wording, so a model that reads only its history keeps
		// working on the task as it was then — the one failure mode nobody can see
		// from outside. The current brief is rebuilt and re-sent, declared
		// authoritative over history; the continuation text follows it.
		current := BuildPromptWithInstructionsVisible(db, in, brief, s.KBDir, s.Timeout, graphOK, focusOK,
			s.readWorkerNote(workDir), instructions, probes, s.ReviewsDisabled())
		if in.Handoff != nil {
			current += handoffSection(db, in, *in.Handoff, handoffAuth) + "\n\n" + handoffIndexNote
		}
		prompt = continuationBriefHeader + current + "\n\n## 本轮续跑\n" + prompt
	}
	// The injection boundary is journaled like the other injected sections: the
	// prompt lives only in the harness session, so without this event a later
	// reader cannot tell whether a round ran before or after the boundary
	// existed. Name and byte size only — never the text. The size comes from the
	// same function the brief renders, so the two cannot drift; it is pulled into
	// a variable because the golden log-anchor scan reads these call sites.
	boundaryBytes := len(untrustedInputSection())
	s.NoteInjected(runID, "boundary", "", boundaryBytes, "untrusted_input")
	// Sections that are appended to every dispatch (ready wait results, the
	// collaboration capability inventory, login material) are captured first so
	// the degraded rebuild keeps them.
	appendDispatchSections := func(p string) string {
		if waitSection != "" {
			p += waitSection
		}
		if collabSection != "" {
			p += collabSection
		}
		if accessSection != "" {
			p += accessSection
		}
		return p
	}
	prompt = appendDispatchSections(prompt)
	// 4zrcin does not predict the window: the harness owns feasibility,
	// compaction and overflow recovery. If it reports a context limit, the
	// post-run path retries once with the degraded brief (context_limit.go).
	if waitSection != "" {
		s.NoteInjected(runID, "wait", "", len(waitSection), "brief")
	}
	// Capability inventory (peers, runnable model tiers, where the collaboration
	// contract lives) — not tool instructions. A model that never reads the skill
	// cannot discover that it may delegate or spawn another tier; sapper puts the
	// same affordance in its mission text (docs/17 §3.2/§3.3).
	if collabSection != "" {
		s.NoteInjected(runID, "collaboration", "", len(collabSection), "brief")
	}
	if accessSection != "" {
		s.NoteInjected(runID, "access", "", len(accessSection), "brief")
	}
	// Messages addressed to this direction while it was not running ride the
	// prompt: queued is a promise that the next run carries it, not a mailbox
	// nobody opens. The section marks what it shows as accepted, so a message is
	// injected exactly once (D86). Its own caps (row count, byte budget) bound
	// it; 4zrcin does not shrink it against a window it cannot predict.
	if section := s.inboxSection(collab.ParticipantID(in.ID), "prompt:"+runID, runID, "", 0); section != "" {
		prompt += section
		// The prompt is not part of the ledger, so the injection itself is:
		// the run log can answer "this turn received N bytes of peer messages"
		// even after the session is gone (docs/17 §3.3).
		s.NoteInjected(runID, "message", "", len(section), "inbox")
	}
	// Every dispatch archives the mission text it was actually given, a first
	// dispatch and a continuation alike: after the fact, that file is the only
	// way to answer "what did this run believe it was asked". A continuation's
	// file carries the current brief plus the continuation, which is exactly what
	// the model received — keeping it out left the resumed run's task text
	// unrecorded while the session's older copy looked like the answer.
	if prompt != "" {
		if rel, err := s.archiveBrief(workDir, runID, prompt); err != nil {
			log.Printf("run %s：归档任务书失败：%v", runID, err)
		} else {
			_, _ = s.Store.UpdateRun(runID, func(r *model.Run) { r.BriefPath = rel })
		}
	}
	// A per-run identity is the collaboration credential: one token, one run,
	// revoked when the turn settles. It goes into the case (0600) rather than
	// the prompt or the host environment, because every run on a host shares
	// that environment. Without a project there is nothing to scope it to.
	if s.Collab != nil && s.Project != nil {
		ident, ierr := s.Collab.Issue(collab.Identity{
			ProjectID: s.Project.ID, ParticipantID: collab.ParticipantID(in.ID),
			IntentID: in.ID, RunID: runID, Generation: in.Attempts, APIBase: s.APIBase,
		})
		if ierr != nil {
			s.finish(runID, in.ID, model.RunFailed, "协作身份："+ierr.Error(), executor.Result{}, 0)
			return
		}
		if ierr := s.publishRunIdentity(workDir, ident); ierr != nil {
			s.finish(runID, in.ID, model.RunFailed, "写协作身份："+ierr.Error(), executor.Result{}, 0)
			return
		}
	}
	modelRecorded := false
	var executionObserved atomic.Bool
	// Liveness (final-e2e I1): a weak model can emit "empty assistant reply ->
	// llm_request" forever, which keeps the idle sweep from ever firing.
	// One detector per run, cancelled with the model_stall cause when it trips.
	stall := s.newRunStallDetector()
	stopCleanup := s.startCleanupWindow(ctx, runID, in.ID, workDir)
	defer stopCleanup()
	s.appendEventLogged(runID, "input", map[string]any{"kind": "input", "at": time.Now(), "text": prompt, "prompt_section": "task_brief"})
	res, runErr := s.Exec.Run(ctx, executor.Request{
		IntentID:    in.ID,
		Prompt:      prompt,
		Goal:        strings.TrimSpace(in.Title),
		WorkDir:     workDir,
		Node:        nodeID,
		Resume:      resume,
		Skills:      skillPaths,
		Eval:        in.Eval,
		MaxTurns:    in.MaxTurns,
		ScopeRules:  s.scopeRulesJSON(),
		GlobalFence: s.globalFenceJSON(),
		EgressProxy: s.egressProxyJSON(),
	}, func(ev executor.Event) {
		if ev.Kind == executor.KindToolCall || ev.Kind == executor.KindToolResult || ev.NativeOperation != nil {
			executionObserved.Store(true)
		}
		if err := s.appendRunEvent(runID, ev); err != nil {
			// The journal is opened before Exec.Run and closed after it, so a
			// failure here is a real write problem, not the settle race.
			log.Printf("run %s: 写事件失败：%v", runID, err)
		}
		// If the harness still exposes its native delegation tool, the ledger
		// cannot see the child session; record that once for the operator
		// (task-24). Only the structured tool name is examined.
		s.noteNativeSubagentUsage(runID, ev)
		// The harness reports the model it resolved for this session. That is
		// the actual model, which may differ from the profile's alias; record it
		// once so the run card never shows only a hopeful profile name. Only a
		// non-empty session event overwrites it (executor.SessionModel).
		if !modelRecorded {
			if name := executor.SessionModel(ev); name != "" {
				modelRecorded = true
				_, _ = s.Store.UpdateRun(runID, func(r *model.Run) { r.Model = name })
			}
		}
		// The pulse keeps one line of the event so the board says what the run
		// is doing, not just that it moved. The journal above stays the full
		// record; the decider reads its tail through run_events (D57).
		s.noteRunEvent(runID, time.Now(), ev.Kind, ev.Tool, ev.Text)
		if ev.ContextTokens > 0 {
			s.Store.ProgressRun(runID, ev.ContextTokens)
		}
		if ev.ContextWindow > 0 {
			s.Store.ProgressWindow(runID, ev.ContextWindow)
		}
		if stall.observe(ev) {
			s.noteModelStall(runID)
		}
	})

	status, reason := model.RunDone, ""
	stopCleanup()
	switch {
	case s.wasCanceled(runID):
		status = model.RunCanceled
		reason = s.cancelReason(runID)
		if reason == "" {
			reason = "已取消（人工或重派）"
		}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, reason = model.RunCanceled, fmt.Sprintf("超时 %s", s.Timeout)
		// Record the cause here instead of guessing it later from the reason
		// text: the structured cause is what the fleet/console reads, and
		// substring matching on Chinese prose was both fragile and exactly the
		// "recognise a word, patch the symptom" shape agent.md forbids.
		s.setCancelCause(runID, model.CancelCauseWatchdog)
		// A timeout must not resubmit itself: with --timeout shorter than the
		// work, cancel -> pending -> dispatchPending would loop forever. Hold it
		// and let the commander retry with a different budget/angle. A failed
		// write here silently restores that loop, so it is logged: the direction
		// would otherwise be re-dispatched every tick with nothing saying why.
		if _, err := s.Store.UpdateIntent(in.ID, func(i *model.Intent) {
			i.Hold = true
			i.HeldBy = model.HoldActorWatchdog
			i.HoldReason = reason
		}); err != nil {
			log.Printf("run %s 超时后 Hold 未写入：%v；意图 %s 会回到待派发，可能被反复重派", runID, err, in.ID)
		}
	case runErr != nil || res.ExitCode != 0:
		status = model.RunFailed
		reason = strings.TrimSpace(res.Output)
		if runErr != nil {
			reason = runErr.Error()
		}
		if reason == "" {
			reason = fmt.Sprintf("exit code %d", res.ExitCode)
		}
	case res.IsError || res.StopReason == "error":
		// The harness reports turn failures in the stream, not always in the
		// exit code (docs/11-protocol.md).
		status = model.RunFailed
		reason = strings.TrimSpace(res.Output)
		if reason == "" {
			reason = "harness 报告这一轮失败（stop_reason=" + res.StopReason + "）"
		}
	}
	// A successful run can still have something to say — an executor warning
	// that did not stop the turn (e.g. an AOP host that does not report its
	// working directory). It goes to the log, not the exit reason: the run did
	// what it was asked.
	if status == model.RunDone && strings.TrimSpace(res.Output) != "" {
		log.Printf("run %s（%s）：%s", runID, in.ID, s.redactSecrets(strings.TrimSpace(res.Output)))
	}

	// The live watcher stops before the finish path settles the same rows: from
	// here on OnFinish is the only writer, so its update-by-title cannot race
	// with the watcher's add-by-title.
	stopFacts()
	declared := s.Ingest.OnFinish(runID, in.ID, s.CaseRoot, workDir, started)
	// The finish ingest writes the authoritative fact set; route its changes
	// before the run settles so related directions are notified promptly.
	s.routeAndDeliver()
	// Warnings are also part of the run's event stream: after the fact,
	// `GET /api/runs/{id}/events` is where an operator looks, and the run row
	// alone would make them search the ledger. The journal is still open here.
	// The child-process memory sample goes first, so a >4 GiB warning it adds is
	// published by the flush below rather than dropped.
	s.recordChildPeaks(runID)
	s.flushRunWarnings(runID)
	// The exit reason is persisted in the ledger and shown in the console; an
	// executor that echoes a credential in its error text must not write it into
	// the ledger (task-13 secret boundary, same values as the journal).
	reason = s.redactSecrets(reason)
	// Before settlement: the cause lives on the lifecycle side and the journal is
	// the only record a review package keeps (see noteRunCancelCause).
	s.noteRunCancelCause(runID)
	s.finish(runID, in.ID, status, reason, res, declared)
	// One automatic recovery for a harness-reported context limit: retry once
	// with a degraded brief that keeps every hard constraint. Only a fresh
	// dispatch can benefit (a resumed session's history is what overflowed, and
	// harness compaction owns that); the degraded attempt itself is never
	// retried again (context_limit.go).
	if status == model.RunFailed && !resume && !keepPrompt && !slimPrompt &&
		contextLimitFailure(res, runErr, reason, s.providerErrorEvidence(runID)) {
		// A fresh request rejected before any tool can be rebuilt safely. A late
		// overflow or interrupted transport is not proof that no action happened.
		if runErr == nil && !executionObserved.Load() && res.UsageRequests <= 1 {
			s.retryWithSlimPrompt(runID, in.ID, reason)
		} else {
			s.noteContextSlimRetry(runID, "上下文超限前已有工具活动或执行结果不明：保留现场，未自动重跑；请核对动作与证据后显式续接。")
		}
	}
	// A resume that hit a host which no longer knows the session is the one
	// case where the memory is already gone (aopexec removed the marker). A
	// single fallback to a fresh session is explicit: the failed run records a
	// warning plus an event, and the new run keeps the direction. Every other
	// failure stays a failure for the operator/commander to decide.
	if status == model.RunFailed && resume && res.ResumeGone {
		s.fallbackToFreshSession(runID, in.ID)
	}
}

// fallbackToFreshSession is the one automatic step of a resume retry: the
// requested session is gone. Before falling back it asks the host's own
// recovery primitive (ResetSession) once; when that yields a replacement
// session the direction is retried on it, otherwise the same direction starts
// on a brand-new session with the full brief. It is never silent — the outcome,
// including why ResetSession could not help, is recorded on the old run
// (warning + `resume_fallback` journal event) before the new dispatch.
func (s *Server) fallbackToFreshSession(oldRunID, intentID string) {
	cur, ok := s.Store.GetIntent(intentID)
	if !ok || cur.Status != model.StatusFailed || cur.RunID != oldRunID {
		return // a newer run already took the direction
	}
	oldRun, _ := s.Store.GetRun(oldRunID)
	replacement, resetReason := s.resetGoneSession(context.Background(), oldRun)
	warning := "续跑失败：宿主已没有原会话（marker 失效）；"
	if replacement != "" {
		warning += "ResetSession 已建立替代会话，本轮在该会话上重试一次"
	} else {
		if resetReason == "" {
			resetReason = "未知原因"
		}
		warning += "ResetSession 无法恢复（" + resetReason + "），已自动改走重派新会话"
	}
	if _, err := s.Store.UpdateRun(oldRunID, func(r *model.Run) {
		appendRunWarning(r, warning)
	}); err != nil {
		log.Printf("run %s: 记录续跑回退警告失败：%v", oldRunID, err)
	}
	to := "fresh_session"
	if replacement != "" {
		to = "reset_session"
	}
	if err := s.appendEvent(oldRunID, map[string]any{
		"kind": "resume_fallback", "at": time.Now(), "intent_id": intentID,
		"from_run": oldRunID, "to": to, "replacement_session": replacement, "reason": resetReason,
	}); err != nil {
		log.Printf("run %s: 记录续跑回退事件失败：%v", oldRunID, err)
	}
	if n, err := s.Store.RedeliverAllUnconfirmed(collab.ParticipantID(intentID)); err != nil {
		log.Printf("续跑回退 %s: 重投未确认消息失败：%v", intentID, err)
	} else if n > 0 {
		log.Printf("续跑回退 %s: %d 条未确认消息回队", intentID, n)
	}
	// The retry after a successful ResetSession continues the replacement
	// session with the full brief (keepPrompt=false): the replacement is empty,
	// so the resumed turn must carry the task material, not just a delta.
	resume := replacement != ""
	prepared, err := s.Store.UpdateIntent(intentID, func(i *model.Intent) {
		if i.RunID != oldRunID || i.Status != model.StatusFailed {
			return
		}
		i.Status = model.StatusPending
		i.Hold = false
		i.ClaimedBy = ""
	})
	if err != nil {
		log.Printf("续跑回退 %s: 准备新会话失败：%v", intentID, err)
		return
	}
	runID, err := s.startRunWith(prepared.ID, "", "", resume, false)
	if err != nil {
		log.Printf("续跑回退 %s: 新会话启动失败：%v", intentID, err)
		return
	}
	log.Printf("run %s: 原会话已丢失（%s），已回退 %s run %s", oldRunID, resetReason, to, runID)
}

// finish writes the terminal state of a run and its intent.
//
// A cancellation returns the intent to pending without spending an attempt
// (docs/06, D19): the timeout is our policy, not the executor's mistake.
//
// Settlement also drops the run's collaboration credential before the status is
// published. Doing it in a defer in dispatch left a window where a run already
// read as finished still had a resolvable token and an identity.json in the
// case — an observer could pick up a credential for a run that was over.
func (s *Server) finish(runID, intentID, status, reason string, res executor.Result, declared int) {
	// Settlement is one transaction sequence owned by the lifecycle service:
	// revoke identity, write the run outcome, settle the intent, hold explicit
	// stops, requeue possibly-unread messages, then wake the hooks.
	s.Lifecycle.Finish(runID, intentID, status, reason, res, declared)
	// Declared facts from this run's last ingest may have committed after the
	// live watcher stopped; notify related directions now instead of waiting for
	// the next watch tick.
	s.routeAndDeliver()
	// An interrupted direction is restarted here, bounded, instead of waiting for
	// the commander to notice the same work is missing (P1, 2026-11-23 review).
	s.redispatchAfterCancel(runID, intentID)
}

// holdExplicitlyCanceledIntent and the canceled-run semantics it protects live
// in the lifecycle service: an explicit stop settles failed+Hold (final-e2e E7)
// while continuation causes keep their old settlement.

// Recover repairs what a crash leaves behind. At startup no goroutine owns any
// run, so a run recorded as running is necessarily an orphan.
//
// Recover settles rows only. The AOP session pointers (`.zrcin/aop-session.json`)
// that a restart invalidates are not its job: they belong to the host layer and
// are swept once at startup (and once per endpoint change) by
// host.SweepStaleSessions, before projects attach — see watchdog-liveness §4.2
// for why scanning the case tree in every Recover would be wasteful and why the
// endpoint comparison belongs to whoever knows the live host.
func (s *Server) Recover() {
	// Crash repair is owned by the lifecycle service. Snapshot the orphan rows
	// first so the Phase 1 sidecar sees the same restart cancels the ledger does:
	// the lifecycle sweep writes rows directly and has no recorder handle.
	var live []model.Run
	if s.Metrics != nil && s.Store != nil {
		for _, r := range s.Store.Runs() {
			if r.Status == model.RunRunning {
				live = append(live, r)
			}
		}
	}
	s.Lifecycle.Recover()
	// The runs this sweep just settled were killed with the previous process, so
	// their journals end mid-flight and cannot say why. Write the cause into each
	// one: "restart" is the answer a review needs, and without this line the only
	// possible reading is "something killed it" (20 of the 49 cancelled runs in the
	// TSecBench 2026-11-22 package looked exactly like that).
	for _, before := range live {
		cur, ok := s.Store.GetRun(before.ID)
		if !ok || cur.Status != model.RunCanceled {
			continue
		}
		if err := s.appendJournalOnDisk(before.ID, map[string]any{
			"kind": "run_cancel", "at": time.Now(), "run_id": before.ID,
			"cause": model.CancelCauseRestart, "reason": s.redactSecrets(cur.ExitReason), "source": "recover",
		}); err != nil {
			log.Printf("run %s: 记录重启取消原因失败：%v", before.ID, err)
		}
	}
	if s.Metrics != nil {
		for _, before := range live {
			cur, ok := s.Store.GetRun(before.ID)
			if !ok || cur.Status != model.RunCanceled {
				continue
			}
			s.Metrics.Cancel(before.ID, model.CancelCauseRestart, cur.ExitReason, before.StopReason)
		}
	}
}

// watchInterval is the guard's cadence. It is exported to the console as
// observation (board.guard), so the page never hardcodes a number that could
// drift from the loop.
const watchInterval = 30 * time.Second

// Watch runs the watchdog: intents whose dispatcher disappeared go back to
// pending so the next decision round can pick them up.
func (s *Server) Watch(ctx context.Context) {
	// Sweep once before the first tick: a process that was down for a while
	// would otherwise keep dead queue rows for another full interval, and
	// "restart then immediately read the queue" is exactly when that shows.
	s.sweepMessages()
	s.routeAndDeliver()
	t := time.NewTicker(watchInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.lastWatch.Store(time.Now().UnixNano())
			s.reapOrphans()
			s.reapIdle()
			s.reapClaims()
			s.reapDanglingIntents()
			// The scheduler is also the wait-deadline tick: pump evaluates stored
			// waits and starts unstarted children that were waiting for a slot.
			s.pump()
			// Queue cleanup is a tick too: a queued message that no run is going
			// to carry expires instead of growing the ledger forever.
			s.sweepMessages()
			s.routeAndDeliver()
		}
	}
}

func (s *Server) reapOrphans() {
	// The watchdog sweep is owned by the lifecycle service.
	s.Lifecycle.ReapOrphans()
}

// reapIdle cancels a run that has emitted nothing for IdleTimeout. It holds the
// intent before cancelling, so the "canceled -> pending" transition (D19) does
// not immediately restart the same stuck direction; the commander sees a held
// direction with the idle reason and chooses retry/park.
func (s *Server) reapIdle() {
	// The idle timeout is the API's setting; the sweep is lifecycle's.
	s.Lifecycle.ReapIdle(s.IdleTimeout)
}

// reapClaims returns a claimed intent whose dispatcher vanished (AddRun or the
// goroutine failed after the claim) to pending. A claim with a live run is left
// alone: that is the normal running transition.
func (s *Server) reapClaims() {
	s.Lifecycle.ReapClaims()
}

// reapDanglingIntents repairs the other half of a crash between finish's two
// writes: a run reached a terminal state, but its intent stayed running. None of
// the other reap paths touch that shape (they key on a running run or a claimed
// intent), so the direction was stuck forever: run says "already running",
// cancel says "not running", park/retry/reopen all refuse it.
//
// The one-minute grace keeps it away from a finish that is between UpdateRun
// and UpdateIntent right now.
func (s *Server) reapDanglingIntents() {
	s.Lifecycle.ReapDanglingIntents()
}
