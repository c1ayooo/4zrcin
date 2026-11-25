package decider

import (
	"strings"
	"testing"
)

// The reviews-on/off rule pairs are shared constants composed into the base
// prompt (not implicit strings.Replace anchors). The prompt therefore cannot
// drift away from the replacement anchors — but the composition itself must be
// pinned: every on-rule appears exactly once in the base, every off-rule only
// in the reviews-off variant, and vice versa.
func TestCommanderIdentityRulePairsAreExclusive(t *testing.T) {
	base := commanderIdentityBase
	off := commanderIdentityFor(true)

	for _, pair := range []struct{ on, off string }{
		{ruleReviewsWaitReview, ruleReviewsReadiness},
		{ruleReviewsPlaceholder, ruleReviewsRecentFacts},
		{ruleReviewsAssumption, ruleReviewsUnverified},
	} {
		if n := strings.Count(base, pair.on); n != 1 {
			t.Fatalf("reviews-on rule appears %d times in the base prompt (want exactly 1): %q", n, pair.on)
		}
		if strings.Contains(base, pair.off) {
			t.Fatalf("reviews-off rule leaked into the reviews-on prompt: %q", pair.off)
		}
		if n := strings.Count(off, pair.off); n != 1 {
			t.Fatalf("reviews-off rule appears %d times in the reviews-off prompt (want exactly 1): %q", n, pair.off)
		}
		if strings.Contains(off, pair.on) {
			t.Fatalf("reviews-on rule survived the swap in the reviews-off prompt: %q", pair.on)
		}
	}
	if commanderIdentityFor(false) != base {
		t.Fatal("reviews-on identity must be the base prompt verbatim")
	}
}
