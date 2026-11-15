package decider

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/board"
	"github.com/kksty/tianwu/internal/executor"
	"github.com/kksty/tianwu/internal/model"
)

// Budgets are not about a 1M-token window: they catch the accidental growth
// that makes the commander's one stable input carry a second policy document.
// The round prompt should be decision input plus pointers; formatting and
// protocol detail belong in SKILL/tool schemas.
func TestCommanderPromptBudget(t *testing.T) {
	st := newStore(t)
	if _, err := st.AddGoal(model.Goal{Statement: "open goal"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		if _, err := st.AddFact(model.Fact{
			Title:   "fixture fact",
			Summary: strings.Repeat("这是一条用于预算测试的摘要。", 20),
			Kind:    "note",
		}); err != nil {
			t.Fatal(err)
		}
	}
	b := board.Build(st, 20)
	p := CommanderRoundPrompt(
		b,
		strings.Repeat("- 已确认的结论与线索\n", 120),
		strings.Repeat("授权范围里的资产与约束。", 30),
		ContextStatus{Used: 50000, Window: 128000},
		RoundContext{Resume: true, StatePath: "/data/state.md"},
	)
	if len(p) > 160<<10 {
		t.Fatalf("commander prompt grew to %d bytes (budget 163840)", len(p))
	}
	if n := strings.Count(p, "\n## "); n > 20 {
		t.Fatalf("commander prompt has %d sections (budget 20)", n)
	}
	if len(commanderIdentity) > 2200 {
		t.Fatalf("commander identity grew to %d bytes (budget 2200)", len(commanderIdentity))
	}
	if n := strings.Count(commanderIdentity, "\n"); n > 12 {
		t.Fatalf("commander identity has %d lines (budget 12)", n)
	}
	for _, gone := range []string{"需要操作员授权", "持久指令被撤销"} {
		if strings.Contains(commanderIdentity, gone) {
			t.Fatalf("commander identity re-acquired a protocol paragraph: %q", gone)
		}
	}
}

// The revocation rule lives once, in the shared instruction section, not in the
// commander identity or SKILL. This keeps one source of truth for every role.
func TestRevocationRuleLivesInInstructionSectionOnly(t *testing.T) {
	if strings.Contains(commanderIdentity, "旧正文不再生效") {
		t.Fatal("commander identity duplicated the instruction revocation rule")
	}
	if !strings.Contains(executor.InstructionSection(nil), "撤销或替换后") {
		t.Fatal("the shared instruction section lost the revocation rule")
	}
}
