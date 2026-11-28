package host

import (
	"strings"
	"testing"
)

// The local node learns the rule library from its environment (docs/20 §4.2):
// exactly one definition, the instance's, and an empty directory changes nothing.
func TestWithRulesDirNamesTheLibraryOnce(t *testing.T) {
	env := []string{"PATH=/bin", "CYBER_RULES_DIR=/old", "CYBER_MODEL=x"}
	got := withRulesDir(env, "/data/rules")
	count := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "CYBER_RULES_DIR=") {
			count++
			if kv != "CYBER_RULES_DIR=/data/rules" {
				t.Fatalf("rules directory = %q", kv)
			}
		}
	}
	if count != 1 || len(got) != 3 {
		t.Fatalf("CYBER_RULES_DIR appears %d times: %v", count, got)
	}
	if same := withRulesDir(env, "  "); strings.Join(same, "|") != strings.Join(env, "|") {
		t.Fatalf("an empty directory changed the environment: %v", same)
	}
}
