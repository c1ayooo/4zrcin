package api

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// A dispatched executor learns that peers and model tiers exist, and — since the
// 2026-11-22 run dispatched zero times across 317 runs — the one executable entry
// it cannot otherwise guess: POST $TW_API/spawn. Everything else is still an
// inventory: the contract stays in the collaborate skill, and no other command
// recipe is allowed here (a model that reads the skill finds the rest).
func TestCollaborationSectionKeepsPeerAndDelegationCapabilities(t *testing.T) {
	s, p := newBindingServer(t)
	self, err := p.Store.CreateIntent(model.Intent{Title: "父意图"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.CreateIntent(model.Intent{Title: "另一意图"}); err != nil {
		t.Fatal(err)
	}
	done, err := p.Store.CreateIntent(model.Intent{Title: "已完成意图"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.UpdateIntent(done.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
		t.Fatal(err)
	}

	section := s.collaborationSection(self)
	for _, want := range []string{"## 自主协作", "协作是手段不是流程", "能独立完成就直接做", "另一意图", "pt_in_",
		"cyber://skills/collaborate/SKILL.md", ".tw/identity.json",
		// The single executable entry: without it a model that never opens the
		// skill cannot discover the endpoint (0 dispatches in 317 runs).
		"POST $TW_API/spawn", "idempotency_key", "context_fact_ids", "started=false"} {
		if !strings.Contains(section, want) {
			t.Fatalf("collaboration section missing %q:\n%s", want, section)
		}
	}
	// Collaboration is described by what a model may do, never by a role
	// ladder: the prompt must not resurrect 将军/士兵 or a fixed hierarchy.
	for _, forbidden := range []string{"将军", "士兵", "协调者"} {
		if strings.Contains(section, forbidden) {
			t.Fatalf("collaboration section reintroduced a role name %q:\n%s", forbidden, section)
		}
	}
	if strings.Contains(section, "已完成意图") {
		t.Fatalf("finished direction listed as a contactable peer:\n%s", section)
	}
	if strings.Contains(section, "broken") {
		t.Fatalf("unrunnable profile listed as available:\n%s", section)
	}
	// Inventory only: no command recipes (agent.md §2 keeps tool use in the
	// harness, the skill carries the collaboration contract). One exception is
	// pinned above and counted here: the spawn entry, and only it.
	for _, deny := range []string{"curl", "GET /", "POST /"} {
		if strings.Contains(section, deny) {
			t.Fatalf("collaboration section teaches tool use (%q):\n%s", deny, section)
		}
	}
	if n := strings.Count(section, "POST $TW_API/"); n != 1 {
		t.Fatalf("collaboration section carries %d POST entries, want exactly the spawn one:\n%s", n, section)
	}
}

// A peer line says which tier the peer's current run is on, so a worker can
// tell a strong direction from a fellow worker without being told a role name.
// The tier is read off the run and stays absent when the peer has no run: the
// intent only carries a profile id, and the catalogue is not this section's job.

// A solitary direction on an instance with no profile catalogue pays nothing.
func TestCollaborationSectionEmptyWhenNothingToOffer(t *testing.T) {
	s, p := newBindingServer(t)
	self, err := p.Store.CreateIntent(model.Intent{Title: "独跑"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.collaborationSection(self); got != "" {
		t.Fatalf("solo section = %q, want empty", got)
	}
}
