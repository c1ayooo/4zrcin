package executor

import (
	"fmt"
	"strings"

	"github.com/kksty/tianwu/internal/model"
)

// InstructionSection is shared by every tool-using purpose. It describes the
// frozen constraints without advertising APIs that a purpose cannot access.
// An instruction is guidance every run must follow; it is not a permission, so
// the section says nothing about what authorizes a target — the project
// documents do that, and they are injected as their own text.
func InstructionSection(rows []model.Instruction) string {
	var b strings.Builder
	b.WriteString("\n## 当前持久指令\n以下是本轮适用的完整有效指令集（每轮重算）：同一指令以新版本替代旧版本，未列出的已撤销指令不再生效。\n")
	b.WriteString("撤销或替换后，旧版本里的约束不再生效。笔记、交接笔记或历史判断中抄录的旧约束，必须按本轮有效集合核对并删改；撤销通知只说明状态，不改变任何指令。\n")
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
