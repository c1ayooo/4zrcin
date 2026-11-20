// Package aop is the client side of the Agent Orchestration Protocol: the
// control plane speaking harness's own language instead of inventing one.
//
// Transport: the WebSocket compatibility layer harness ships for browsers
// (`/api/aop/application/ws`). One connection carries one bidirectional stream of
// `aop.Envelope` messages, each frame a marshalled protobuf. Requests carry a
// ProtocolMessage in the Any payload; the reply comes back with `reply_to` set
// to the request id, and events arrive on the same socket while a turn runs.
// (The Connect/gRPC transport is the same service, but a bidi Connect stream
// needs HTTP/2 and the harness serves cleartext HTTP/1.1 here.)
package aop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	pb "github.com/kksty/tianwu/internal/aop/pb"
	"github.com/kksty/tianwu/internal/ws"
)

// Client dials one harness server (aiscan web: the process that hosts the AOP
// application transport and the agent pool).
type Client struct {
	BaseURL string
	Token   string
}

func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token}
}

// Conn is one open Envelope stream. It multiplexes replies and events by
// request id, so callers see request/response while events keep flowing.
type Conn struct {
	sock    *ws.Conn
	events  chan *pb.Event
	replies chan *pb.Envelope
	errs    chan error
	// dropped counts events the reader could not hand over. A ledger that
	// silently loses events is worse than one that says how many it lost, so
	// the executor appends this to the run's reason instead of hiding it.
	dropped atomic.Int64
	// cursor is the last delivery cursor seen on this stream. A resumed turn
	// passes it back as after_cursor, so the host replays only the new tail
	// instead of the whole session.
	cursor atomic.Pointer[string]
	// readErr is why the reader stopped. Without it a closed events channel says
	// only that the stream ended: a peer's close frame and a broken transport
	// would look the same, and the turn's failure could not be attributed.
	readErr atomic.Pointer[error]
	// watchID is the envelope id of the current WatchEvents subscription, if
	// any. The host sends that subscription's events under it and accepts a
	// CancelOperation naming it, so it is also the streaming operation's
	// identity.
	watchID atomic.Pointer[string]
}

// Dial opens the application socket and starts the reader.
func (c *Client) Dial(ctx context.Context) (*Conn, error) {
	url := applicationWSURL(c.BaseURL)
	header := http.Header{}
	if c.Token != "" {
		header.Set("Authorization", "Bearer "+c.Token)
	}
	sock, err := ws.Dial(ctx, url, header)
	if err != nil {
		return nil, fmt.Errorf("aop: 连接 %s：%w", url, err)
	}
	conn := &Conn{
		sock:    sock,
		events:  make(chan *pb.Event, 512),
		replies: make(chan *pb.Envelope, 16),
		errs:    make(chan error, 1),
	}
	go conn.read()
	return conn, nil
}

func applicationWSURL(base string) string {
	u := base
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return strings.TrimRight(u, "/") + "/api/aop/application/ws"
}

func (c *Conn) read() {
	for {
		raw, err := c.sock.ReadMessage()
		if err != nil {
			cause := err
			c.readErr.Store(&cause)
			select {
			case c.errs <- err:
			default:
			}
			close(c.events)
			close(c.replies)
			return
		}
		var env pb.Envelope
		if err := proto.Unmarshal(raw, &env); err != nil {
			continue
		}
		if env.Payload == nil {
			continue
		}
		// The cursor travels on the envelope, not on a namespace payload.
		if dc := env.GetDeliveryCursor(); dc != "" {
			c.cursor.Store(&dc)
		}
		// Only the core ProtocolMessage namespace carries turn events. Other
		// registered namespaces (commands, traffic, guardrail) keep their reply
		// envelopes for the matching caller, which decodes the Any by its
		// type_url into its own message.
		var msg pb.ProtocolMessage
		if env.Payload.UnmarshalTo(&msg) == nil {
			if ev := msg.GetEvent(); ev != nil {
				select {
				case c.events <- ev:
				default:
					c.dropped.Add(1)
				}
				continue
			}
		}
		select {
		case c.replies <- &env:
		default:
		}
	}
}

// Events is the stream of turn events for every session on this connection.
func (c *Conn) Events() <-chan *pb.Event { return c.events }

// Dropped is how many events were discarded because the consumer fell behind.
func (c *Conn) Dropped() int { return int(c.dropped.Load()) }

// ReadError is why the event stream ended; nil while it is still open.
func (c *Conn) ReadError() error {
	if p := c.readErr.Load(); p != nil {
		return *p
	}
	return nil
}

