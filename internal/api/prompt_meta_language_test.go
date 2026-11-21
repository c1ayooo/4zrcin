package api

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/board"
	"github.com/kksty/tianwu/internal/contract"
	"github.com/kksty/tianwu/internal/decider"
	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/store"
)

// retiredPromptPhrases are the meta sentences the team removed on 2026-11-21.
// They are matched as whole phrases, not as a single word: 「授权」alone appears
// in legitimate user text and in the commander's own identity, so a one-word
// assertion is both too broad (it fails on honest text) and too narrow (it
// misses「文档即边界…以文档为准」).
var retiredPromptPhrases = []string{
	"授权来源",
	"文档即边界",
	"文档全文即边界",
	"不是授权",
	"不是重新授权",
	"不是新的授权",
	"冲突时停手",
	"冲突停手",
	"以文档为准",
	"不构成新的授权",
}

func assertNoRetiredPhrases(t *testing.T, where, prompt string) {
	t.Helper()
	for _, phrase := range retiredPromptPhrases {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("%s still carries the retired phrase %q:\n%s", where, phrase, prompt)
		}
	}
}

// The executor brief is: 约束 → 本轮指派 → the rest, each section exactly once.
// The operator-priority sentence is the very first paragraph, before any heading.
//
// Falsification: move the priority sentence below a heading, drop a section or
// render one twice, and this fails.
func TestExecutorBriefSectionOrder(t *testing.T) {
	db := store.DB{
		Intents: []model.Intent{{ID: "in_x", Title: "读 /admin 的响应头", Description: "核对缓存策略", Eval: "拿到响应头"}},
		Runs:    []model.Run{{ID: "r_x", IntentID: "in_x"}},
	}
	docs := "不要删除或修改真实数据。测试产物清不掉就记录后继续。"
	p := BuildPromptWithInstructions(db, db.Intents[0], docs, "", "", 0, false, false, "", "", "")

	if !strings.HasPrefix(p, contract.OperatorPriority) {
		t.Fatalf("operator priority is not the first paragraph:\n%s", p)
	}
	order := []string{"## 约束", "## 本轮指派", "## 验收标准"}
	at := -1
	for _, heading := range order {
		next := strings.Index(p, heading)
		if next < 0 {
			t.Fatalf("section %q missing:\n%s", heading, p)
		}
		if next <= at {
			t.Fatalf("section %q is out of order (offset %d after %d):\n%s", heading, next, at, p)
		}
		if got := strings.Count(p, heading); got != 1 {
			t.Fatalf("section %q rendered %d times, want 1", heading, got)
		}
		at = next
	}
	if got := strings.Count(p, contract.OperatorPriority); got != 1 {
		t.Fatalf("priority sentence rendered %d times, want 1", got)
	}
	if got := strings.Count(p, docs); got != 1 {
		t.Fatalf("constraint document rendered %d times, want 1", got)
	}
	assertNoRetiredPhrases(t, "executor brief", p)
}

// A document that happens to contain「授权」passes through untouched: the
// retirement rule is about template sentences, not about filtering user text.
func TestConstraintDocumentPassesThroughVerbatim(t *testing.T) {
	db := store.DB{Intents: []model.Intent{{ID: "in_x", Title: "t"}}}
	docs := "授权文档：只测 A 段，不要碰 B 段。"
	p := BuildPromptWithInstructions(db, db.Intents[0], docs, "", "", 0, false, false, "", "", "")
	if got := strings.Count(p, docs); got != 1 {
		t.Fatalf("a document containing the word was filtered or duplicated (%d)", got)
	}
	// Other template text legitimately uses the word (the executor identity and
	// the discipline block talk about what the operator authorises), which is why
	// the retirement rule is a phrase list, not a one-word ban.
	assertNoRetiredPhrases(t, "executor brief", p)
}

// A persistent instruction is injected as its own text: no sentence explains
// where it comes from or how it relates to the documents.
func TestInstructionSectionRendersOnlyTheText(t *testing.T) {
	rows := []model.Instruction{{ID: "instruction_1", Version: 3, Text: "多关注运维口"}}
	section := instructionSection(rows, "r_x")
	assertNoRetiredPhrases(t, "instruction section", section)
	for _, banned := range []string{"来源", "范围依据"} {
		if strings.Contains(section, banned) {
			t.Fatalf("instruction section still explains %q:\n%s", banned, section)
		}
	}
	if !strings.Contains(section, "多关注运维口") || !strings.Contains(section, "instruction_1") {
		t.Fatalf("instruction text or id missing:\n%s", section)
	}
}

// The commander round carries the priority sentence once, between the role line
// and the documents; the retired phrases stay out of it too.
func TestCommanderRoundCarriesOperatorPriorityOnce(t *testing.T) {
	in := testDB()
	docs := "## 约束\n不要改数据\n\n## 起点\n10.0.0.0/24\n\n"
	prompt := decider.CommanderRoundPrompt(board.BuildFromDB(in, 0), "", docs, decider.ContextStatus{}, decider.RoundContext{})
	if got := strings.Count(prompt, contract.OperatorPriority); got != 1 {
		t.Fatalf("commander round renders the priority sentence %d times, want 1", got)
	}
	role := strings.Index(prompt, "你是天吴项目的指挥官")
	prio := strings.Index(prompt, contract.OperatorPriority)
	docsAt := strings.Index(prompt, "## 约束")
	if !(role >= 0 && role < prio && prio < docsAt) {
		t.Fatalf("priority sentence is not between the role and the documents (role=%d prio=%d docs=%d)", role, prio, docsAt)
	}
	assertNoRetiredPhrases(t, "commander round", prompt[prio:])
}
