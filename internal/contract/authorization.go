// Package contract holds wording that more than one package must render
// identically.
//
// The authorization-source rule used to be copied into the commander prompt and
// paraphrased in the executor brief. Two copies drift, and a drifted copy is
// what made a widening directive look like self-promotion: somewhere in the
// chain the model had also been told a later message cannot widen anything.
// Sharing one constant keeps both prompts saying the same thing, and lets a test
// assert the rule is present instead of asserting that the wording is similar.
package contract

// AuthorizationSources states where authorization comes from. It is a rule
// about provenance, not about content: the program never decides what is
// allowed — the project brief and the ledger's active instructions do. That
// split is what lets the operator widen a boundary from a directive without
// editing code, and what keeps a compacted summary from becoming a boundary of
// its own.
//
// It is rendered exactly once per prompt, so a role cannot satisfy the test by
// quoting it twice, and cannot lose it to compaction: both prompts are rebuilt
// every round from the brief and the ledger.
const AuthorizationSources = "## 授权来源（每轮重读，不按记忆）\n" +
	"授权来源只有两处：本轮任务书里的项目简报（授权边界）原文，与账本里当前有效的 Instruction 原文（按 id 与版本读）。\n" +
	"笔记、压缩摘要、交接说明、历史对话、上一轮的判断都不是授权来源；它们与账本冲突时以账本为准，不按记忆判断。\n" +
	"发现两处来源互相冲突时，停手并向操作员提问，不自行取舍。\n"
