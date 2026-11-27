package settings

// The fence section (docs/19 已决 15/19): the tool-judge knobs (on_error,
// shadow, scope, allow_prefixes, timeout, concurrency, llm, and a provider
// naming the llm/hybrid judges) moved out of the guardrail section. The read
// paths recognize the legacy spelling by moving the keys in memory; any save
// writes the fence section and prunes the legacy keys from the guardrail
// section, which keeps only the approval queue's own fields (provider
// none/jev, mode, review_timeout, jev).

import (
	"log"
	"strings"

	"gopkg.in/yaml.v3"
)

// fenceJudgeKeys are the guardrail-section keys that belong to the fence
// section since the migration.
var fenceJudgeKeys = []string{"on_error", "shadow", "scope", "allow_prefixes", "timeout", "concurrency", "llm"}

// fenceProviders name the judges that live in the fence section only; a
// legacy guardrail provider carrying one migrates with the rest.
var fenceProviders = map[string]bool{"llm": true, "hybrid": true}

// migrateFenceJudge moves the judge keys from the guardrail section into the
// fence section, in place, in memory. Existing fence keys win; the operator's
// file is only rewritten when a save happens anyway. Returns whether anything
// moved.
func migrateFenceJudge(doc *yaml.Node) bool {
	guardrail := mapGet(doc, "guardrail")
	if guardrail == nil || guardrail.Kind != yaml.MappingNode {
		return false
	}
	fence := mapGet(doc, "fence")
	if fence != nil && fence.Kind != yaml.MappingNode {
		return false
	}
	moved := false
	move := func(key *yaml.Node, value *yaml.Node) {
		if fence == nil {
			fence = ensureMapping(doc, "fence")
		}
		if mapGet(fence, key.Value) == nil {
			fence.Content = append(fence.Content, key, value)
		}
		moved = true
	}
	for i := 0; i+1 < len(guardrail.Content); {
		key, value := guardrail.Content[i], guardrail.Content[i+1]
		if fenceJudgeKey(key.Value) {
			move(key, value)
			guardrail.Content = append(guardrail.Content[:i], guardrail.Content[i+2:]...)
			continue
		}
		if key.Value == "provider" && fenceProviders[strings.TrimSpace(value.Value)] {
			move(key, value)
			guardrail.Content = append(guardrail.Content[:i], guardrail.Content[i+2:]...)
			continue
		}
		i += 2
	}
	return moved
}

func fenceJudgeKey(key string) bool {
	for _, candidate := range fenceJudgeKeys {
		if candidate == key {
			return true
		}
	}
	return false
}

// migrationDiag records the operator-facing diagnostic that 已决 19 requires:
// every save that carries a legacy layout to its new home says so. Reads
// migrate in memory silently — the operator's file is only rewritten when a
// save happens anyway.
func migrationDiag(what string) {
	log.Printf("配置迁移：%s 已随本次保存写入 harness 配置（docs/19 已决 15/19）", what)
}

// effectiveGuardrailProvider reads the judge provider across both spellings:
// the fence section's provider wins when it names one.
func effectiveGuardrailProvider(doc *yaml.Node) string {
	if fence := mapGet(doc, "fence"); fence != nil && fence.Kind == yaml.MappingNode {
		if raw := mapGet(fence, "provider"); raw != nil && strings.TrimSpace(raw.Value) != "" {
			return strings.TrimSpace(raw.Value)
		}
	}
	if guardrail := mapGet(doc, "guardrail"); guardrail != nil && guardrail.Kind == yaml.MappingNode {
		if raw := mapGet(guardrail, "provider"); raw != nil {
			return strings.TrimSpace(raw.Value)
		}
	}
	return ""
}
