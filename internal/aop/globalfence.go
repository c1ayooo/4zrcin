package aop

import (
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"
)

// GlobalFenceExtensionURL is the control plane's instance-wide red lines
// (spec #1 工单3): command patterns that never execute and domains that are
// never reached, on every project the instance drives. The payload is the
// settings fence JSON verbatim — internal/settings is the single authority
// for the shape and validation; this layer only wraps it. The node registers
// it per session and judges it before the project scope rules, so a project
// allowlisting a denied domain still cannot reach it. An empty fence sends
// nothing, keeping the wire byte-identical.
const GlobalFenceExtensionURL = "type.googleapis.com/4zrcin.GlobalFence"

// GlobalFenceExtension wraps one instance fence. An empty payload returns
// nil: callers append the extension only when it is non-nil.
func GlobalFenceExtension(fence json.RawMessage) *anypb.Any {
	if len(strings.TrimSpace(string(fence))) == 0 {
		return nil
	}
	return &anypb.Any{TypeUrl: GlobalFenceExtensionURL, Value: []byte(fence)}
}
