package board

import (
	"strings"
	"testing"
)

// Only a verified assertion carries its text into the decision surface. An
// unverified row keeps its id, kind and status so the model knows verification is
// pending, but the prose stays out: reading it is how a wrong observation becomes
// the next direction's belief without ever being cited as a premise.
func TestForPromptWithholdsUnverifiedFactText(t *testing.T) {
	v := View{
		RecentFacts: []Item{
			{ID: "f_ok", Kind: "vuln", Status: "verified", Title: "已核验结论", Summary: "证据确凿"},
			{ID: "f_pending", Kind: "service", Status: "unverified", Title: "模型写的产品名", Summary: "模型写的解释", SourceRef: "private-ref", Evidence: 3, Refs: 2},
		},
		Ports:    []PortGroup{{Port: "1234", Members: []string{"raw service title"}}},
		Coverage: []CoverageItem{{Host: "raw host title"}},
	}
	got := ForPrompt(v)
	if len(got.RecentFacts) != 2 {
		t.Fatalf("prompt facts = %d, want both rows kept as placeholders", len(got.RecentFacts))
	}
	ok := got.RecentFacts[0]
	if ok.Title != "已核验结论" || ok.Summary != "证据确凿" {
		t.Fatalf("verified row lost its text: %+v", ok)
	}
	pending := got.RecentFacts[1]
	if pending.ID != "f_pending" || pending.Kind != "service" || pending.Status != "unverified" {
		t.Fatalf("placeholder lost its identity: %+v", pending)
	}
	if pending.Title != "" || strings.Contains(pending.Summary, "模型写的") {
		t.Fatalf("unverified text leaked into the prompt: %+v", pending)
	}
	if pending.Evidence != 0 || pending.Refs != 0 || pending.SourceRef != "" {
		t.Fatalf("placeholder still exposes provenance: %+v", pending)
	}
	if len(got.Coverage) != 0 || len(got.Ports) != 0 {
		t.Fatal("prompt carries unfiltered fact aggregates")
	}
	// The console view is untouched: the operator still reads the full row.
	if v.RecentFacts[1].Title != "模型写的产品名" || v.RecentFacts[1].Evidence != 3 {
		t.Fatalf("ForPrompt mutated the input view: %+v", v.RecentFacts[1])
	}
}

// A note is never reviewable, so it never becomes verified: it must not ride the
// decision surface either.
func TestForPromptWithholdsNotes(t *testing.T) {
	v := View{RecentFacts: []Item{{ID: "f_note", Kind: "note", Status: "unverified", Title: "线索笔记", Summary: "待复核假设"}}}
	got := ForPrompt(v).RecentFacts[0]
	if got.Title != "" || got.Summary != "待核验：内容不进入决策上下文" {
		t.Fatalf("note rode the prompt: %+v", got)
	}
}

// With review switched off there is no verdict to wait for: the unverified row
// keeps its text (status still unverified), while withdrawn rows and notes stay
// redacted — a switch must not make a retracted claim readable.
func TestForPromptOpenShowsUnverifiedText(t *testing.T) {
	v := View{RecentFacts: []Item{
		{ID: "f_u", Kind: "vuln", Status: "unverified", Title: "T", Summary: "S"},
		{ID: "f_w", Kind: "vuln", Status: "withdrawn", Title: "W", Summary: "WS"},
		{ID: "f_n", Kind: "note", Status: "unverified", Title: "N", Summary: "NS"},
	}}
	if got := ForPrompt(v).RecentFacts[0]; got.Title != "" || got.Summary != "待核验：内容不进入决策上下文" {
		t.Fatalf("review-on projection changed: %+v", got)
	}
	got := ForPromptOpen(v).RecentFacts
	if got[0].Title != "T" || got[0].Summary != "S" {
		t.Fatalf("review-off projection hid an unverified fact: %+v", got[0])
	}
	if got[1].Title != "" || got[2].Title != "" {
		t.Fatalf("withdrawn or note text leaked with review off: %+v", got)
	}
}
