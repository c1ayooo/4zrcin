package api

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// A temporary pause stops review runs; it must not relax what a direction is
// allowed to dispatch on. Only the instance-level --no-verify switch does that,
// because a paused review can resume and a direction frozen without its promised
// review would be silently downgraded.
func TestReadinessBypassNeedsTheInstanceSwitchNotATemporaryPause(t *testing.T) {
	s, p, f, _ := proofPolicyFixture(t)
	in, err := p.Store.CreateIntent(model.Intent{Title: "无核验前提", FromFactIDs: []string{f.ID}})
	if err != nil {
		t.Fatal(err)
	}
	s.SetReviewsPaused(true)
	if got := s.readiness(p.Store.Snapshot(), in, s.premisePolicy()); got.Ready {
		t.Fatalf("a paused review relaxed premise admission: %+v", got)
	}
	s.SetReviewsPaused(false)
	s.SetReviewsDisabled(true)
	if got := s.readiness(p.Store.Snapshot(), in, s.premisePolicy()); !got.Ready {
		t.Fatalf("reviews disabled still blocked the direction: %+v", got)
	}
}

// With review off the text of an unverified claim is shown, but a withdrawn or
// rejected claim must never become readable because a switch was flipped: those
// statuses are recorded verdicts, not "waiting for review".
func TestRenderFactHidesRetractedClaimsEvenWhenReviewIsOff(t *testing.T) {
	for _, status := range []string{"withdrawn", "rejected", "missing", ""} {
		f := model.Fact{ID: "f_" + status, Kind: "vuln", Status: status, Title: "TITLE-" + status, Summary: "SUMMARY"}
		got := renderFact(f, true)
		if strings.Contains(got, "TITLE-") || strings.Contains(got, "SUMMARY") {
			t.Fatalf("status %q leaked its text with review off: %q", status, got)
		}
	}
	unverified := model.Fact{ID: "f_u", Kind: "vuln", Status: "unverified", Title: "TITLE-U", Summary: "SUMMARY-U"}
	if got := renderFact(unverified, true); !strings.Contains(got, "TITLE-U") {
		t.Fatalf("unverified claim hidden with review off: %q", got)
	}
}

// The commander round used to say "verified_only, wait for the current proof" on
// every round, including rounds where review was off. That describes a queue that
// cannot move: the switch must change the admission wording, not only the gate.
func TestPremisePolicyLineFollowsTheReviewSwitch(t *testing.T) {
	off := premisePolicyLine("verified_only", true)
	if !strings.Contains(off, "复核已关闭") || strings.Contains(off, "本轮前提政策") {
		t.Fatalf("review-off line still describes verified_only: %q", off)
	}
	if !strings.Contains(off, "不要等待复核") {
		t.Fatalf("review-off line does not stop the waiting instruction: %q", off)
	}
	on := premisePolicyLine("verified_only", false)
	if !strings.Contains(on, "本轮前提政策：verified_only") || strings.Contains(on, "复核已关闭") {
		t.Fatalf("review-on line changed: %q", on)
	}
}

// With review off a direction must not be shown as "waiting for review": there is
// no queue, so readiness reports the premise usable and attaches no review state.
func TestReadinessAttachesNoReviewStateWhenReviewIsOff(t *testing.T) {
	s, p, f, _ := proofPolicyFixture(t)
	in, err := p.Store.CreateIntent(model.Intent{Title: "无核验前提", FromFactIDs: []string{f.ID}})
	if err != nil {
		t.Fatal(err)
	}
	s.SetReviewsDisabled(true)
	got := s.readiness(p.Store.Snapshot(), in, s.premisePolicy())
	if !got.Ready {
		t.Fatalf("review-off readiness blocked: %+v", got)
	}
	for _, row := range got.Premises {
		if row.Review != nil {
			t.Fatalf("review-off premise still carries a review state: %+v", row)
		}
	}
	if got.ReviewHint != "" {
		t.Fatalf("review-off readiness still explains a queue: %q", got.ReviewHint)
	}
}
