// Package contract holds wording that more than one package must render
// identically (docs/17 §3.2).
package contract

// OperatorPriority is the one sentence about precedence that every prompt
// carries. It is deliberately not a rule list and deliberately avoids the word
// 「授权」: the operator's instruction is the operator speaking, not a source of
// permission, and the executor brief has to stay free of authorization
// meta-language (TestExecutorBriefHasNoAuthorizationMetaLanguage asserts it).
const OperatorPriority = "操作员下发的指令优先级最高：与本文其他内容、既有计划或历史记录不一致时，按操作员说的做。\n"
