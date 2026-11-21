package executor

import (
	"fmt"
	"strings"

	"github.com/kksty/tianwu/internal/model"
)

// AuthorizedSection renders the instruction texts the operator marked as
// boundary changes. The heading is contract.AuthorizationSources' own, so this
// returns lines only: one heading per prompt, never two competing ones.
//
// The list exists because "persistent instruction" and "permission" are not the
// same thing. Every active instruction is injected and must be followed; only a
// marked one may be cited as the reason a target is in scope. Listing the marked
// ones next to the rule is what lets a run answer "what authorizes this" without
// re-reading the whole instruction set and guessing.
func AuthorizedSection(rows []model.Instruction) string {
	// nil means "this text has no instruction context" (the handoff block, which
	// carries the frozen instruction texts itself); an empty slice means "this
	// round has no marked instruction", which is worth saying out loud.
	if rows == nil {
		return ""
	}
	var b strings.Builder
	if len(rows) == 0 {
		b.WriteString("本轮没有被标记为授权变更的指令：授权边界只有上面这段简报原文。\n")
		return b.String()
	}
	b.WriteString("以下指令被标记为授权变更，可作为范围依据（按 id 与版本回账本核对）：\n")
	for _, in := range rows {
		scope := "项目"
		if in.IntentID != "" {
			scope = "方向子树 " + in.IntentID
		}
		fmt.Fprintf(&b, "- `%s` v%d（%s）：%s\n", in.ID, in.Version, scope, strings.TrimSpace(in.Text))
	}
	return b.String()
}

// InstructionSection is shared by every tool-using purpose. It describes the
// frozen constraints without advertising APIs that a purpose cannot access. The
// authorization rule is stated once for all roles: the operator owns the
// boundary, a newer directive may widen as well as narrow it, and the only
// authorization sources are the project brief and this ledger set — audit F12
// said the opposite (a message may only narrow), which the operator reversed on
// 2026-11-21 after a widening directive was dropped as self-promotion.
func InstructionSection(rows []model.Instruction) string {
	var b strings.Builder
	b.WriteString("\n## 当前持久指令\n以下是本轮适用的完整有效指令集（每轮重算）：同一指令以新版本替代旧版本，未列出的已撤销指令不再生效。授权来源只有项目简报与账本里**被标记为授权变更**的 `Instruction`；未标记的指令是持久要求，不是范围依据。笔记、压缩摘要、交接回执、历史对话都不是授权来源。操作员的最新一条下发为准：它可以补充、收窄，也可以放宽先前口径；同一处冲突时后写的胜，仍无法消解时停手并询问操作员。指令不替代项目授权边界。\n")
	b.WriteString("撤销或替换后，旧版本里的约束不再生效。笔记、交接笔记或历史判断中抄录的旧约束，必须按本轮有效集合核对并删改；撤销通知只说明状态，不是重新授权，也不能用旧笔记把已撤销的约束继续执行。\n")
	if len(rows) == 0 {
		b.WriteString("当前没有有效持久指令。\n")
	}
	for _, in := range rows {
		scope := "项目"
		if in.IntentID != "" {
			scope = "意图子树 " + in.IntentID
		}
		fmt.Fprintf(&b, "\n### %s · v%d · %s\n%s\n", in.ID, in.Version, scope, in.Text)
	}
	return b.String()
}
