package api

import (
	"context"
	"log"
	"time"
)

func (s *Server) autoVerify(intentID string) {
	ctx := s.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.autoVerifyWithContext(ctx, intentID)
}

// autoVerifyWithContext is the pump's form: the pump owns the review context,
// so a pause/close can cancel the verifier run instead of waiting for it.
func (s *Server) autoVerifyWithContext(ctx context.Context, intentID string) {
	if ctx == nil {
		ctx = context.Background()
	}
	// The review switch also covers the automatic path: a paused review set
	// must not spend tokens just because a run happened to finish.
	if s.Store == nil || s.reviewsOff() || !s.automaticReviewsEnabled() || s.ReviewsPaused() || s.deleting.Load() || s.closing.Load() {
		return
	}
	start := s.Store.ReviewCursor(intentID)
	facts, _ := s.Store.ReviewCandidates(intentID, start, autoVerifyBatch, s.reviewPolicy())
	if len(facts) == 0 {
		return
	}
	cursor := start
	reviewed, busy, cooling, failed := 0, 0, 0, 0
	for _, f := range facts {
		// Stop before the first candidate this pass did not reach: the cursor
		// stays on the last handled candidate, so a resumed pass continues with
		// this one instead of skipping it.
		if !s.automaticReviewsEnabled() || s.ReviewsPaused() || s.reviewsOff() || s.deleting.Load() || s.closing.Load() || ctx.Err() != nil {
			break
		}
		previous := cursor
		cursor = f.ID
		if s.Verifier.Busy(f.ID) {
			busy++
			continue
		}
		now := time.Now()
		if wait, ok := s.reviewCoolingDown(f, now); ok {
			cooling++
			log.Printf("自动复核 %s（%s）：上次未得到可用结论，冷却中（约 %s 后重试）",
				f.ID, clipRunes(f.Title, 60), wait.Round(time.Second))
			continue
		}
		review, err := s.Verifier.Run(ctx, f.ID)
		if err != nil {
			if s.ReviewsPaused() || s.reviewsOff() || s.deleting.Load() || s.closing.Load() || ctx.Err() != nil {
				cursor = previous
				break
			}
			failed++
			s.noteReviewFailure(f, time.Now())
			log.Printf("自动复核 %s（%s）失败，进入冷却：%v", f.ID, clipRunes(f.Title, 60), err)
			continue
		}
		if review.Verdict != "pass" && review.Verdict != "reject" {
			// needs_evidence: the run happened but the fact is still unverified.
			// Treat it as a failed attempt for retry budgeting, otherwise every
			// wake would pay for the same unsuccessful review again.
			failed++
			s.noteReviewFailure(f, time.Now())
			log.Printf("自动复核 %s（%s）：结论 %q 未改变事实状态，进入冷却（%s）",
				f.ID, clipRunes(f.Title, 60), review.Verdict, reviewRetryCooldown)
			continue
		}
		s.clearReviewFailure(f)
		reviewed++
	}
	if err := s.Store.SetReviewCursor(intentID, cursor); err != nil {
		log.Printf("自动复核 %s：记录复核游标失败：%v", intentID, err)
	}
	if reviewed+busy+cooling+failed > 0 {
		log.Printf("自动复核 %s：本批 %d 个候选，%d 个得到结论，%d 个未得结论（冷却），%d 个忙碌跳过，%d 个冷却跳过",
			intentID, len(facts), reviewed, failed, busy, cooling)
	}
	if reviewed > 0 && s.Decider != nil {
		s.wakeCommanderIfNeeded("review")
	}
	if reviewed > 0 {
		go s.pump()
	}
}
