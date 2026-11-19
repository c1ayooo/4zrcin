package model

import "testing"

// The grade is a label, so the write path accepts exactly four values and never
// guesses a neighbour for anything else. Read paths must not have to re-map.
func TestNormalizeSeverityKeepsOnlyTheFourGrades(t *testing.T) {
	cases := map[string]string{
		"critical": SeverityCritical,
		" HIGH ":   SeverityHigh,
		"Medium":   SeverityMedium,
		"low":      SeverityLow,
		"info":     "",
		"unknown":  "",
		"severe":   "",
		"":         "",
	}
	for in, want := range cases {
		if got := NormalizeSeverity(in); got != want {
			t.Fatalf("NormalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

// SeverityRank is the one order the server hands out; ungraded sorts below low
// and is never folded into it.
func TestSeverityRankOrdersUngradedLast(t *testing.T) {
	order := []string{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, ""}
	for i := 1; i < len(order); i++ {
		if SeverityRank(order[i-1]) <= SeverityRank(order[i]) {
			t.Fatalf("rank(%q) must be greater than rank(%q)", order[i-1], order[i])
		}
	}
	if SeverityRank("severe") != 0 {
		t.Fatal("an unknown value must rank as ungraded, not as a tier")
	}
}
