package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// ---- reviews ----

// verifyFact starts one review asynchronously: a verifier run is a real
// executor run and can take minutes, so the caller gets 202 and watches the
// fact status change.
func (s *Server) verifyFact(w http.ResponseWriter, r *http.Request) {
	if s.ReviewsDisabled() {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "复核已关闭：不会再有复核 run，未核验事实按原文可见", "code": "verify_disabled",
		})
		return
	}
	if s.Verifier == nil {
		writeErr(w, 503, "复核未启用")
		return
	}
	if s.deleting.Load() {
		writeErr(w, http.StatusConflict, "项目正在删除：不能复核")
		return
	}
	if s.ReviewsPaused() {
		writeErr(w, http.StatusConflict, "复核已暂停：先点「继续复核」，项目暂停不影响复核")
		return
	}
	id := r.PathValue("id")
	f, ok := s.Store.FactByID(id)
	if !ok {
		writeErr(w, 404, "fact not found")
		return
	}
	// Say what is missing instead of the generic policy refusal: the operator can
	// fix "no evidence" by attaching the material, not by changing the policy.
	if reason := s.Store.ReviewPassBlocked(f); reason != "" {
		writeErr(w, 409, reason)
		return
	}
	if !s.Verifier.CanReview(f) {
		writeErr(w, 409, "当前政策不支持复核此类事实，或该断言已有核验结论")
		return
	}
	if s.Verifier.Busy(id) {
		writeErr(w, 409, "这条事实正在复核中")
		return
	}
	// Start claims before returning, so a concurrent request cannot also get 202
	// and then lose the race inside the verifier.
	if err := s.Verifier.Start(s.Ctx, id, func() {
		// A verdict moves the frontier: a rejected fact can block the direction
		// that derives from it, so the next round has to see it (D52).
		if s.Decider != nil {
			s.wakeCommanderIfNeeded("review")
		}
	}); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"queued": true, "fact_id": id})
}

// verifyPendingFacts queues every assertion admitted by the current review
// policy, still unverified and not already being reviewed. Manual batches and
// automatic passes share the auxiliary model admission gate. This is the
// operator's explicit batch path alongside automatic background review.
func (s *Server) verifyPendingFacts(w http.ResponseWriter, r *http.Request) {
	if s.ReviewsDisabled() {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "复核已关闭：不会再有复核 run，未核验事实按原文可见", "code": "verify_disabled",
		})
		return
	}
	if s.Verifier == nil {
		writeErr(w, 503, "复核未启用")
		return
	}
	if s.deleting.Load() {
		writeErr(w, http.StatusConflict, "项目正在删除：不能复核")
		return
	}
	if s.ReviewsPaused() {
		writeErr(w, http.StatusConflict, "复核已暂停：先点「继续复核」，项目暂停不影响复核")
		return
	}
	s.verifyBatchMu.Lock()
	if s.verifyBatch {
		s.verifyBatchMu.Unlock()
		writeErr(w, 409, "已有批量复核在跑：等它结束，或在漏洞页单条复核")
		return
	}
	ids := make([]string, 0, 8)
	skipped := 0
	for _, f := range s.Store.ListFacts("", 0) {
		if !s.Verifier.CanReview(f) {
			continue
		}
		if s.Verifier.Busy(f.ID) {
			skipped++
			continue
		}
		ids = append(ids, f.ID)
	}
	if len(ids) == 0 {
		s.verifyBatchMu.Unlock()
		writeJSON(w, 200, map[string]any{"queued": 0, "skipped": skipped})
		return
	}
	s.verifyBatch = true
	s.verifyBatchMu.Unlock()

	go func() {
		defer func() {
			s.verifyBatchMu.Lock()
			s.verifyBatch = false
			s.verifyBatchMu.Unlock()
		}()
		for _, id := range ids {
			// Review pause is the review's own switch, not the project's: a
			// paused engagement can still run the reviews an operator asked for.
			// Delete/shutdown stops everything; a live review is canceled through
			// reviewRuns, the rest never start.
			if s.ReviewsPaused() || s.ReviewsDisabled() || s.deleting.Load() || s.closing.Load() {
				return
			}
			if s.Ctx != nil && s.Ctx.Err() != nil {
				return
			}
			_, _ = s.Verifier.Run(s.Ctx, id)
		}
		if s.Decider != nil {
			s.wakeCommanderIfNeeded("review")
		}
	}()
	writeJSON(w, 202, map[string]any{"queued": len(ids), "skipped": skipped})
}

// ReviewsPaused reports the review kill switch.
func (s *Server) ReviewsPaused() bool { return s.reviewsPaused.Load() }

// ReviewsDisabled reports the runtime review switch. It starts as --no-verify at
// boot and the operator can open/close it per project while the instance runs
// (POST /api/verifier/disable, /api/verifier/enable). It is not the temporary
// pause: a pause refuses review runs for now, a disabled review cannot run at all
// — no verdict will arrive, so unverified text stays readable and premise
// admission relaxes.
func (s *Server) ReviewsDisabled() bool { return s.verifyDisabled.Load() }

