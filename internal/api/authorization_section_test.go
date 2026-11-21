package api

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/store"
)

// The 授权来源 section must list the operator's marked permissions and nothing
// else: an unmarked instruction is still injected above as a durable
// requirement, but it must not be readable as the reason a target is in scope.
func TestAuthorizationSectionListsOnlyMarkedInstructions(t *testing.T) {
	db := store.DB{
		Intents: []model.Intent{{ID: "in_auth", Title: "固定意图"}},
		Instructions: []model.Instruction{
			{ID: "in_marked", Version: 2, Text: "订阅可测", Authorization: true, State: model.InstructionActive},
			{ID: "in_plain", Version: 1, Text: "多关注运维口", State: model.InstructionActive},
		},
		Runs: []model.Run{{ID: "r_auth", IntentID: "in_auth"}},
	}
	// The frozen instruction section is rendered by the dispatch path and passed
	// in; it carries every active instruction verbatim, marked or not.
	instructions := "\n## 当前持久指令\n### in_plain · v1 · 项目\n多关注运维口\n"
	prompt := BuildPromptWithInstructions(db, db.Intents[0], "10.0.0.0/24", "", "", 0, false, false, "", instructions, store.AuthorizedInstructions(db, "in_auth", false), "")

	atSection := strings.Index(prompt, "## 授权来源")
	if atSection < 0 {
		t.Fatal("authorization-source section missing")
	}
	marked := strings.Index(prompt, "`in_marked` v2（项目）：订阅可测")
	if marked < atSection {
		t.Fatalf("marked instruction not listed under the section (section=%d instruction=%d)", atSection, marked)
	}
	if strings.Count(prompt, "订阅可测") != 1 {
		t.Fatalf("marked text appears %d times", strings.Count(prompt, "订阅可测"))
	}
	plain := strings.Index(prompt, "多关注运维口")
	if plain < 0 {
		t.Fatal("the plain instruction was dropped from the prompt")
	}
	if plain > atSection {
		t.Fatal("an unmarked instruction is rendered as an authorization source")
	}
	// The list itself (section start up to the next heading) must not name it.
	section := prompt[atSection:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	if strings.Contains(section, "in_plain") {
		t.Fatalf("an unmarked instruction id appears in the authorization list: %s", section)
	}
}

// With nothing marked the section says so instead of leaving a bare heading: a
// reader must be able to tell "no permissions beyond the brief" from "the list
// failed to render".
func TestAuthorizationSectionSaysWhenNothingIsMarked(t *testing.T) {
	db := store.DB{
		Intents:      []model.Intent{{ID: "in_none", Title: "固定意图"}},
		Instructions: []model.Instruction{{ID: "in_plain", Version: 1, Text: "多关注运维口", State: model.InstructionActive}},
		Runs:         []model.Run{{ID: "r_none", IntentID: "in_none"}},
	}
	prompt := BuildPromptWithInstructions(db, db.Intents[0], "10.0.0.0/24", "", "", 0, false, false, "", "", []model.Instruction{}, "")
	if !strings.Contains(prompt, "本轮没有被标记为授权变更的指令") {
		t.Fatal("empty authorization list is silent")
	}
	if strings.Contains(prompt, "`in_plain`") {
		t.Fatal("a plain instruction leaked into the authorization list")
	}
}
