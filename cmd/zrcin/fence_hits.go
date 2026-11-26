package main

// The instance fence's hit audit (spec #1 工单9/#10): one list across every
// project, read from the records the control plane already keeps —
//
//   - domain hits: the capture records' sidecar verdict. A global entry names
//     itself "global:<id>"; a project row names its stable "sr-…" id.
//   - command hits: the run journals' guardrail rows. The global fence refuses
//     before the project policy runs and writes its verdict as
//     "global.<id>" (a Decision has no call, so the journal's tool name is the
//     only other fact; the command text itself is not recorded).
//
// Nothing is written here and no new store exists: the audit is a read of the
// same evidence the traffic and journal views already show, grouped by the
// question "which red line fired, where, and when".

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/settings"
	"github.com/c1ayooo/4zrcin/internal/traffic"
)

// FenceHit is one recorded firing of a global entry or a project rule.
type FenceHit struct {
	At        time.Time `json:"at"`
	ProjectID string    `json:"project_id"`
	Project   string    `json:"project"`
	RunID     string    `json:"run_id,omitempty"`
	// Source is global (the instance fence) or rule (a project scope row).
	Source string `json:"source"`
	// Kind is command or domain.
	Kind     string `json:"kind"`
	RuleID   string `json:"rule_id"`
	RuleText string `json:"rule_text,omitempty"`
	// Content is what the hit names: the URL for a domain, the reason line for
	// a command (the journal does not carry the command text).
	Content string `json:"content"`
	// Action is how the verdict was recorded: block | review.
	Action string `json:"action"`
	// State is the review lifecycle when the row is a review (pending/approved/
	// rejected…), empty for a plain decision.
	State string `json:"state,omitempty"`
}

const (
	fenceHitLimitMax     = 500
	fenceHitLimitDefault = 200
	// Per-project caps: the audit is a reading surface, not an archive. A
	// project with tens of thousands of captures must not make the page scan
	// the entire disk; the newest files/rows win.
	fenceHitCaptureFilesPerProject = 4000
	fenceHitJournalLinesPerRun     = 4000
)

func (h *hub) serveFenceHits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	q := r.URL.Query()
	source := strings.ToLower(strings.TrimSpace(q.Get("source")))
	if source != "" && source != "global" && source != "rule" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "source 只能是 global 或 rule"})
		return
	}
	kind := strings.ToLower(strings.TrimSpace(q.Get("kind")))
	if kind != "" && kind != "command" && kind != "domain" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "kind 只能是 command 或 domain"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(q.Get("action")))
	if action != "" && action != "block" && action != "review" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "action 只能是 block 或 review"})
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = fenceHitLimitDefault
	}
	if limit > fenceHitLimitMax {
		limit = fenceHitLimitMax
	}
	var fence settings.Fence
	if h.settings != nil {
		fence = h.settings.FenceOf()
	}
	var projects []*project.Project
	if h.mgr != nil {
		projects = h.mgr.All()
	}
	rows := collectFenceHits(projects, fence)
	filtered := filterFenceHits(rows, source, kind, action)
	total := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits":   filtered,
		"total":  total,
		"limit":  limit,
		"source": source,
		"kind":   kind,
		"action": action,
	})
}

// filterFenceHits applies the page's three filters; each empty value is "all".
func filterFenceHits(rows []FenceHit, source, kind, action string) []FenceHit {
	out := make([]FenceHit, 0, len(rows))
	for _, row := range rows {
		if source != "" && row.Source != source {
			continue
		}
		if kind != "" && row.Kind != kind {
			continue
		}
		if action != "" && row.Action != action {
			continue
		}
		out = append(out, row)
	}
	return out
}

// collectFenceHits reads every project's captures and run journals and returns
// the hits newest-first.
func collectFenceHits(projects []*project.Project, fence settings.Fence) []FenceHit {
	var hits []FenceHit
	for _, p := range projects {
		if p == nil {
			continue
		}
		hits = append(hits, captureFenceHits(p, fence)...)
		hits = append(hits, journalFenceHits(p, fence)...)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].At.Equal(hits[j].At) {
			return hits[i].ProjectID < hits[j].ProjectID
		}
		return hits[i].At.After(hits[j].At)
	})
	return hits
}

