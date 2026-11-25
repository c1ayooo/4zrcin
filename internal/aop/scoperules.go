package aop

import (
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"
)

// ScopeRulesExtensionURL is the 4zrcin fork's per-turn scope-rules extension on
// RunTurnRequest. The payload is the project manifest's "scope_rules" JSON,
// verbatim — the project package (internal/project) is the single authority for
// the shape and the validation; this layer only wraps it, so there is no second
// struct to drift. The fork applies the rule set to the session's audit context
// (traffic and command matching) before the turn's tools run. An old harness
// ignores the unknown extension, so sending it is additive; a nil ruleset sends
// nothing and keeps the wire byte-identical.
//
// The rules ride every turn instead of a separate Configure push for the same
// reason ToolEnv does: a resumed turn never re-opens its session, so any
// push-once channel would leave a restarted host on stale rules until someone
// remembered to resend. Per-turn carriage makes "saved" and "in force from the
// next turn" the same fact.
const ScopeRulesExtensionURL = "type.googleapis.com/4zrcin.ScopeRules"

// ScopeRulesExtension wraps one project's scope rules. An empty payload
// (no rules configured) returns nil: callers append the extension only when
// it is non-nil.
func ScopeRulesExtension(rules json.RawMessage) *anypb.Any {
	if len(strings.TrimSpace(string(rules))) == 0 {
		return nil
	}
	return &anypb.Any{TypeUrl: ScopeRulesExtensionURL, Value: []byte(rules)}
}
