package aopexec

import (
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	pb "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/types"
)

// The fork announces a lost transcript as a system message carrying the
// WebMessageMetadata extension with code "session_context_reset" (cyber-harness
// pkg/web/service/events.go: the node recreated a session the hub holds
// history for, so the agent resumes with an empty context). The generated type
// is the engine's types.WebMessageMetadata; field 2 of the message is the code
// string.
const (
	sessionContextResetCode   = "session_context_reset"
	webMessageMetadataURLPart = "cyber.agent.WebMessageMetadata"
)

// sessionContextResetText is what lands in the run journal when the
// announcement passes through a run. It names the consequence, not a promise:
// this round finishes as it is; the next dispatch rebuilds the session from a
// full brief because the resume pointer below is gone.
const sessionContextResetText = "会话上下文已重置：节点丢失了对话历史（节点重启或会话被逐出，Hub 侧历史仍在）。" +
	"本轮照常收尾；下一轮将按「没有可续的会话」带完整任务书重建，不再把空会话当成有历史"

// isSessionContextReset reports whether the event is that announcement for
// the given session.
func isSessionContextReset(ev *pb.Event, sessionID string) bool {
	if ev.GetSessionId() != sessionID || ev.GetEmitter() != "cyber.web" {
		return false
	}
	msg := ev.GetMessage()
	if msg == nil || msg.GetRole() != "system" {
		return false
	}
	for _, ext := range ev.GetExtensions() {
		if !strings.HasSuffix(ext.GetTypeUrl(), webMessageMetadataURLPart) {
			continue
		}
		return extensionCode(ext.GetValue()) == sessionContextResetCode
	}
	return false
}

// extensionCode reads field 2 (code) from the serialized metadata. A payload
// that cannot be decoded is not a reset: it returns the empty string, which
// never equals the reset code.
func extensionCode(raw []byte) string {
	var msg types.WebMessageMetadata
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return ""
	}
	return msg.GetCode()
}

// contextResetEvent builds the wire event the harness publishes when a node
// recreates a session the hub holds history for: a system message whose
// WebMessageMetadata sidecar carries the reset code. Test fixture and decoder
// stay in one file so the wire shape cannot drift silently.
func contextResetEvent(sessionID string) *pb.Event {
	raw, err := proto.Marshal(&types.WebMessageMetadata{Code: sessionContextResetCode})
	if err != nil {
		panic("WebMessageMetadata{Code} cannot fail to marshal: " + err.Error())
	}
	ev := &pb.Event{
		Id:        "ev-reset",
		SessionId: sessionID,
		Emitter:   "cyber.web",
		Payload: &pb.Event_Message{Message: &pb.Message{
			Id:      "m-reset",
			Role:    "system",
			Content: []*pb.Content{{Value: &pb.Content_Text{Text: &pb.TextContent{Text: "The node recreated this session."}}}},
		}},
	}
	ev.Extensions = append(ev.Extensions, &anypb.Any{
		TypeUrl: "type.googleapis.com/" + webMessageMetadataURLPart,
		Value:   raw,
	})
	return ev
}
