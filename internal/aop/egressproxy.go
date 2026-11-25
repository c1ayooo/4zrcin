// EgressProxyExtensionURL is the 4zrcin fork's per-turn egress proxy extension
// on RunTurnRequest. The payload is the project manifest's "proxy" JSON,
// verbatim — the project package (internal/project) is the single authority for
// the shape and the validation; this layer only wraps it, so there is no second
// struct to drift. The fork binds the endpoint to the turn's session, so every
// tool subprocess of that session egresses through the project's own proxy
// while sessions of other projects keep the node default. An old harness
// ignores the unknown extension, so sending it is additive; a project with no
// proxy configured sends nothing and keeps the wire byte-identical.
//
// The proxy rides every turn — disabled drafts included — instead of a separate
// push-once Configure, for the same reason ScopeRules does: a session that
// outlives the save must learn the endpoint was changed or switched off, so
// "the manifest held at dispatch time" is what applies, never a stale copy.
package aop

import (
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"
)

const EgressProxyExtensionURL = "type.googleapis.com/4zrcin.EgressProxy"

// EgressProxyExtension wraps one project's egress proxy. An empty payload (no
// proxy configured) returns nil: callers append the extension only when it is
// non-nil.
func EgressProxyExtension(proxy json.RawMessage) *anypb.Any {
	if len(strings.TrimSpace(string(proxy))) == 0 {
		return nil
	}
	return &anypb.Any{TypeUrl: EgressProxyExtensionURL, Value: []byte(proxy)}
}