// SetReviewsDisabled flips the runtime review switch. Opening it is the
// operator's decision, so the caller re-arms the pending work (verifierEnable);
// closing it also cancels live reviews (verifierDisable).
func (s *Server) SetReviewsDisabled(disabled bool) { s.verifyDisabled.Store(disabled) }

// SetReviewBootDisabled records the instance boot value (--no-verify). It is
// evidence for exports, not a second switch: ReviewsDisabled stays the only
// thing that governs whether a verifier may run.
func (s *Server) SetReviewBootDisabled(disabled bool) { s.reviewBootDisabled = disabled }

// ReviewBootDisabled reports the boot value as recorded by SetReviewBootDisabled.
func (s *Server) ReviewBootDisabled() bool { return s.reviewBootDisabled }

// persistReviewsDisabled records the operator's switch in the project manifest.
// The runtime atomic is the live value; the manifest is what survives a restart,
// so a project whose review the operator opened does not fall back to the
// instance boot flag on the next start.
func (s *Server) persistReviewsDisabled(disabled bool) error {
	if s.Project == nil {
		return nil
	}
	if err := s.Project.SetReviewsDisabled(disabled); err != nil {
		log.Printf("保存项目复核开关失败：%v", err)
		return err
	}
	return nil
}

// reviewsOff is the one read for "no review run can start here": the runtime
// switch is closed, or a bare test server has no verifier at all. A temporary
// pause is deliberately not part of it — admission and prompt visibility only
// relax when no verdict can ever arrive.
func (s *Server) reviewsOff() bool { return s.ReviewsDisabled() || s.Verifier == nil }

// SetReviewsPaused flips the review kill switch. It is independent of the
// project pause: a paused project can still review, and reviews can be paused
// while the commander keeps working (D137). A pause suspends the fixed-review
// phases (they publish no verdict), so turning the switch back on must re-arm the
// directions that were left in reviewing — otherwise an operator's emergency
// stop would silently strand every direction the phase owned.
func (s *Server) SetReviewsPaused(paused bool) {
	if s.Lifecycle != nil {
		s.Lifecycle.SetReviewsPaused(paused)
	} else {
		s.reviewsPaused.Store(paused)
	}
	if !paused {
		s.resumeReviewPhases()
	}
}

func (s *Server) verifierStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		// enabled keeps its old meaning ("a review run can start here"); disabled
		// says why when it is false — the runtime switch, not a temporary pause.
		"enabled":  !s.reviewsOff(),
		"paused":   s.ReviewsPaused(),
		"disabled": s.ReviewsDisabled(),
	})
}

// verifierDisable closes reviews for this project at runtime. Every live review
// run is canceled, the automatic pump and the fixed settlement review stop, and
// unverified facts keep their text (the same relaxed admission --no-verify always
// had). Idempotent: closing a closed switch answers 200 with canceled=0.
func (s *Server) verifierDisable(w http.ResponseWriter, r *http.Request) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	// Persist first: a switch the operator is told succeeded must survive a
	// restart, so a write failure is reported instead of being logged and lost.
	// The state named in the error is the one that actually remains — an
	// idempotent click on a closed switch must not claim it is still open.
	already := s.ReviewsDisabled()
	if err := s.persistReviewsDisabled(true); err != nil {
		state := "复核仍是开启状态"
		if already {
			state = "复核仍为关闭状态"
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "保存复核开关失败，" + state + "：" + err.Error(),
			"code":  "reviews_persist_failed",
		})
		return
	}
	canceled := s.closeReviewsLocked()
	writeJSON(w, http.StatusOK, map[string]any{"disabled": true, "canceled": canceled})
}

// verifierEnable opens the switch again and re-arms what the close suspended: the
// durable candidate set is scheduled and every direction left in reviewing
// resumes its fixed phase. Idempotent: enabling an open switch touches nothing,
// in particular it must not clear an operator's temporary pause.
func (s *Server) verifierEnable(w http.ResponseWriter, r *http.Request) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if err := s.persistReviewsDisabled(false); err != nil {
		state := "复核仍是关闭状态"
		if !s.ReviewsDisabled() {
			state = "复核仍为开启状态"
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "保存复核开关失败，" + state + "：" + err.Error(),
			"code":  "reviews_persist_failed",
		})
		return
	}
	if s.ReviewsDisabled() {
		s.SetReviewsDisabled(false)
		// Closing the switch suspended reviews; opening it clears that suspension
		// so "开启复核" means reviews run again instead of staying paused.
		s.SetReviewsPaused(false)
	}
	s.SchedulePendingReviews()
	s.wakeReviewPump()
	writeJSON(w, http.StatusOK, map[string]any{"disabled": false})
}