// DisconnectReason turns the reader's terminal error into the conclusion a run
// needs: who ended the stream, and whether that is a transport failure. Only the
// transport case says the link itself is unstable; a peer that closed on purpose
// and said why is an intentional end, which is a different finding.
func DisconnectReason(err error) string {
	var closed *ws.CloseError
	switch {
	case err == nil:
		return "aop: 回合结束前连接断开（读端没有留下原因）"
	case errors.As(err, &closed):
		return fmt.Sprintf("aop: 回合结束前连接断开（宿主主动关闭，code=%d，%s）", closed.Code, closed.Text)
	case errors.Is(err, io.EOF):
		return "aop: 回合结束前连接断开（对端关闭了 TCP，但没有给出关闭原因；传输层本身没有报错）"
	default:
		return fmt.Sprintf("aop: 回合结束前连接断开（传输错误：%v）", err)
	}
}

// Cursor is the last delivery cursor seen on the stream. A resumed turn uses it
// as after_cursor so the host resends only the tail, not the whole session.
func (c *Conn) Cursor() string {
	if p := c.cursor.Load(); p != nil {
		return *p
	}
	return ""
}

// send writes one request message in an envelope under the given id. Any
// registered namespace message is accepted: the transport is generic, the
// payload is opaque to the envelope layer.
func (c *Conn) send(id string, msg proto.Message) error {
	payload, err := anypb.New(msg)
	if err != nil {
		return err
	}
	raw, err := proto.Marshal(&pb.Envelope{Id: id, Payload: payload})
	if err != nil {
		return err
	}
	return c.sock.WriteBinary(raw)
}

// callWithID sends one request under a fixed envelope id and waits for the
// envelope that answers it. The id is caller-supplied so an idempotent retry can
// ask about the same request instead of creating a second one: the host keys
// reply replay by envelope id.
func (c *Conn) callWithID(ctx context.Context, id string, msg proto.Message) (*pb.Envelope, error) {
	if err := c.send(id, msg); err != nil {
		return nil, err
	}
	return c.awaitReply(ctx, id)
}

// awaitReply waits for the envelope whose reply_to is id.
func (c *Conn) awaitReply(ctx context.Context, id string) (*pb.Envelope, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-c.errs:
			return nil, err
		case env, ok := <-c.replies:
			if !ok {
				// The reader closes replies after a read error. Report why the
				// stream ended rather than a generic "disconnected": a
				// transport timeout here is what the turn retry recognises.
				select {
				case err := <-c.errs:
					return nil, err
				default:
				}
				return nil, errors.New("aop: 连接已断开")
			}
			if env.GetReplyTo() != id {
				continue // another request's reply; the protocol allows interleaving
			}
			return env, nil
		}
	}
}

// callRequest sends one namespace request under a fresh envelope id and returns
// its raw reply envelope. The caller decodes the reply with decodeReply into
// the message type its namespace defines.
func (c *Conn) callRequest(ctx context.Context, msg proto.Message) (*pb.Envelope, error) {
	return c.callWithID(ctx, newID(), msg)
}

// decodeReply decodes one reply Any into out by its type_url. A refusal travels
// as a core ProtocolError payload for every namespace, not as a broken request,
// so it is checked first and returned as the error; everything else must decode
// into the caller's own message type.
func decodeReply(env *pb.Envelope, out proto.Message) error {
	if env == nil || env.GetPayload() == nil {
		return errors.New("aop: 回复为空")
	}
	var core pb.ProtocolMessage
	if env.Payload.UnmarshalTo(&core) == nil {
		if pe := core.GetProtocolError(); pe != nil {
			return fmt.Errorf("aop: %s：%s", pe.GetCode(), pe.GetMessage())
		}
	}
	if err := env.Payload.UnmarshalTo(out); err != nil {
		return errors.New("aop: 回复无法解析")
	}
	return nil
}

// call sends one core ProtocolMessage and decodes the core reply. The public
// API of the core path is unchanged; namespace callers use callRequest and
// decodeReply directly.
func (c *Conn) call(ctx context.Context, msg *pb.ProtocolMessage) (*pb.Envelope, *pb.ProtocolMessage, error) {
	env, err := c.callRequest(ctx, msg)
	if err != nil {
		return nil, nil, err
	}
	var out pb.ProtocolMessage
	if err := decodeReply(env, &out); err != nil {
		return env, &out, err
	}
	return env, &out, nil
}

