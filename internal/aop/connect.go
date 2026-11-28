package aop

// The management RPCs are Connect unary calls, served by the harness as JSON
// over cleartext HTTP/1.1. They ride the engine's generated Connect clients
// (cyber-harness pkg/rpc: SessionService, ConfigService) in protojson wire
// format, so the request and reply field names are the engine's types and the
// harness upgrade that moves a service fails here with a clear error instead
// of silently decoding something else.
//
// This file is deliberately separate from client.go: the WebSocket client above
// is the execution path (turns, events, PTY), while these calls are reads and
// one bounded config write. The shell's own DTOs below are unchanged — the
// console consumes their JSON, and §14 keeps the outer field names shell-owned.
//
// TestLLM is the one call that does not ride the generated client: the shell's
// tolerant decode of the answer (a latency value the console cannot read is 0,
// never an error) is pinned by connect_test.go, and protojson cannot express
// it — an int64 field carrying "1608ms" fails the whole document. The request
// is still the engine's type so the field names stay canonical; only the POST
// is manual.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	pb "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/pkg/rpc"
	"google.golang.org/protobuf/encoding/protojson"
)

// ConnectError is one rejection from a Connect unary call: the HTTP error
// carried a code and a message. Callers use the code to tell "the host refused
// this request" from "the host is unreachable", and never report a refusal as
// success. It is a value so errors.As works across wrapped errors.
type ConnectError struct {
	// RPC is the method that failed ("ListEvents", "ResetSession", ...).
	RPC string
	// Code is the Connect code (invalid_argument, not_found, unauthenticated…).
	Code string
	// Message is the host's message, verbatim.
	Message string
	// Status is the HTTP status the transport answered with.
	Status int
}

func (e *ConnectError) Error() string {
	code := strings.TrimSpace(e.Code)
	if code == "" {
		code = fmt.Sprintf("HTTP %d", e.Status)
	}
	if msg := strings.TrimSpace(e.Message); msg != "" {
		return fmt.Sprintf("aop: %s 被宿主拒绝（%s）：%s", e.RPC, code, msg)
	}
	return fmt.Sprintf("aop: %s 被宿主拒绝（%s）", e.RPC, code)
}

// IsConnectCode reports whether err is a Connect rejection with one of the
// given codes. It matches case-insensitively because Connect's JSON code and
// the harness's own AOP rejection codes use different spellings.
func IsConnectCode(err error, codes ...string) bool {
	var ce *ConnectError
	if !errors.As(err, &ce) {
		return false
	}
	for _, code := range codes {
		if strings.EqualFold(strings.TrimSpace(code), strings.TrimSpace(ce.Code)) {
			return true
		}
	}
	return false
}

// IsConnectNotFound reports the host "session not found" answer. The AOP stream
// spells the same condition NOT_FOUND, so both are accepted here rather than
// making every caller compare strings.
func IsConnectNotFound(err error) bool {
	return IsConnectCode(err, "not_found", "NOT_FOUND")
}