// closeReviewsLocked is the runtime half of closing the review switch, shared by
// the project page's disable endpoint and the project settings write so both mean
// the same thing: the switch closes, the pump and the fixed-review phases are
// suspended, and live review runs are canceled with the review-pause cause. A
// canceled review must not publish review_failed — no verdict arrived. The caller
// holds controlMu and has persisted the choice already.
func (s *Server) closeReviewsLocked() int {
	already := s.ReviewsDisabled()
	s.SetReviewsDisabled(true)
	// The pump, the fixed-review phases and the crash reaper read the pause gate:
	// closing it is a suspension, never a verdict. Resume is refused while
	// disabled, so this pause cannot be cleared behind the close's back.
	if !already {
		s.SetReviewsPaused(true)
	}
	canceled := 0
	if s.Lifecycle != nil {
		canceled = s.CancelReviewsWithCause(model.CancelCauseReviewPause, "复核已关闭：取消正在跑的复核")
	}
	s.wakeReviewPump()
	return canceled
}

// openReviewsLocked is the runtime half of opening it: the switch opens, the
// suspension the close set is cleared (only when it was closed, so an operator's
// temporary pause survives an unrelated save), the durable candidate set is
// re-armed and the pump is woken. Caller holds controlMu.
func (s *Server) openReviewsLocked() {
	if s.ReviewsDisabled() {
		s.SetReviewsDisabled(false)
		s.SetReviewsPaused(false)
	}
	s.SchedulePendingReviews()
	s.wakeReviewPump()
}

// SetReviewsOverride persists the operator's per-project review switch and moves
// the live switch in the same call. disabled==nil clears the project's own value,
// so the project follows the instance default again; the runtime then takes
// defaultDisabled — the value the instance resolved right now — so clearing the
// override cannot leave the project running the value it just stopped storing.
// It is the settings-write half of the project page's enable/disable endpoints:
// closing cancels live reviews and records the cause, opening re-arms the queue.
func (s *Server) SetReviewsOverride(disabled *bool, defaultDisabled bool) (int, error) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.Project == nil {
		return 0, errors.New("没有项目上下文，无法保存复核开关")
	}
	var err error
	if disabled == nil {
		err = s.Project.ClearReviewsDisabled()
	} else {
		err = s.Project.SetReviewsDisabled(*disabled)
	}
	if err != nil {
		// Same contract as the endpoints: the switch the console is told about
		// must survive a restart, so a failed write is reported, not swallowed.
		log.Printf("保存项目复核开关失败：%v", err)
		return 0, err
	}
	live := defaultDisabled
	if disabled != nil {
		live = *disabled
	}
	if live {
		return s.closeReviewsLocked(), nil
	}
	s.openReviewsLocked()
	return 0, nil
}

// verifierPause stops reviews without stopping the engagement: live verifier
// runs are canceled with the review cause, the commander and executor runs are
// untouched, and the next verify request is refused until resume.
func (s *Server) verifierPause(w http.ResponseWriter, r *http.Request) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	s.SetReviewsPaused(true)
	canceled := s.CancelReviewsWithCause(model.CancelCauseReviewPause, "复核暂停：取消正在跑的复核")
	writeJSON(w, http.StatusOK, map[string]any{"paused": true, "canceled": canceled})
}

// verifierResume re-arms reviews. It is explicit and does not wake the
// commander: the two switches are separate.
func (s *Server) verifierResume(w http.ResponseWriter, r *http.Request) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.reviewsOff() {
		// Reviews are closed (--no-verify at boot, or the operator's disable
		// switch): there is nothing to resume. Re-opening the pause would flip the
		// prompt back to hiding unverified text while no review can ever run, so
		// the two states would disagree. Opening reviews is the enable endpoint.
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "复核已关闭（实例默认或本项目关闭）：没有可恢复的复核", "code": "verify_disabled",
		})
		return
	}
	s.SetReviewsPaused(false)
	s.SchedulePendingReviews()
	writeJSON(w, http.StatusOK, map[string]any{"paused": false})
}

// reviewFact records a human verdict. A person may only say "this does not
// hold" (reject) or "put it back" (needs_evidence): confirmed is earned by a
// verifier rerun, never asserted (docs/07, D41). A verdict without a reason is
// how work silently disappears, so notes are required.
func (s *Server) reviewFact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, ok := s.Store.FactByID(id)
	if !ok {
		writeErr(w, 404, "fact not found")
		return
	}
	if f.Kind != "vuln" && f.Kind != "cred" {
		writeErr(w, 409, "只有会进报告的结论（vuln / cred）需要判定")
		return
	}
	var body struct {
		Verdict string `json:"verdict"`
		Notes   string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体不是合法 JSON")
		return
	}
	switch body.Verdict {
	case "reject", "needs_evidence":
	default:
		writeErr(w, 400, "人工只能判定不成立（reject）或退回未验证（needs_evidence）；已证实必须由复核重跑得出")
		return
	}
	notes := strings.TrimSpace(body.Notes)
	if notes == "" {
		writeErr(w, 400, "人工判定必须写理由")
		return
	}
	rev, err := s.Store.ReviewFact(model.Review{FactID: id, Verdict: body.Verdict, Notes: notes})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// A verdict changes the fact row; related directions should learn the new
	// version now rather than on the next watch tick.
	s.routeAndDeliver()
	writeJSON(w, 200, map[string]any{"review": rev})
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	factID := r.URL.Query().Get("fact")
	writeJSON(w, 200, map[string]any{"reviews": s.Store.Reviews(factID)})
}
