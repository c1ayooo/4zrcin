package aop

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/c1ayooo/4zrcin/internal/connectretry"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/pkg/rpc"
)

// Agent is one agent node as the host reports it (ListAgents). The control
// plane uses WorkingDir for session placement and Commands for the live
// capability catalogue. Neither command registration nor Bound proves that
// scanner resources or the model provider are ready.
type Agent struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	OS          string `json:"os,omitempty"`
	WorkingDir  string `json:"working_dir,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	Bound       bool   `json:"bound,omitempty"`
	ConfigError string `json:"config_error,omitempty"`
	// Capabilities is the node's own announcement from the registration hello
	// (docs/04 §3.1): the harness base set, the product marker ("scan") and —
	// for trimmed recipes — the recipe id ("scan-capture"). An absent recipe
	// marker means full; the control plane routes on it conservatively.
	Capabilities []string      `json:"capabilities,omitempty"`
	Commands     []CommandSpec `json:"commands"`
}

// CommandSpec is the host's public command declaration, not an executable
// readiness check. It includes runtime commands, native tools and skills.
type CommandSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Usage       string `json:"usage,omitempty"`
}

// agentsHTTPClient is the client for the host's Connect/JSON management API. A
// variable only so a test can inject a dial failure; production never assigns
// it.
var agentsHTTPClient = http.DefaultClient

// Agents lists the agents registered into this host.
//
// The call rides the engine's generated AgentService client in protojson wire
// format, like the other management RPCs in connect.go: the field names come
// from the engine's type, so an engine upgrade carries them over without a
// hand-copied tag to edit here. Fields the engine adds are ignored, as they
// were by the hand-written decode (connect-go decodes protojson with
// DiscardUnknown). The shell's Agent and CommandSpec stay as they are — the
// console consumes their JSON.
//
// The dial gets the same connect budget as every other path to the host: a
// dropped SYN here failed a round before one byte of the request was written
// (2026-11-22: a full run died on `aop: 列出 agent 失败：Post … connectex …`).
// Only the connect phase is repeated — the request is rebuilt for each attempt,
// the error is a `*net.OpError` with Op="dial" (so nothing was sent), and the
// call is a list. A refusal, an answer or a read timeout after the request went
// out is returned as-is.
func (c *Client) Agents(ctx context.Context) ([]Agent, error) {
	resp, err := c.agentsDo(ctx)
	if err != nil {
		return nil, err
	}
	views := resp.Msg.GetAgents()
	out := make([]Agent, 0, len(views))
	for _, view := range views {
		hello, runtime, status := view.GetHello(), view.GetHello().GetRuntime(), view.GetStatus()
		out = append(out, Agent{
			ID:           hello.GetNodeId(),
			Name:         hello.GetName(),
			Hostname:     runtime.GetHostname(),
			OS:           runtime.GetOs(),
			WorkingDir:   runtime.GetWorkingDir(),
			Provider:     status.GetProvider(),
			Capabilities: hello.GetCapabilities(),
			Model:        status.GetModel(),
			Bound:        status.GetBound(),
			ConfigError:  status.GetConfigError(),
			Commands:     commandSpecs(view.GetCommands()),
		})
	}
	return out, nil
}

// commandSpecs copies the host's command catalogue into the shell's type. An
// absent catalogue stays absent (nil, "commands": null on the wire), as it did
// when the reply was decoded by hand.
func commandSpecs(specs []*types.CommandSpec) []CommandSpec {
	if specs == nil {
		return nil
	}
	out := make([]CommandSpec, len(specs))
	for i, spec := range specs {
		out[i] = CommandSpec{Name: spec.GetName(), Description: spec.GetDescription(), Usage: spec.GetUsage()}
	}
	return out
}

// agentsDo sends the ListAgents request, repeating it only when the *dial*
// timed out. Each attempt builds a fresh request, so no body is reused. The
// bearer token rides the request: the HTTP client is the package's dial-failure
// seam rather than the token transport the other management calls build.
func (c *Client) agentsDo(ctx context.Context) (*connect.Response[types.ListAgentsResponse], error) {
	const procedure = "/cyber.rpc.agent.AgentService/ListAgents"
	client := rpc.NewAgentServiceClient(agentsHTTPClient, c.BaseURL, connect.WithProtoJSON())
	deadline := time.Now().Add(connectretry.Window)
	var causes []string
	for attempt := 1; ; attempt++ {
		req := connect.NewRequest(&types.ListAgentsRequest{})
		if c.Token != "" {
			req.Header().Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := client.ListAgents(ctx, req)
		if err == nil {
			if attempt > 1 {
				log.Printf("aop: 管理接口 %s%s 第 %d 次尝试连上（前 %d 次连接超时）", c.BaseURL, procedure, attempt, attempt-1)
			}
			return resp, nil
		}
		causes = append(causes, err.Error())
		if !dialTimedOut(err) || ctx.Err() != nil {
			return nil, fmt.Errorf("aop: 列出 agent 失败：%w", err)
		}
		wait := connectretry.WaitForNext(attempt, deadline)
		if wait <= 0 {
			return nil, fmt.Errorf("aop: 列出 agent 失败（共 %d 次尝试，预算 %s）：%s；最后 %w",
				len(causes), connectretry.Label(), strings.Join(causes, "；"), err)
		}
		log.Printf("aop: 管理接口 %s%s 第 %d 次连接超时（%v），%s 后重试（第 %d/%d 次）",
			c.BaseURL, procedure, attempt, err, wait.Round(time.Millisecond), attempt, len(connectretry.Delays))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("aop: 列出 agent 失败（%v）：%w", ctx.Err(), err)
		case <-timer.C:
		}
	}
}

// dialTimedOut reports a connect that timed out before the request was written.
// The Op=="dial" check is what keeps this to the connect phase: a read timeout
// after the POST went out is not retried. The generated client wraps the
// transport's error in a *connect.Error; errors.As still reaches the net.OpError
// underneath it.
func dialTimedOut(err error) bool {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		return false
	}
	return connectretry.IsTimeout(err)
}

// Find returns the agent with this node id, or the only agent when id is empty.
// "Only" matters: a host with two agents and no pinning would make the working
// directory a guess, and a guessed working directory is a run that writes
// somewhere nobody reads.
func Find(agents []Agent, id string) (Agent, bool) {
	if id != "" {
		for _, a := range agents {
			if a.ID == id {
				return a, true
			}
		}
		// Names are operator-facing selectors; never guess when they collide.
		var found Agent
		count := 0
		for _, a := range agents {
			if a.Name == id {
				found = a
				count++
			}
		}
		return found, count == 1 && found.ID != ""
	}
	if len(agents) == 1 {
		return agents[0], true
	}
	return Agent{}, false
}

// ResolveAgent translates an id/name selector (or a unique unpinned node) to
// the explicit node id required by OpenSession and PTY in upstream rc10.
func (c *Client) ResolveAgent(ctx context.Context, selector string) (Agent, error) {
	agents, err := c.Agents(ctx)
	if err != nil {
		return Agent{}, err
	}
	a, ok := Find(agents, selector)
	if !ok || a.ID == "" {
		return Agent{}, fmt.Errorf("AOP agent %q 不存在或不唯一", selector)
	}
	return a, nil
}

// NodeID preserves a protocol id or resolves a human-readable selector first.
func (c *Client) NodeID(ctx context.Context, selector string) (string, error) {
	if raw, err := hex.DecodeString(selector); err == nil && len(raw) == 16 {
		return selector, nil
	}
	a, err := c.ResolveAgent(ctx, selector)
	if err != nil {
		return "", err
	}
	return a.ID, nil
}