// SessionEvents is one page of canonical session history.
type SessionEvents struct {
	Events     []SessionEvent `json:"events"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// SessionEvent is one delivered event. The event payload stays raw JSON for the
// caller to relay: it is the harness's proto JSON, and rebuilding a second
// projection here would drift from the session store.
type SessionEvent struct {
	Cursor string          `json:"cursor"`
	Event  json.RawMessage `json:"event"`
}

// SessionCommand is one native command a session's console can run.
type SessionCommand struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	Usage       string   `json:"usage,omitempty"`
	Description string   `json:"description,omitempty"`
}

// SessionReset is the outcome of ResetSession: either the receipt of the
// replacement session or the host's rejection.
type SessionReset struct {
	RequestID string
	Accepted  bool
	// Previous is the session the host closed; Current is the replacement.
	Previous string
	Current  string
	Code     string
	Message  string
}

// LLMProbe is a TestLLM/ListModels request. Empty values ask the host to probe
// its effective runtime; explicit values probe a candidate connection without
// saving it, which is what the settings page's "test" button needs.
type LLMProbe struct {
	ProfileID string `json:"profileId,omitempty"`
	Provider  string `json:"provider,omitempty"`
	BaseURL   string `json:"baseUrl,omitempty"`
	APIKey    string `json:"apiKey,omitempty"`
	Model     string `json:"model,omitempty"`
	Proxy     string `json:"proxy,omitempty"`
}

// proto converts the shell DTO into the engine's request type; the field names
// on the wire are the engine's from here on.
func (p LLMProbe) proto() *types.LLMProbeRequest {
	return &types.LLMProbeRequest{
		ProfileId: p.ProfileID,
		Provider:  p.Provider,
		BaseUrl:   p.BaseURL,
		ApiKey:    p.APIKey,
		Model:     p.Model,
		Proxy:     p.Proxy,
	}
}

// LLMProbeResult is the host's TestLLM answer.
type LLMProbeResult struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	Reply     string `json:"reply,omitempty"`
	Error     string `json:"error,omitempty"`
}

// UnmarshalJSON reads the host's TestLLM answer tolerantly. The host serves
// Connect's JSON through protojson, which writes 64-bit integers as decimal
// strings — {"latencyMs":"1608"} — so decoding the field as a plain int64 failed
// on every real answer. encoding/json then discarded the whole document, and a
// cosmetic latency field turned the host's definite verdict (provider, served
// model, its own error text) into "the probe could not be read", which made the
// settings page fall back to a second, different probe.
//
// A value the console cannot read is therefore 0, never an error: no field of a
// probe answer may invalidate the rest of it.
func (r *LLMProbeResult) UnmarshalJSON(raw []byte) error {
	var wire struct {
		OK        bool    `json:"ok"`
		Provider  string  `json:"provider"`
		Model     string  `json:"model"`
		LatencyMS flexInt `json:"latencyMs"`
		Reply     string  `json:"reply"`
		Error     string  `json:"error"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	*r = LLMProbeResult{
		OK:        wire.OK,
		Provider:  wire.Provider,
		Model:     wire.Model,
		LatencyMS: int64(wire.LatencyMS),
		Reply:     wire.Reply,
		Error:     wire.Error,
	}
	return nil
}

// flexInt is a 64-bit integer written either as protojson writes it (a decimal
// string) or as a hand-built document may write it (a number). A unit suffix
// ("1608ms") is dropped rather than rejected, and anything unreadable is 0.
type flexInt int64

func (v *flexInt) UnmarshalJSON(raw []byte) error {
	*v = 0
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil
	}
	text = strings.TrimSpace(strings.Trim(text, `"`))
	end := 0
	for end < len(text) {
		c := text[end]
		if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return nil
	}
	if n, err := strconv.ParseFloat(text[:end], 64); err == nil {
		*v = flexInt(int64(n))
	}
	return nil
}