func captureFenceHits(p *project.Project, fence settings.Fence) []FenceHit {
	dir := filepath.Join(p.CaseDir, ".http")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	// Newest files first: the page's cap is a reading choice, and an old hit
	// must not crowd out today's.
	sort.Slice(entries, func(i, j int) bool {
		ii, e1 := entries[i].Info()
		ij, e2 := entries[j].Info()
		if e1 != nil || e2 != nil {
			return entries[i].Name() > entries[j].Name()
		}
		return ii.ModTime().After(ij.ModTime())
	})
	var out []FenceHit
	for i, entry := range entries {
		if i >= fenceHitCaptureFilesPerProject {
			break
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var c traffic.Capture
		if json.Unmarshal(raw, &c) != nil || c.GuardrailRuleID == "" {
			continue
		}
		if c.GuardrailAction != "block" && c.GuardrailAction != "review" {
			continue
		}
		hit := FenceHit{
			At: c.At, ProjectID: p.ID, Project: p.Name, RunID: c.RunID,
			Kind: "domain", RuleID: c.GuardrailRuleID, Content: c.URL,
			Action: c.GuardrailAction,
		}
		if host, ok := splitGlobalID(c.GuardrailRuleID, "global:"); ok {
			hit.Source = "global"
			hit.RuleID = host
			hit.RuleText = fenceDomainText(fence, host)
		} else {
			hit.Source = "rule"
			hit.RuleText = projectRuleText(p, c.GuardrailRuleID)
		}
		out = append(out, hit)
	}
	return out
}

// journalFenceHits reads guardrail rows from run journals. The journal is the
// only record of a command refusal, so this is the whole command side.
func journalFenceHits(p *project.Project, fence settings.Fence) []FenceHit {
	dir := p.RunsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	var out []FenceHit
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		lines := readJournalGuardrails(filepath.Join(dir, entry.Name()))
		runID := strings.TrimSuffix(entry.Name(), ".jsonl")
		for _, ev := range lines {
			hit, ok := fenceHitFromEvent(ev, fence)
			if !ok {
				continue
			}
			hit.ProjectID, hit.Project, hit.RunID = p.ID, p.Name, runID
			out = append(out, hit)
		}
	}
	return out
}

func readJournalGuardrails(path string) []executor.Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []executor.Event
	dec := json.NewDecoder(f)
	for i := 0; i < fenceHitJournalLinesPerRun && dec.More(); i++ {
		var ev executor.Event
		if dec.Decode(&ev) != nil {
			break
		}
		if ev.Kind == executor.KindGuardrailDecision || ev.Kind == executor.KindGuardrailReview {
			out = append(out, ev)
		}
	}
	return out
}

// fenceHitFromEvent maps one guardrail journal row to a hit. A row that names
// neither a global entry nor a project rule is not a fence hit (it is the
// ordinary policy traffic) and is dropped.
func fenceHitFromEvent(ev executor.Event, fence settings.Fence) (FenceHit, bool) {
	action := ev.GuardrailAction
	if ev.Kind == executor.KindGuardrailReview {
		action = "review"
	}
	if action != "block" && action != "review" {
		return FenceHit{}, false
	}
	hit := FenceHit{
		At: ev.At, Kind: "command", Action: action, State: ev.GuardrailState,
		Content: strings.TrimSpace(ev.Text),
		RuleID:  ev.GuardrailRuleID,
	}
	if id, ok := splitGlobalID(ev.GuardrailReason, "global."); ok {
		hit.Source = "global"
		hit.RuleID = trimDesc(id)
		hit.RuleText = fenceCommandText(fence, hit.RuleID)
		return hit, true
	}
	if ev.GuardrailRuleID != "" {
		hit.Source = "rule"
		return hit, true
	}
	return FenceHit{}, false
}

// splitGlobalID pulls the id out of a global reference in either spelling:
// "global:<id>" (domain sidecar) or "global.<id>…" (command reason). It also
// accepts the bare reason text, because the command reason is what the journal
// stores.
func splitGlobalID(raw, prefix string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	return strings.TrimPrefix(raw, prefix), true
}

// trimDesc cuts the "（描述）" suffix the command reason appends.
func trimDesc(id string) string {
	if i := strings.IndexAny(id, "（("); i >= 0 {
		return strings.TrimSpace(id[:i])
	}
	return id
}

func fenceDomainText(fence settings.Fence, id string) string {
	for _, rule := range fence.Domains {
		if rule.ID == id {
			return "禁访 " + rule.Host
		}
	}
	return ""
}

func fenceCommandText(fence settings.Fence, id string) string {
	for _, rule := range fence.Commands {
		if rule.ID == id {
			if rule.Desc != "" {
				return rule.Desc + " · " + rule.Pattern
			}
			return rule.Pattern
		}
	}
	return ""
}

func projectRuleText(p *project.Project, id string) string {
	rules, ok := p.ScopeRulesOf()
	if !ok {
		return ""
	}
	for _, rule := range rules.Rules {
		if rule.ID == id {
			return rule.Text()
		}
	}
	return ""
}
