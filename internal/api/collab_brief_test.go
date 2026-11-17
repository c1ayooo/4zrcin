package api

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// A dispatched executor learns that peers and model tiers exist, but not how to
// drive them: the section is a capability inventory, the contract stays in the
// collaborate skill. This is the affordance sapper puts in its mission text.
func TestCollaborationSectionKeepsPeerAndDelegationCapabilities(t *testing.T) {
	s, p := newBindingServer(t)
	self, err := p.Store.CreateIntent(model.Intent{Title: "父方向"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.CreateIntent(model.Intent{Title: "另一方向"}); err != nil {
		t.Fatal(err)
	}
	done, err := p.Store.CreateIntent(model.Intent{Title: "已完成方向"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.UpdateIntent(done.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
		t.Fatal(err)
	}

	section := s.collaborationSection(self)
	for _, want := range []string{"## 自主协作", "协作是手段不是流程", "能独立完成就直接做", "另一方向", "pt_in_",
		"cyber://skills/collaborate/SKILL.md", ".tw/identity.json"} {
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
	if strings.Contains(section, "已完成方向") {
		t.Fatalf("finished direction listed as a contactable peer:\n%s", section)
	}
	if strings.Contains(section, "broken") {
		t.Fatalf("unrunnable profile listed as available:\n%s", section)
	}
	// Inventory only: no command recipes (agent.md §2 keeps tool use in the
	// harness, the skill carries the collaboration contract).
	for _, deny := range []string{"curl", "GET /", "POST /"} {
		if strings.Contains(section, deny) {
			t.Fatalf("collaboration section teaches tool use (%q):\n%s", deny, section)
		}
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