// OpenSession asks for a session, optionally pinned to a node. workDir is the
// session's workspace (the case directory); it travels as a Tianwu fork
// extension because upstream AOP has no per-session working directory (D110).
func (c *Conn) OpenSession(ctx context.Context, nodeID, title, workDir string) (*pb.Session, error) {
	req := &pb.OpenSessionRequest{NodeId: nodeID, Title: title}
	if extension := WorkDirExtension(workDir); extension != nil {
		req.Extensions = []*anypb.Any{extension}
	}
	_, reply, err := c.call(ctx, &pb.ProtocolMessage{Message: &pb.ProtocolMessage_OpenSessionRequest{
		OpenSessionRequest: req,
	}})
	if err != nil {
		return nil, err
	}
	resp := reply.GetOpenSessionResponse()
	if resp == nil {
		return nil, fmt.Errorf("aop: 意外的回复 %T", reply.Message)
	}
	if rej := resp.GetRejected(); rej != nil {
		return nil, fmt.Errorf("aop: 会话被拒：%s %s", rej.GetCode(), rej.GetMessage())
	}
	return resp.GetAccepted(), nil
}

// TurnOptions is how a turn relates to the session it runs in.
type TurnOptions struct {
	// Continue reuses the session's existing conversation instead of starting a
	// new one: the input is delivered as the next user message but is not
	// re-published as a new session opening (the host then keeps the context).
	Continue bool
	// MaxTurns caps the host agent loop for this turn; 0 leaves the host default.
	MaxTurns uint32
	// Eval is the intent's acceptance criterion. It travels as the harness's
	// AgentRunOptions extension, not as text in the brief: the evaluator is a
	// different role and must not judge the executor's prompt.
	Eval string
	// Goal is the short control-plane goal the evaluator judges. It travels as
	// the Tianwu RunGoal extension, separate from the turn input (which stays
	// the executor's full brief). Empty leaves the upstream behavior: the
	// evaluator derives its goal from the turn input.
	Goal string
	// EvalRounds is the evaluator's pacing spec (a hard round number or natural
	// language). Empty leaves the harness default ceiling. It rides the same
	// RunGoal extension so old harnesses that ignore it keep their default.
	EvalRounds string
}

const agentRunOptionsTypeURL = "type.googleapis.com/cyber.agent.AgentRunOptions"

// agentRunOptions encodes the extension by hand. tianwu does not link the whole
// harness module just for one two-field message; the wire format is stable and
// the harness validates it with the real proto on its side.
func agentRunOptions(criteria string) *anypb.Any {
	var b []byte
	if criteria = strings.TrimSpace(criteria); criteria != "" {
		b = protowire.AppendTag(b, 1, protowire.BytesType) // eval_criteria
		b = protowire.AppendString(b, criteria)
	}
	return &anypb.Any{TypeUrl: agentRunOptionsTypeURL, Value: b}
}

// RunTurn sends one prompt. Events for it come back on Events().
func (c *Conn) RunTurn(ctx context.Context, sessionID, turnID, prompt string) (*pb.TurnReceipt, error) {
	return c.RunTurnOpts(ctx, sessionID, turnID, prompt, TurnOptions{})
}

// RunTurnOpts is RunTurn with an explicit continuation choice.
func (c *Conn) RunTurnOpts(ctx context.Context, sessionID, turnID, prompt string, opts TurnOptions) (*pb.TurnReceipt, error) {
	msg := &pb.Message{
		Id:      newID(),
		Role:    "user",
		Content: []*pb.Content{{Value: &pb.Content_Text{Text: &pb.TextContent{Text: prompt}}}},
	}
	req := &pb.RunTurnRequest{
		SessionId:       sessionID,
		TurnId:          turnID,
		Input:           msg,
		ContinueSession: opts.Continue,
		MaxTurns:        opts.MaxTurns,
	}
	if strings.TrimSpace(opts.Eval) != "" {
		req.Extensions = append(req.Extensions, agentRunOptions(opts.Eval))
	}
	if extension := RunGoalExtension(opts.Goal, opts.EvalRounds); extension != nil {
		req.Extensions = append(req.Extensions, extension)
	}
	request := &pb.ProtocolMessage{Message: &pb.ProtocolMessage_RunTurnRequest{
		RunTurnRequest: req,
	}}
	// One fixed envelope id for the attempt and its retry. The host stores the
	// request's response under that id, so the retry asks "what happened to this
	// request?" and cannot open a second turn; a business rejection is a
	// decoded answer, never a timeout, so it is not retried.
	id := newID()
	env, err := c.callWithID(ctx, id, request)
	if retryableTurnError(ctx, err) {
		env, err = c.callWithID(ctx, id, request)
	}
	if err != nil {
		return nil, err
	}
	var reply pb.ProtocolMessage
	if err := decodeReply(env, &reply); err != nil {
		return nil, err
	}
	resp := reply.GetRunTurnResponse()
	if resp == nil {
		return nil, fmt.Errorf("aop: 意外的回复 %T", reply.Message)
	}
	if rej := resp.GetRejected(); rej != nil {
		return nil, fmt.Errorf("aop: 回合被拒：%s %s", rej.GetCode(), rej.GetMessage())
	}
	return resp.GetAccepted(), nil
}

