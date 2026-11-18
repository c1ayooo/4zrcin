package aop

// The management RPCs are Connect unary calls. Over HTTP/1.1 the harness serves
// them as plain JSON (the browser console and `agent/connectrpc` examples use
// the same transport), so the control plane can read canonical session history,
// list a session's native commands, reset a session and push a config without
// linking the whole harness module or opening a second AOP stream.
//
// This file is deliberately separate from client.go: the WebSocket client above
// is the execution path (turns, events, PTY), while these calls are reads and
// one bounded config write. Request fields are the proto JSON names
// (lowerCamelCase); a rejection is a Connect error response, not a broken
// document, and is surfaced with the host's own code and message.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Connect paths. They are the generated service names from
// cyber-harness pkg/rpc (SessionService, ConfigService); keep them literal so a
// harness upgrade that moves the services fails here with a clear 404 instead of
// silently decoding something else.
const (
	connectSessionService = "cyber.rpc.chat.SessionService"
	connectConfigService  = "cyber.rpc.config.ConfigService"
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

// LLMProbeResult is the host's TestLLM answer.
type LLMProbeResult struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	Reply     string `json:"reply,omitempty"`
	Error     string `json:"error,omitempty"`
}

// LLMModels is the host's ListModels answer. Supported=false means the provider
// has no catalogue endpoint; that is not an error.
type LLMModels struct {
	OK        bool     `json:"ok"`
	Supported bool     `json:"supported"`
	Models    []string `json:"models,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// ConnectJSON posts one Connect unary request and decodes its JSON answer. A
// non-2xx answer is a Connect error ({"code","message"}); a 200 whose body is
// itself an error object is also treated as a rejection, so a proxy that
// rewrites statuses cannot turn a refusal into a zero value.
func (c *Client) ConnectJSON(ctx context.Context, service, method string, in, out any) error {
	path := "/" + service + "/" + method
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("aop: %s 请求无法编码：%w", method, err)
	}
	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("aop: %s 请求无法创建：%w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("aop: %s 连不上宿主 %s：%w", method, url, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if readErr != nil {
		return fmt.Errorf("aop: %s 读取回复失败：%w", method, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, message := decodeConnectError(raw)
		return &ConnectError{RPC: method, Code: code, Message: message, Status: resp.StatusCode}
	}
	// A Connect error served with 200 is still an error (connect-go never does
	// this, but an intermediary may). Only check when out is absent or the body
	// is an error object, so a normal payload is never mistaken for one.
	if code, message := decodeConnectError(raw); code != "" && message != "" && out == nil {
		return &ConnectError{RPC: method, Code: code, Message: message, Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("aop: %s 回复不是 JSON：%w", method, err)
	}
	return nil
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
	request := map[string]any{"sessionId": sessionID}
	if strings.TrimSpace(afterCursor) != "" {
		request["afterCursor"] = afterCursor
	}
	if limit > 0 {
		request["limit"] = limit
	}
	var out struct {
		Events []struct {
			Cursor string          `json:"cursor"`
			Event  json.RawMessage `json:"event"`
		} `json:"events"`
		NextCursor string `json:"nextCursor"`
	}
	if err := c.ConnectJSON(ctx, connectSessionService, "ListEvents", request, &out); err != nil {
		return nil, err
	}
	page := &SessionEvents{NextCursor: out.NextCursor, Events: make([]SessionEvent, 0, len(out.Events))}
	for _, item := range out.Events {
		page.Events = append(page.Events, SessionEvent{Cursor: item.Cursor, Event: item.Event})
	}
	return page, nil
}

// Commands lists the native command menu for one session.
func (c *Client) Commands(ctx context.Context, sessionID string) ([]SessionCommand, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("aop: ListCommands 缺少 session_id")
	}
	var out struct {
		Commands []SessionCommand `json:"commands"`
	}
	if err := c.ConnectJSON(ctx, connectSessionService, "ListCommands", map[string]any{"sessionId": sessionID}, &out); err != nil {
		return nil, err
	}
	if out.Commands == nil {
		return []SessionCommand{}, nil
	}
	return out.Commands, nil
}

// ResetSession replaces a session with a fresh one on the same node. The
// request id makes the call idempotent on the host: retrying with the same id
// replays the receipt instead of opening a second session. The old session must
// still be known to the host (a NOT_FOUND rejection is returned as such).
func (c *Client) ResetSession(ctx context.Context, requestID, sessionID, newSessionID, title string) (SessionReset, error) {
	request := map[string]any{"requestId": requestID, "sessionId": sessionID}
	if strings.TrimSpace(newSessionID) != "" {
		request["newSessionId"] = newSessionID
	}
	if strings.TrimSpace(title) != "" {
		request["title"] = title
	}
	var out struct {
		RequestID string `json:"requestId"`
		Accepted  *struct {
			Previous struct {
				ID string `json:"id"`
			} `json:"previous"`
			Current struct {
				Session struct {
					ID string `json:"id"`
				} `json:"session"`
			} `json:"current"`
		} `json:"accepted"`
		Rejected *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"rejected"`
	}
	outcome := SessionReset{RequestID: requestID}
	if err := c.ConnectJSON(ctx, connectSessionService, "ResetSession", request, &out); err != nil {
		return outcome, err
	}
	if out.Rejected != nil {
		outcome.Code, outcome.Message = out.Rejected.Code, out.Rejected.Message
		return outcome, &ConnectError{RPC: "ResetSession", Code: out.Rejected.Code, Message: out.Rejected.Message, Status: http.StatusOK}
	}
	if out.Accepted == nil {
		return outcome, errors.New("aop: ResetSession 回复既没有 accepted 也没有 rejected")
	}
	outcome.Accepted = true
	outcome.Previous = out.Accepted.Previous.ID
	outcome.Current = out.Accepted.Current.Session.ID
	return outcome, nil
}

// ProbeLLM runs the host's own lightweight inference probe. An explicit
// candidate connection is tested without saving it.
func (c *Client) ProbeLLM(ctx context.Context, probe LLMProbe) (LLMProbeResult, error) {
	var out LLMProbeResult
	if err := c.ConnectJSON(ctx, connectConfigService, "TestLLM", probe, &out); err != nil {
		return out, err
	}
	return out, nil
}

// ListModels asks the host's endpoint for its catalogue.
func (c *Client) ListModels(ctx context.Context, probe LLMProbe) (LLMModels, error) {
	var out LLMModels
	if err := c.ConnectJSON(ctx, connectConfigService, "ListModels", probe, &out); err != nil {
		return out, err
	}
	return out, nil
}

// UpdateConfig pushes one shared configuration document (llm/agent/traffic) and
// returns the host's view. Product sections (mcp/search/guardrail/jev) are not
// shared: they are read by the scan node from its own file, so they are not
// pushed here.
func (c *Client) UpdateConfig(ctx context.Context, config map[string]any) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.ConnectJSON(ctx, connectConfigService, "UpdateConfig", map[string]any{"config": config}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
