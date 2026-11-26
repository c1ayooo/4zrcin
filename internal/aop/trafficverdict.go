package aop

import (
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"
)

// TrafficVerdictExtensionURL is the 4zrcin fork's per-flow verdict extension on
// a traffic.Flow event. The scope-rule runtime (not the model judge) emits it
// beside the flow it judged, in the same event's extensions list — the flow
// proto itself stays untouched, so the vendored descriptors never change:
//
//	{"action": "record|review|block", "rule_id": "sr-2"}   # rule_id omitted
//	                                                        when no rule matched
//
// zrcin stores the verdict in the capture metadata (.http/*.json), where the
// console's traffic view renders it next to the exchange. The payload is JSON,
// like every other fork extension; the project manifest remains the authority
// for the rule vocabulary.
const TrafficVerdictExtensionURL = "type.googleapis.com/4zrcin.TrafficVerdict"

// TrafficVerdict is one decoded verdict sidecar.
type TrafficVerdict struct {
	Action string `json:"action"`
	RuleID string `json:"rule_id,omitempty"`
	// DefaultMissed marks a fallback verdict (no rule row matched, fork patch
	// 30): under the allowlist fence the control plane routes it to the
	// asset-pending flow instead of reading it as a hand-written verdict.
	// Absent on a hit and on every pre-patch sidecar.
	DefaultMissed bool `json:"default_missed,omitempty"`
}

// Valid reports whether the verdict carries a known action. Anything else is
// not a verdict the console may render as one.
func (v TrafficVerdict) Valid() bool {
	switch strings.ToLower(v.Action) {
	case "record", "review", "block":
		return true
	}
	return false
}

// TrafficVerdictExtension encodes one verdict. An invalid (or empty) verdict
// returns nil: a caller appending nil is a no-op, so an unjudged flow keeps the
// wire byte-identical.
func TrafficVerdictExtension(v TrafficVerdict) *anypb.Any {
	if !v.Valid() {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return &anypb.Any{TypeUrl: TrafficVerdictExtensionURL, Value: raw}
}

// ParseTrafficVerdict decodes one sidecar Any. ok is false for anything that is
// not a valid verdict of this namespace; the caller keeps such events untouched
// instead of guessing.
func ParseTrafficVerdict(ext *anypb.Any) (TrafficVerdict, bool) {
	if ext == nil || ext.GetTypeUrl() != TrafficVerdictExtensionURL {
		return TrafficVerdict{}, false
	}
	var v TrafficVerdict
	if err := json.Unmarshal(ext.GetValue(), &v); err != nil {
		return TrafficVerdict{}, false
	}
	if !v.Valid() {
		return TrafficVerdict{}, false
	}
	v.Action = strings.ToLower(v.Action)
	return v, true
}