// retryableTurnError reports whether a failed turn submission is a lost reply
// on the wire worth one idempotent retry. A caller-side deadline is the run's
// own budget, not a transient transport hiccup: retrying after it would only
// burn a frame on a dead context.
func retryableTurnError(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() == nil && isTransportTimeout(err)
}

// isTransportTimeout reports whether err is a timeout on the wire rather than a
// protocol answer: a network read/write deadline or a deadline-exceeded
// transport error. It is what separates "the reply may have been lost" from
// "the host refused the turn".
func isTransportTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// CancelTurn asks the node to stop; the stream still ends with turn_ended.
func (c *Conn) CancelTurn(ctx context.Context, sessionID, turnID, reason string) error {
	_, reply, err := c.call(ctx, &pb.ProtocolMessage{Message: &pb.ProtocolMessage_CancelTurnRequest{
		CancelTurnRequest: &pb.CancelTurnRequest{SessionId: sessionID, TurnId: turnID, Reason: reason},
	}})
	if err != nil {
		return err
	}
	if resp := reply.GetCancelTurnResponse(); resp != nil {
		if rej := resp.GetRejected(); rej != nil {
			return fmt.Errorf("aop: 取消失败：%s %s", rej.GetCode(), rej.GetMessage())
		}
	}
	return nil
}

// CloseSession ends the session on the node.
func (c *Conn) CloseSession(ctx context.Context, sessionID, reason string) error {
	_, _, err := c.call(ctx, &pb.ProtocolMessage{Message: &pb.ProtocolMessage_CloseSessionRequest{
		CloseSessionRequest: &pb.CloseSessionRequest{SessionId: sessionID, Reason: reason},
	}})
	return err
}

// Watch subscribes to a session's events. It is a subscription, not an RPC: the
// server answers with the event stream itself (envelopes whose reply_to is this
// request), so there is nothing to wait for here.
//
// The request's envelope id is the subscription's identity: events are
// delivered under it and a CancelOperation naming it releases the subscription
// on the host. Watch records it so the caller can cancel explicitly instead of
// leaving the operation to socket teardown.
func (c *Conn) Watch(ctx context.Context, sessionID, afterCursor string) error {
	id := newID()
	if err := c.send(id, &pb.ProtocolMessage{Message: &pb.ProtocolMessage_WatchEventsRequest{
		WatchEventsRequest: &pb.WatchEventsRequest{SessionId: sessionID, AfterCursor: afterCursor},
	}}); err != nil {
		return err
	}
	c.watchID.Store(&id)
	return nil
}

// WatchID is the envelope id of the current event subscription ("" before
// Watch). It is the streaming operation's identity.
func (c *Conn) WatchID() string {
	if p := c.watchID.Load(); p != nil {
		return *p
	}
	return ""
}

// CancelOperation asks the host to stop one operation by the envelope id it
// started under. The application endpoint cancels the matching event
// subscription / PTY route and sends no reply, so this is fire-and-forget: only
// the write is reported. Best effort by design — a turn's result does not
// depend on the cancel frame arriving.
func (c *Conn) CancelOperation(targetID, reason string) error {
	if strings.TrimSpace(targetID) == "" {
		return errors.New("aop: CancelOperation 缺少 target_id")
	}
	return c.send(newID(), &pb.ProtocolMessage{Message: &pb.ProtocolMessage_CancelOperation{
		CancelOperation: &pb.CancelOperation{TargetId: targetID, Reason: reason},
	}})
}

// CancelWatch releases the current event subscription, if Watch was called.
// Clearing the recorded id first keeps a later Close from sending a second
// cancel for the same subscription.
func (c *Conn) CancelWatch(reason string) error {
	for {
		p := c.watchID.Load()
		if p == nil {
			return nil
		}
		if c.watchID.CompareAndSwap(p, nil) {
			return c.CancelOperation(*p, reason)
		}
	}
}

func (c *Conn) Close() error {
	// A subscription outlives the stream unless the host is told: make the end
	// explicit before dropping the socket. Best effort; closing still wins.
	_ = c.CancelWatch("tianwu: 连接关闭")
	return c.sock.Close()
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
