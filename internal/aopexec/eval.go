package aopexec

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	pb "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/types"
)

// evalDetailExtensionURL is the harness evaluator's detail extension on status
// events (state eval_start / eval_end / eval_error). The generated message is
// the engine's types.EvalDetail; the wire format is validated there. An unknown
// or absent extension leaves the status event untouched, which is what an
// older harness without evaluation detail looks like.
const evalDetailExtensionURL = "type.googleapis.com/cyber.agent.EvalDetail"

// evalDetail is the subset of cyber.agent.EvalDetail the run journal records.
type evalDetail struct {
	Round     int
	MaxRounds int
	// Pass is nil when the event carries no verdict (eval_start / eval_error).
	// "Not judged" must not be recorded as "failed", so presence is tracked
	// instead of defaulting the bool to false.
	Pass   *bool
	Reason string
	Error  string
}

// findEvalDetail returns the evaluation detail carried by an event. A payload
// that cannot be decoded is reported as the detail's error: the event still
// reaches the journal and says why its detail is missing.
func findEvalDetail(ev *pb.Event) (evalDetail, bool) {
	for _, ext := range ev.GetExtensions() {
		if ext == nil || ext.GetTypeUrl() != evalDetailExtensionURL {
			continue
		}
		detail, err := decodeEvalDetail(ext.GetValue())
		if err != nil {
			return evalDetail{Error: "评估详情解码失败：" + err.Error()}, true
		}
		return detail, true
	}
	return evalDetail{}, false
}

// hasPassField scans the wire bytes for a field-3 tag only. proto3 cannot
// distinguish "absent" from "false" in the generated type, and "not judged"
// must not be recorded as "failed", so presence is read off the raw bytes.
// It does nothing else.
func hasPassField(raw []byte) bool {
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return false
		}
		raw = raw[n:]
		if num == 3 && typ == protowire.VarintType {
			return true
		}
		n = protowire.ConsumeFieldValue(num, typ, raw)
		if n < 0 {
			return false
		}
		raw = raw[n:]
	}
	return false
}

// decodeEvalDetail parses cyber.agent.EvalDetail through the engine's
// generated type:
//
//	1 error (string), 2 max_rounds (uint32), 3 pass (bool),
//	4 reason (string), 5 round (uint32).
//
// The pass field's value comes from the generated type; whether it appeared at
// all is scanned from the raw bytes (see hasPassField) because proto3 folds an
// absent bool and a false one together.
func decodeEvalDetail(raw []byte) (evalDetail, error) {
	var msg types.EvalDetail
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return evalDetail{}, err
	}
	out := evalDetail{
		Error:     msg.GetError(),
		MaxRounds: int(msg.GetMaxRounds()),
		Reason:    msg.GetReason(),
		Round:     int(msg.GetRound()),
	}
	if hasPassField(raw) {
		pass := msg.GetPass()
		out.Pass = &pass
	}
	return out, nil
}

// evalStatusText folds the evaluation detail into the status text. The run page
// already renders status text, so the verdict is visible without a front-end
// change; the structured fields stay on the event for later consumers.
func evalStatusText(state string, detail evalDetail) string {
	var b strings.Builder
	b.WriteString(state)
	if detail.Round > 0 || detail.MaxRounds > 0 {
		fmt.Fprintf(&b, " 第%d/%d轮", detail.Round, detail.MaxRounds)
	}
	if detail.Pass != nil {
		if *detail.Pass {
			b.WriteString("：通过")
		} else {
			b.WriteString("：未通过")
		}
	}
	if reason := strings.TrimSpace(detail.Reason); reason != "" {
		b.WriteString(" " + reason)
	}
	if failure := strings.TrimSpace(detail.Error); failure != "" {
		b.WriteString(" " + failure)
	}
	return b.String()
}
