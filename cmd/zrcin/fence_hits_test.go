package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/settings"
	"github.com/c1ayooo/4zrcin/internal/traffic"
)

// fenceHitProject writes one project tree with a domain capture and a run
// journal carrying a command refusal.
func fenceHitProject(t *testing.T, root string) *project.Project {
	t.Helper()
	dir := filepath.Join(root, "p1")
	caseDir := filepath.Join(dir, "case")
	httpDir := filepath.Join(caseDir, ".http")
	runDir := filepath.Join(dir, "runs")
	for _, d := range []string{httpDir, runDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	capture := traffic.Capture{
		Ref: "in_1/cap.json", RunID: "run_1", URL: "https://evil.example/x",
		At: at.Add(time.Minute), GuardrailAction: "block", GuardrailRuleID: "global:gf-1",
	}
	raw, _ := json.Marshal(capture)
	if err := os.WriteFile(filepath.Join(httpDir, "cap.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ruleCapture := traffic.Capture{
		Ref: "in_1/cap2.json", RunID: "run_1", URL: "https://out.example/y",
		At: at.Add(2 * time.Minute), GuardrailAction: "review", GuardrailRuleID: "sr-2",
	}
	raw, _ = json.Marshal(ruleCapture)
	if err := os.WriteFile(filepath.Join(httpDir, "cap2.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	events := []executor.Event{
		{Kind: executor.KindGuardrailDecision, At: at.Add(3 * time.Minute), GuardrailAction: "block",
			GuardrailReason: "global.gf-9（禁止下载）", Text: "guardrail block：global.gf-9（禁止下载）"},
		{Kind: executor.KindGuardrailReview, At: at.Add(4 * time.Minute), GuardrailAction: "block",
			GuardrailState: "pending", GuardrailReason: "global.gf-9（禁止下载）",
			GuardrailTool: "bash", Text: "guardrail review bash：global.gf-9（禁止下载）"},
		{Kind: executor.KindGuardrailDecision, At: at.Add(5 * time.Minute), GuardrailAction: "record",
			GuardrailReason: "策略判定：record"},
	}
	f, err := os.Create(filepath.Join(runDir, "run_1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	rules := &project.ScopeRules{Enabled: true, DefaultAction: project.ScopeActionReview, Rules: []project.ScopeRule{
		{ID: "sr-2", Action: project.ScopeActionReview, Method: "*", Host: "out.example", PathPrefix: "/"},
	}}
	return &project.Project{
		Manifest: project.Manifest{ID: "p1", Name: "测试项目", ScopeRuleSet: rules},
		Dir:      dir, CaseDir: caseDir,
	}
}

// The audit names where each hit came from: the instance fence (global:) or a
// project row (sr-…), with the rule's own text; command rows come from journals
// and keep their review state.
func TestCollectFenceHitsAcrossSources(t *testing.T) {
	p := fenceHitProject(t, t.TempDir())
	fence := settings.Fence{
		Commands: []settings.GlobalCommandRule{{ID: "gf-9", Pattern: "curl *", Desc: "禁止下载"}},
		Domains:  []settings.GlobalDomainRule{{ID: "gf-1", Host: "evil.example"}},
	}
	hits := collectFenceHits([]*project.Project{p}, fence)
	if len(hits) != 4 {
		t.Fatalf("hits = %d: %+v", len(hits), hits)
	}
	// Newest first: review row (4m), decision (3m), project-rule capture (2m),
	// global domain capture (1m).
	if hits[0].Kind != "command" || hits[0].Action != "review" || hits[0].State != "pending" {
		t.Fatalf("review hit = %+v", hits[0])
	}
	if hits[0].Source != "global" || hits[0].RuleID != "gf-9" {
		t.Fatalf("review source = %+v", hits[0])
	}
	if hits[0].RuleText != "禁止下载 · curl *" {
		t.Fatalf("review rule text = %q", hits[0].RuleText)
	}
	if hits[1].Kind != "command" || hits[1].Action != "block" || hits[1].Source != "global" {
		t.Fatalf("decision hit = %+v", hits[1])
	}
	if hits[3].Kind != "domain" || hits[3].Source != "global" || hits[3].RuleID != "gf-1" {
		t.Fatalf("domain hit = %+v", hits[3])
	}
	if hits[3].RuleText != "禁访 evil.example" || hits[3].Content != "https://evil.example/x" {
		t.Fatalf("domain text/content = %+v", hits[3])
	}
	if hits[3].ProjectID != "p1" || hits[3].Project != "测试项目" || hits[3].RunID != "run_1" {
		t.Fatalf("project provenance = %+v", hits[3])
	}
}

// A project rule's hit is source=rule with the row's own text; a record
// verdict is not a hit.
func TestCollectFenceHitsProjectRule(t *testing.T) {
	p := fenceHitProject(t, t.TempDir())
	hits := collectFenceHits([]*project.Project{p}, settings.Fence{})
	var rule *FenceHit
	for i := range hits {
		if hits[i].Source == "rule" {
			rule = &hits[i]
		}
	}
	if rule == nil {
		t.Fatalf("no project-rule hit: %+v", hits)
	}
	if rule.Kind != "domain" || rule.RuleID != "sr-2" || rule.Action != "review" {
		t.Fatalf("rule hit = %+v", rule)
	}
	if rule.RuleText != "review * out.example /" {
		t.Fatalf("rule text = %q", rule.RuleText)
	}
	for _, hit := range hits {
		if hit.Action == "record" {
			t.Fatalf("a record verdict leaked into the audit: %+v", hit)
		}
	}
}

func TestFilterFenceHits(t *testing.T) {
	rows := []FenceHit{
		{Source: "global", Kind: "command", Action: "block"},
		{Source: "global", Kind: "domain", Action: "block"},
		{Source: "rule", Kind: "domain", Action: "review"},
	}
	if got := filterFenceHits(rows, "global", "", ""); len(got) != 2 {
		t.Fatalf("source filter = %+v", got)
	}
	if got := filterFenceHits(rows, "", "domain", ""); len(got) != 2 {
		t.Fatalf("kind filter = %+v", got)
	}
	if got := filterFenceHits(rows, "", "", "review"); len(got) != 1 || got[0].Source != "rule" {
		t.Fatalf("action filter = %+v", got)
	}
	if got := filterFenceHits(rows, "global", "domain", "block"); len(got) != 1 {
		t.Fatalf("combined filter = %+v", got)
	}
}
