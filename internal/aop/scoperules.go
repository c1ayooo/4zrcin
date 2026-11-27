package aop

// The scope-rules payload contract lives in aop/payloads: the control plane
// holds the manifest's rule table verbatim and the node's fence parses it with
// its own schema, so the wire shape is a raw-JSON passthrough.
import (
	"encoding/json"

	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const ScopeRulesExtensionURL = payloads.ScopeRulesExtensionURL

func ScopeRulesExtension(rules json.RawMessage) *anypb.Any {
	return payloads.ScopeRulesExtension(rules)
}
