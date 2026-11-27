//go:build fenceparity

package guardrail

// The acceptance corpus's baseline twin (4zrcin docs/19 P1-4 acceptance):
// scripts/fence-parity.sh copies this file into a P0-era worktree at
// exts/guardrail/, where the fence admission and the scope judge still live
// on the guardrail runtime, and diffs the emitted VERDICT lines against the
// current tree's. Same sample set, same table format.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/globalfence"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/scoperules"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"github.com/chainreactors/cyber/tools/proxy"
)

type parityConfig struct {
	GlobalFence json.RawMessage `json:"globalfence"`
	ScopeRules  json.RawMessage `json:"scoperules"`
}

type paritySample struct {
	Name   string          `json:"name"`
	Config parityConfig    `json:"config"`
	Call   struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"call"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	Passive    bool   `json:"passive"`
	WantAudit  string `json:"want_audit"`
	WantDeny   bool   `json:"want_deny"`
	WantHold   bool   `json:"hold"`
	WantReason string `json:"want_reason"`
	WantAction string `json:"want_action"`
	WantRule   string `json:"want_rule"`
}

func TestAcceptanceCorpusBaseline(t *testing.T) {
	raw, err := os.ReadFile("testdata/acceptance_samples.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		ToolCalls    []paritySample `json:"tool_calls"`
		HTTPRequests []paritySample `json:"http_requests"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}

	for _, sample := range corpus.ToolCalls {
		t.Run(sample.Name, func(t *testing.T) {
			stream := events.New()
			runtime := newRuntimeWithPolicy(t.Context(), stream, runtimeOptions{timeout: 2 * time.Second}, nil)
			defer func() { _ = runtime.Close(context.Background()) }()
			if sample.Config.GlobalFence != nil {
				if err := globalfence.SetRules("sess-acc", sample.Config.GlobalFence); err != nil {
					t.Fatal(err)
				}
				defer globalfence.DropSession("sess-acc")
			}
			var decisions []string
			var mu sync.Mutex
			stop := stream.Observe(func(event *aop.Event) {
				var d Decision
				ext := event.GetExtension()
				if ext == nil || !ext.MessageIs(&d) || ext.UnmarshalTo(&d) != nil {
					return
				}
				mu.Lock()
				decisions = append(decisions, d.Reason)
				mu.Unlock()
			})
			defer stop.Cancel()

			ev := toolhooks.CallEvent{Call: &aop.ToolCall{Name: sample.Call.Name, Arguments: &aop.EncodedValue{Data: sample.Call.Arguments}}, Operation: &operationpb.Ref{OperationId: "op-" + sample.Name}}
			ctx := operation.ContextWithInvocation(context.Background(), operation.Invocation{SessionID: "sess-acc", CallID: sample.Name})
			if sample.WantHold {
				done := make(chan toolhooks.Admission, 1)
				go func() {
					admission, _ := runtime.Admit(ctx, ev)
					done <- admission
				}()
				waitParityReview(t, stream, ev.Operation.GetOperationId())
				resolveCtx := operation.ContextWithInvocation(context.Background(), operation.Invocation{SessionID: "sess-acc", Emitter: "control"})
				if err := runtime.Resolve(resolveCtx, ev.Operation.GetOperationId(), true); err != nil {
					t.Fatal(err)
				}
				select {
				case admission := <-done:
					if admission.Deny != nil {
						t.Fatalf("approved hold still denied: %v", admission.Deny)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("approval did not release the hold")
				}
				t.Logf("VERDICT %s audit=%s deny=false hold=true", sample.Name, firstParityAudit(mu, decisions))
				return
			}
			admission, err := runtime.Admit(ctx, ev)
			if err != nil {
				t.Fatal(err)
			}
			audit := firstParityAudit(mu, decisions)
			deny := admission.Deny != nil
			reason := ""
			if deny {
				reason = admission.Deny.Error()
			}
			if deny != sample.WantDeny {
				t.Fatalf("deny = %v (%v), want %v", deny, reason, sample.WantDeny)
			}
			if sample.WantReason != "" && !strings.Contains(reason, sample.WantReason) {
				t.Fatalf("reason %q lacks %q", reason, sample.WantReason)
			}
			if !strings.HasPrefix(audit, sample.WantAudit) {
				t.Fatalf("audit = %q, want prefix %q", audit, sample.WantAudit)
			}
			t.Logf("VERDICT %s audit=%s deny=%t hold=%t", sample.Name, audit, deny, sample.WantHold)
		})
	}

	for _, sample := range corpus.HTTPRequests {
		t.Run(sample.Name, func(t *testing.T) {
			stream := events.New()
			runtime := newRuntimeWithPolicy(t.Context(), stream, runtimeOptions{timeout: 2 * time.Second}, nil)
			defer func() { _ = runtime.Close(context.Background()) }()
			if sample.Config.GlobalFence != nil {
				if err := globalfence.SetRules("sess-acc", sample.Config.GlobalFence); err != nil {
					t.Fatal(err)
				}
				defer globalfence.DropSession("sess-acc")
			}
			if sample.Config.ScopeRules != nil {
				if err := scoperules.SetRules("sess-acc", sample.Config.ScopeRules); err != nil {
					t.Fatal(err)
				}
				defer scoperules.DropSession("sess-acc")
			}
			decision := runtime.JudgeScopeRequest(proxy.ScopeRequest{
				Invocation: operation.Invocation{SessionID: "sess-acc"},
				Call:       parityHTTPCall(sample),
				Operation:  &operationpb.Ref{OperationId: "op-http-" + sample.Name},
			})
			if !decision.Judged || decision.Action != sample.WantAction || decision.RuleID != sample.WantRule {
				t.Fatalf("decision = %+v, want action=%s rule=%s", decision, sample.WantAction, sample.WantRule)
			}
			held := decision.Hold != nil
			if held != sample.WantHold {
				t.Fatalf("hold = %v, want %v", held, sample.WantHold)
			}
			t.Logf("VERDICT %s action=%s rule=%s hold=%t", sample.Name, decision.Action, decision.RuleID, held)
		})
	}
}

func parityHTTPCall(sample paritySample) *aop.ToolCall {
	value := map[string]any{"method": sample.Method, "url": sample.URL}
	if sample.Passive {
		value["passive"] = true
	}
	data, _ := json.Marshal(value)
	return &aop.ToolCall{Name: "http.request", Arguments: &aop.EncodedValue{Data: data}}
}

func firstParityAudit(mu sync.Mutex, decisions []string) string {
	mu.Lock()
	defer mu.Unlock()
	for _, reason := range decisions {
		if strings.HasPrefix(reason, "builtin.") {
			return reason
		}
	}
	return ""
}

func waitParityReview(t *testing.T, stream *events.Stream, operationID string) {
	t.Helper()
	found := make(chan struct{}, 1)
	stop := stream.Observe(func(event *aop.Event) {
		var r Review
		ext := event.GetExtension()
		if ext == nil || !ext.MessageIs(&r) || ext.UnmarshalTo(&r) != nil {
			return
		}
		if r.GetState() == ReviewState_REVIEW_STATE_PENDING && r.GetOperation().GetOperationId() == operationID {
			found <- struct{}{}
		}
	})
	defer stop.Cancel()
	select {
	case <-found:
	case <-time.After(2 * time.Second):
		t.Fatal("pending review never appeared")
	}
}