// LLMModels is the host's ListModels answer. Supported=false means the provider
// has no catalogue endpoint; that is not an error.
type LLMModels struct {
	OK        bool     `json:"ok"`
	Supported bool     `json:"supported"`
	Models    []string `json:"models,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// connectStatus records the HTTP status of one generated-client call.
// connect-go turns every non-2xx into a *connect.Error and drops the status;
// ConnectError keeps carrying it because an operator reading a rejection wants
// to know what the transport actually said.
type connectStatus struct {
	code int
}

type connectStatusKey struct{}

// connectTransport injects the shell's bearer token and copies the response
// status into the per-call cell. Base transport is http.DefaultTransport, so
// connection pooling behaves exactly like the calls it replaces.
type connectTransport struct {
	token string
}

func (t connectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if cell, _ := req.Context().Value(connectStatusKey{}).(*connectStatus); cell != nil {
		cell.code = resp.StatusCode
	}
	return resp, nil
}

// sessionRPC pins a per-call status cell into ctx and returns the generated
// SessionService client plus the updated ctx (the transport reads the cell out
// of it) and the cell itself.
func (c *Client) sessionRPC(ctx context.Context) (rpc.SessionServiceClient, context.Context, *connectStatus) {
	status := &connectStatus{}
	ctx = context.WithValue(ctx, connectStatusKey{}, status)
	httpClient := &http.Client{Transport: connectTransport{token: c.Token}}
	return rpc.NewSessionServiceClient(httpClient, c.BaseURL, connect.WithProtoJSON()), ctx, status
}

// configRPC pins a per-call status cell into ctx and returns the generated
// ConfigService client plus the updated ctx and the cell.
func (c *Client) configRPC(ctx context.Context) (rpc.ConfigServiceClient, context.Context, *connectStatus) {
	status := &connectStatus{}
	ctx = context.WithValue(ctx, connectStatusKey{}, status)
	httpClient := &http.Client{Transport: connectTransport{token: c.Token}}
	return rpc.NewConfigServiceClient(httpClient, c.BaseURL, connect.WithProtoJSON()), ctx, status
}

// rejection maps a failed generated-client call onto the shell's ConnectError.
// A call whose wire never answered (connect-go reports CodeUnavailable and the
// cell holds no status) is a transport failure, not a rejection: it keeps the
// plain wrapping the callers already match on.
func rejection(baseURL, procedure string, err error, status *connectStatus) error {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return err
	}
	if ce.Code() == connect.CodeUnavailable && status.code == 0 {
		return fmt.Errorf("aop: %s 连不上宿主 %s：%w", procedure, baseURL, ce)
	}
	return &ConnectError{RPC: procedure, Code: ce.Code().String(), Message: ce.Message(), Status: status.code}
}

// connectPost is the raw Connect unary POST for the one call that cannot ride
// the generated client (see the file comment on ProbeLLM): bearer token,
// protojson body, the host's error document kept verbatim.
func (c *Client) connectPost(ctx context.Context, procedure string, body []byte) ([]byte, int, error) {
	url := c.BaseURL + "/" + procedure
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("aop: %s 请求无法创建：%w", procedure, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("aop: %s 连不上宿主 %s：%w", procedure, url, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if readErr != nil {
		return nil, resp.StatusCode, fmt.Errorf("aop: %s 读取回复失败：%w", procedure, readErr)
	}
	return raw, resp.StatusCode, nil
}

// decodeConnectError extracts {"code","message"} from a Connect JSON error
// document. Empty code means "not an error document".
func decodeConnectError(raw []byte) (code, message string) {
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		// Some intermediaries produce {"error": "..."} with no message.
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", ""
	}
	code = strings.TrimSpace(body.Code)
	message = strings.TrimSpace(body.Message)
	if message == "" && code == "" {
		message = strings.TrimSpace(body.Error)
	}
	return code, message
}

// Events reads canonical history for one session, after a cursor. The control
// plane relays these events read-only; it never dispatches or replays them.
func (c *Client) Events(ctx context.Context, sessionID, afterCursor string, limit int) (*SessionEvents, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("aop: ListEvents 缺少 session_id")
	}
	session, ctx, status := c.sessionRPC(ctx)
	req := connect.NewRequest(&pb.ListEventsRequest{
		SessionId:   sessionID,
		AfterCursor: afterCursor,
		Limit:       uint32(max(limit, 0)),
	})
	resp, err := session.ListEvents(ctx, req)
	if err != nil {
		return nil, rejection(c.BaseURL, "ListEvents", err, status)
	}
	page := &SessionEvents{
		NextCursor: resp.Msg.GetNextCursor(),
		Events:     make([]SessionEvent, 0, len(resp.Msg.GetEvents())),
	}
	for _, delivery := range resp.Msg.GetEvents() {
		// The event payload is relayed as the harness's proto JSON. Decoding
		// and re-serialising keeps every field name; only protojson's own
		// (deliberately unstable) whitespace may differ from the host's bytes.
		event := json.RawMessage("null")
		if delivery.GetEvent() != nil {
			raw, err := protojson.Marshal(delivery.GetEvent())
			if err != nil {
				return nil, fmt.Errorf("aop: ListEvents 事件无法编码：%w", err)
			}
			event = raw
		}
		page.Events = append(page.Events, SessionEvent{Cursor: delivery.GetCursor(), Event: event})
	}
	return page, nil
}

// Commands lists the native command menu for one session.
func (c *Client) Commands(ctx context.Context, sessionID string) ([]SessionCommand, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("aop: ListCommands 缺少 session_id")
	}
	session, ctx, status := c.sessionRPC(ctx)
	resp, err := session.ListCommands(ctx, connect.NewRequest(&types.ListCommandsRequest{SessionId: sessionID}))
	if err != nil {
		return nil, rejection(c.BaseURL, "ListCommands", err, status)
	}
	commands := make([]SessionCommand, 0, len(resp.Msg.GetCommands()))
	for _, spec := range resp.Msg.GetCommands() {
		commands = append(commands, SessionCommand{
			Name:        spec.GetName(),
			Aliases:     spec.GetAliases(),
			Usage:       spec.GetUsage(),
			Description: spec.GetDescription(),
		})
	}
	return commands, nil
}

// ResetSession replaces a session with a fresh one on the same node. The
// request id makes the call idempotent on the host: retrying with the same id
// replays the receipt instead of opening a second session. The old session must
// still be known to the host (a NOT_FOUND rejection is returned as such).
func (c *Client) ResetSession(ctx context.Context, requestID, sessionID, newSessionID, title string) (SessionReset, error) {
	outcome := SessionReset{RequestID: requestID}
	session, ctx, status := c.sessionRPC(ctx)
	resp, err := session.ResetSession(ctx, connect.NewRequest(&types.ResetSessionRequest{
		RequestId:    requestID,
		SessionId:    sessionID,
		NewSessionId: newSessionID,
		Title:        title,
	}))
	if err != nil {
		return outcome, rejection(c.BaseURL, "ResetSession", err, status)
	}
	// A rejection inside a 200 answer is still a rejection.
	if rejected := resp.Msg.GetRejected(); rejected != nil {
		outcome.Code, outcome.Message = rejected.GetCode(), rejected.GetMessage()
		return outcome, &ConnectError{RPC: "ResetSession", Code: rejected.GetCode(), Message: rejected.GetMessage(), Status: http.StatusOK}
	}
	accepted := resp.Msg.GetAccepted()
	if accepted == nil {
		return outcome, errors.New("aop: ResetSession 回复既没有 accepted 也没有 rejected")
	}
	outcome.Accepted = true
	outcome.Previous = accepted.GetPrevious().GetId()
	outcome.Current = accepted.GetCurrent().GetSession().GetId()
	return outcome, nil
}

// ProbeLLM runs the host's own lightweight inference probe. An explicit
// candidate connection is tested without saving it. See the file comment for
// why this one call keeps a manual POST.
func (c *Client) ProbeLLM(ctx context.Context, probe LLMProbe) (LLMProbeResult, error) {
	reqDoc, err := protojson.Marshal(probe.proto())
	if err != nil {
		return LLMProbeResult{}, fmt.Errorf("aop: TestLLM 请求无法编码：%w", err)
	}
	raw, status, err := c.connectPost(ctx, "cyber.rpc.config.ConfigService/TestLLM", reqDoc)
	if err != nil {
		return LLMProbeResult{}, err
	}
	if status < 200 || status >= 300 {
		code, message := decodeConnectError(raw)
		return LLMProbeResult{}, &ConnectError{RPC: "TestLLM", Code: code, Message: message, Status: status}
	}
	var out LLMProbeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return LLMProbeResult{}, fmt.Errorf("aop: TestLLM 回复不是 JSON：%w", err)
	}
	return out, nil
}

// ListModels asks the host's endpoint for its catalogue.
func (c *Client) ListModels(ctx context.Context, probe LLMProbe) (LLMModels, error) {
	config, ctx, status := c.configRPC(ctx)
	resp, err := config.ListModels(ctx, connect.NewRequest(probe.proto()))
	if err != nil {
		return LLMModels{}, rejection(c.BaseURL, "ListModels", err, status)
	}
	result := resp.Msg
	return LLMModels{
		OK:        result.GetOk(),
		Supported: result.GetSupported(),
		Models:    result.GetModels(),
		Error:     result.GetError(),
	}, nil
}

// UpdateConfig pushes one shared configuration document (llm/agent/traffic) and
// returns the host's view. Product sections (mcp/search/guardrail/jev) are not
// shared: they are read by the scan node from its own file, so they are not
// pushed here. The document is decoded strictly against the engine's
// DistributeConfig: a key the engine does not define fails here, where the
// operator can see it, instead of travelling to the host to be dropped there.
func (c *Client) UpdateConfig(ctx context.Context, config map[string]any) (json.RawMessage, error) {
	doc, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("aop: UpdateConfig 请求无法编码：%w", err)
	}
	var dist types.DistributeConfig
	if err := protojson.Unmarshal(doc, &dist); err != nil {
		return nil, fmt.Errorf("aop: UpdateConfig 配置无法按引擎的 DistributeConfig 解析：%w", err)
	}
	cfg, ctx, status := c.configRPC(ctx)
	resp, err := cfg.UpdateConfig(ctx, connect.NewRequest(&types.UpdateConfigRequest{Config: &dist}))
	if err != nil {
		return nil, rejection(c.BaseURL, "UpdateConfig", err, status)
	}
	raw, err := protojson.Marshal(resp.Msg)
	if err != nil {
		return nil, fmt.Errorf("aop: UpdateConfig 回复无法编码：%w", err)
	}
	return json.RawMessage(raw), nil
}
