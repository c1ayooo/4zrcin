// Package node owns the execution locations of one instance.
//
// A node is "a machine + a harness host + the toolchain around it": it is an
// AOP endpoint, including the machine zrcin runs on (zrcin starts a local `aiscan
// web` and registers it as "local"). cyber-audit is the one non-host form: a
// one-shot local binary with no agent to talk to. Everything else — dispatch,
// steer, verify, report, terminal, REPL — goes through the host (D64).
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/aop"
	"github.com/c1ayooo/4zrcin/internal/aopexec"
	"github.com/c1ayooo/4zrcin/internal/executor"
)

// Spec is one node as it is written in <data>/nodes.json or injected by the
// control plane. Every execution node is an AOP host: the control-plane machine
// is not an exception, zrcin starts a local `Hub + scan` and registers it as the
// node "local" (docs/14-nodes.md, D64).
//
//	{
//	  "default": "lab-win",
//	  "nodes": [
//	    {"id": "lab-win", "aop": "http://10.0.0.5:18766", "token": "…",
//	     "agent": "local", "note": "另一台机器的 harness 宿主"},
//	    {"id": "audit", "bin": "../tools/cyber-audit", "audit": true,
//	     "note": "源码审计：本地一次性进程，不是 agent"}
//	  ]
//	}
type Spec struct {
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
	// AOP nodes are reached through a harness host's AOP endpoint. `bin` is a
	// control-plane local process and exists only for audit nodes.
	AOP string `json:"aop,omitempty"`
	// Token is that host's access key.
	Token string `json:"token,omitempty"`
	// Agent pins sessions to one agent node id inside the host. Empty means the
	// host picks, which is only right when it runs exactly one agent.
	Agent string `json:"agent,omitempty"`
	// Audit marks a node that runs cyber-audit instead of an agent (D47). The
	// command line and the product are different: a one-shot audit writes a
	// report directory, which the executor translates into the ledger's handoff.
	// It is the only node form with a local binary (D64).
	Audit bool `json:"audit,omitempty"`
	// Bin, Args and Env belong to the audit form: the binary, its extra flags
	// and the environment it runs with. They are ignored (and rejected) on an
	// execution node, which is always a host somewhere else.
	Bin  string            `json:"bin,omitempty"`
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	// Capabilities declares the node's run mode as the operator deployed it
	// (docs/04 §3.1): ["scan-capture"] marks a capture node launched with
	// --capture-only, whose engine block is not assembled. The routing rule is
	// conservative: implicit dispatch (an intent that does not name a node)
	// never lands here; an explicit request honours the operator. An absent
	// declaration reads as full. The runtime hello reports the same marker —
	// the situational badge shows the announcement, so a drift between
	// declaration and deployment is visible.
	Capabilities []string `json:"capabilities,omitempty"`
	// Note on the AOP form: the agent's working directory is the host's, so the
	// case directory must be visible to it at the same path (same machine or an
	// identical mount). There is no file-read RPC to fall back on.
}

// Registry is the instance-level list of nodes plus the default one.
type Registry struct {
	specs map[string]Spec
	order []string
	def   string
	path  string

	// llmEnv / llmArgs are the instance's harness configuration from the
	// settings page (CYBER_* and --context-window / --max-tokens). The local
	// host zrcin starts inherits them; a remote AOP host keeps its own host-side
	// configuration.
	llmMu   sync.RWMutex
	llmEnv  []string
	llmArgs []string

	// localID identifies the node supervised by this control plane.
	// remoteSource answers the tunnel facts of one agent id (docs/14 §7.3).
	// Nil when the instance has no node ingress; a local or direct node has no
	// answer either, which is exactly the "not remote" signal.
	remoteSource func(agentID string) *executor.RemoteNode

	localID       string
	discoveryMu   sync.Mutex
	discovered    map[string]Spec
	discoveryInfo map[string]executor.NodeInfo
	discoveryAt   time.Time

	// discoveryHub is the Hub discovery lists against (endpoint, token) plus
	// the local agent to skip. It is set once at boot; without it RefreshNodes
	// falls back to the local spec, the form the registry tests use.
	discoveryHub   bool
	hubEndpoint    string
	hubToken       string
	hubSelfAgentID string
}

// SetDiscoveryHub names the Hub whose registered scan agents RefreshNodes
// discovers, and the local agent to leave out of the results. It works with
// --no-local-node too: the Hub runs, only its node is absent, so discovery
// needs an anchor that is not the (missing) local spec.
func (r *Registry) SetDiscoveryHub(endpoint, token, selfAgent string) {
	r.discoveryMu.Lock()
	defer r.discoveryMu.Unlock()
	r.discoveryHub = true
	r.hubEndpoint = strings.TrimSpace(endpoint)
	r.hubToken = strings.TrimSpace(token)
	r.hubSelfAgentID = strings.TrimSpace(selfAgent)
}

// discoveryAnchor resolves the Hub discovery anchor. The fallback keeps the
// historical shape (the local spec doubles as the anchor) for registries the
// tests and tools build without SetDiscoveryHub.
func (r *Registry) discoveryAnchor() (endpoint, token, selfAgent string, ok bool) {
	if r.discoveryHub {
		if r.hubEndpoint == "" {
			return "", "", "", false
		}
		return r.hubEndpoint, r.hubToken, r.hubSelfAgentID, true
	}
	base, found := r.specs[r.localID]
	if !found || base.AOP == "" {
		return "", "", "", false
	}
	return base.AOP, base.Token, base.Agent, true
}

// SetRemoteSource attaches the tunnel profile lookup. It is set once at boot,
// before the health prober and the API read Infos.
func (r *Registry) SetRemoteSource(fn func(agentID string) *executor.RemoteNode) { r.remoteSource = fn }

// RemoteFor returns the tunnel profile of the agent behind a spec, if any.
func (r *Registry) RemoteFor(id string) *executor.RemoteNode {
	spec, ok := r.Spec(id)
	if !ok || spec.Agent == "" || r.remoteSource == nil {
		return nil
	}
	return r.remoteSource(spec.Agent)
}

// SetLocalID records the node started by this control plane.
func (r *Registry) SetLocalID(id string) { r.localID = strings.TrimSpace(id) }

// LocalID is the node id zrcin started ("" when this instance has no local host).
func (r *Registry) LocalID() string { return r.localID }

// SetLLM replaces the harness environment and flags handed to local audit runs.
// It is called at boot and whenever the settings page saves, so the next run
// uses the new configuration without restarting the control plane. (The local
// AOP host is started once, with the settings in effect at boot.)
func (r *Registry) SetLLM(env, args []string) {
	r.llmMu.Lock()
	r.llmEnv = append([]string(nil), env...)
	r.llmArgs = append([]string(nil), args...)
	r.llmMu.Unlock()
}

func (r *Registry) llm() (env, args []string) {
	r.llmMu.RLock()
	defer r.llmMu.RUnlock()
	return append([]string(nil), r.llmEnv...), append([]string(nil), r.llmArgs...)
}

// New validates a set of specs and picks a default. An empty default means the
// first node, which is what a one-node deployment wants.
//
// An empty spec list is the 纯控制面 form (--no-local-node): the Hub runs with
// an empty registry and dispatch fails readably at resolveNode. The launcher
// owns the guard for "not even the local host came up", because only it knows
// whether a local node was supposed to exist.
//
// Every node is a host except audit: there is no local one-shot agent form any
// more (D64). A node that only has `bin` is refused with that sentence instead
// of being run as a subprocess.
func New(specs []Spec, def string) (*Registry, error) {
	r := &Registry{specs: make(map[string]Spec, len(specs))}
	if len(specs) == 0 {
		return r, nil
	}
	for _, s := range specs {
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" {
			return nil, errors.New("节点缺 id")
		}
		if _, dup := r.specs[s.ID]; dup {
			return nil, fmt.Errorf("节点 id 重复：%s", s.ID)
		}
		if s.Audit {
			if strings.TrimSpace(s.Bin) == "" {
				return nil, fmt.Errorf("审计节点 %s 缺 bin", s.ID)
			}
			if strings.TrimSpace(s.AOP) != "" {
				return nil, fmt.Errorf("节点 %s 既是 audit 又是 aop：cyber-audit 是本地进程，不通过宿主跑", s.ID)
			}
		} else {
			if strings.TrimSpace(s.AOP) == "" {
				if strings.TrimSpace(s.Bin) != "" {
					return nil, fmt.Errorf("节点 %s 只写了 bin：spawn 已移除，执行节点必须是 aop 宿主（本机也由控制台自动起 Hub + scan）", s.ID)
				}
				return nil, fmt.Errorf("节点 %s 缺 aop：执行节点必须是 harness 宿主", s.ID)
			}
			if strings.TrimSpace(s.Bin) != "" || len(s.Args) > 0 || len(s.Env) > 0 {
				return nil, fmt.Errorf("节点 %s 同时写了 aop 和 bin/args/env：执行节点由宿主运行，这些字段只属于 audit", s.ID)
			}
		}
		r.specs[s.ID] = s
		r.order = append(r.order, s.ID)
	}
	if def == "" {
		def = r.order[0]
	}
	if _, ok := r.specs[def]; !ok {
		return nil, fmt.Errorf("默认节点 %s 不在列表里", def)
	}
	r.def = def
	return r, nil
}

// File is the on-disk nodes.json: extra hosts on top of the local one zrcin
// starts itself.
type File struct {
	Default string `json:"default"`
	Nodes   []Spec `json:"nodes"`
}

// Read loads nodes.json without validating it: the control plane adds its own
// local host to the list first, so validation belongs to New.
//
// A relative `bin` (audit only) is resolved against the directory of the file,
// not the shell that started zrcin: the file describes its own nodes, so moving
// the file moves its references.
func Read(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("%s 不是合法的节点文件：%w", path, err)
	}
	f.Nodes = absolutize(f.Nodes, filepath.Dir(path))
	return f, nil
}

// Load reads and validates a nodes.json on its own (used by tests and tools
// that have no local host to inject).
func Load(path string) (*Registry, error) {
	f, err := Read(path)
	if err != nil {
		return nil, err
	}
	r, err := New(f.Nodes, f.Default)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", path, err)
	}
	r.path = path
	return r, nil
}

// absolutize anchors relative binary paths so a child process can start them
// from any work dir. base "" means the process's own working directory. The
// result is absolute even when base itself was relative (`--nodes .build/x`).
func absolutize(specs []Spec, base string) []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	for i := range out {
		p := strings.TrimSpace(out[i].Bin)
		if p == "" {
			continue
		}
		target := p
		if !filepath.IsAbs(p) {
			if base != "" {
				target = filepath.Join(base, p)
			}
			if abs, err := filepath.Abs(target); err == nil {
				target = abs
			}
		}
		// A nodes.json written on Windows may omit the .exe suffix.
		out[i].Bin = executor.NormalizePath(target)
	}
	return out
}

// Missing lists audit nodes whose binary is not on disk right now. A node may
// be provisioned after the process starts, so this is a startup warning, not an
// error: it turns "the first run failed mysteriously" into "the log said so".
// A host is checked by the health probe, not by stat.
func (r *Registry) Missing() []string {
	var out []string
	for _, id := range r.order {
		s := r.specs[id]
		if s.AOP != "" {
			continue // a host somewhere else: reachability is a probe, not a file
		}
		if st, err := os.Stat(s.Bin); err != nil || st.IsDir() {
			label := s.Bin
			if strings.TrimSpace(label) == "" {
				label = "没配 bin"
			}
			out = append(out, id+" ("+label+")")
		}
	}
	return out
}

func (r *Registry) Default() string { return r.def }

// Path is where the registry came from; "" when it was synthesised.
func (r *Registry) Path() string { return r.path }

// IDs lists every routable node id: the configured ones plus the currently
// discovered scan agents. The health prober (and anything else that walks
// IDs + Spec) must see both — a discovered capture node that only ever shows
// up in Infos() would keep its offline default and its recipe badge would
// never light up (docs/14 §4).
func (r *Registry) IDs() []string {
	out := append([]string(nil), r.order...)
	r.discoveryMu.Lock()
	for id := range r.discovered {
		out = append(out, id)
	}
	r.discoveryMu.Unlock()
	sort.Strings(out)
	return out
}

func (r *Registry) Spec(id string) (Spec, bool) {
	s, ok := r.specs[id]
	if !ok {
		r.discoveryMu.Lock()
		defer r.discoveryMu.Unlock()
		s, ok = r.discovered[id]
	}
	return s, ok
}

// Resolve preserves explicit placement. Only an empty request uses the default;
// a misspelled or disconnected node must never silently run elsewhere.
func (r *Registry) Resolve(requested string) string {
	if s := strings.TrimSpace(requested); s != "" {
		return s // unknown requests must fail, never silently run elsewhere
	}
	return r.def
}

// TerminalEndpoint reports how the console reaches a shell on the node. Only an
// AOP host has one: audit is a local process with no host to dial (D64). The
// agent id is what the host routes PTY streams to.
func (r *Registry) TerminalEndpoint(id string) (baseURL, token, agent string, ok bool) {
	s, found := r.Spec(strings.TrimSpace(id))
	if !found || s.AOP == "" {
		return "", "", "", false
	}
	agent = strings.TrimSpace(s.Agent)
	return s.AOP, s.Token, agent, true
}

// Infos is the console-facing view: id, note, host endpoint or audit binary,
// and the transport that reaches it.
func (r *Registry) Infos() []executor.NodeInfo {
	out := make([]executor.NodeInfo, 0, len(r.order))
	for _, id := range r.order {
		s := r.specs[id]
		info := executor.NodeInfo{ID: id, Note: s.Note, Default: id == r.def, Capabilities: s.Capabilities}
		if s.AOP != "" {
			info.Transport, info.Endpoint = "aop", s.AOP
		} else {
			// An audit node is the only form with a local binary (D64).
			info.Transport, info.Bin = "audit", s.Bin
		}
		out = append(out, info)
	}
	r.discoveryMu.Lock()
	var dynamic []executor.NodeInfo
	for _, info := range r.discoveryInfo {
		dynamic = append(dynamic, info)
	}
	r.discoveryMu.Unlock()
	sort.Slice(dynamic, func(i, j int) bool { return dynamic[i].ID < dynamic[j].ID })
	out = append(out, dynamic...)
	if r.remoteSource != nil {
		for i := range out {
			if out[i].Agent == "" {
				continue
			}
			out[i].Remote = r.remoteSource(out[i].Agent)
		}
	}
	return out
}

// RefreshNodes discovers scan processes already registered with 4zrcin's Hub.
// It does not start another Hub or invent a remote execution protocol.
func (r *Registry) RefreshNodes(ctx context.Context) error {
	r.discoveryMu.Lock()
	defer r.discoveryMu.Unlock()
	if time.Since(r.discoveryAt) < 3*time.Second {
		return nil
	}
	endpoint, token, selfAgent, ok := r.discoveryAnchor()
	if !ok {
		return nil
	}
	agents, err := aop.New(endpoint, token).Agents(ctx)
	if err != nil {
		return err
	}
	if r.discovered == nil {
		r.discovered = map[string]Spec{}
		r.discoveryInfo = map[string]executor.NodeInfo{}
	}
	for id, info := range r.discoveryInfo {
		online := false
		ready := false
		info.Online = &online
		info.Ready = &ready
		r.discoveryInfo[id] = info
	}
	for _, agent := range agents {
		// Skip the local agent only when there is one to name: hub-only mode
		// has no self agent, so every answer belongs to a remote node.
		if selfAgent != "" && (agent.ID == selfAgent || agent.Name == selfAgent) {
			continue
		}
		if agent.ID == "" {
			continue
		}
		id := "scan_" + agent.ID
		// An explicit configured selector owns its id; do not duplicate it.
		duplicate := false
		for _, spec := range r.specs {
			if spec.AOP == endpoint && (spec.Agent == agent.ID || spec.Agent == agent.Name) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		// Capabilities is the node's own hello announcement (docs/04 §3.1):
		// the runtime recipe report is the routing input for discovered nodes,
		// which have no operator-written declaration to lean on.
		r.discovered[id] = Spec{ID: id, AOP: endpoint, Token: token, Agent: agent.ID, Note: agent.Name, Capabilities: agent.Capabilities}
		online, ready := true, agent.Bound && agent.ConfigError == ""
		r.discoveryInfo[id] = executor.NodeInfo{ID: id, Agent: agent.ID, Name: agent.Name, Online: &online, Ready: &ready, WorkingDir: agent.WorkingDir, Model: agent.Model, ConfigError: agent.ConfigError, Transport: "aop", Endpoint: endpoint, Note: agent.Name, Capabilities: agent.Capabilities}
	}
	r.discoveryAt = time.Now()
	return nil
}

// Executor builds the router for this registry.
func (r *Registry) Executor(timeout time.Duration) executor.Executor {
	return &Router{reg: r, timeout: timeout}
}

// Router dispatches one request to the node it names.
type Router struct {
	reg     *Registry
	timeout time.Duration
}

func (r *Router) Name() string {
	return "nodes(" + strings.Join(r.reg.IDs(), ",") + ")"
}

func (r *Router) Nodes() []executor.NodeInfo { return r.reg.Infos() }

func (r *Router) RefreshNodes(ctx context.Context) error { return r.reg.RefreshNodes(ctx) }

func (r *Router) ResolveNode(requested string) string { return r.reg.Resolve(requested) }

// TerminalEndpoint exposes the host terminal through the router, which is what
// the API's executor actually is (not the Registry).
func (r *Router) TerminalEndpoint(nodeID string) (baseURL, token, agent string, ok bool) {
	return r.reg.TerminalEndpoint(nodeID)
}

// CanResume verifies the saved session belongs to this node's current host.
func (r *Router) CanResume(node, workDir string) bool {
	id := r.reg.Resolve(node)
	session, ok := aopexec.ReadSession(workDir)
	if !ok {
		return false
	}
	endpoint, _, agent, ok := r.reg.TerminalEndpoint(id)
	return ok && strings.TrimRight(session.URL, "/") == strings.TrimRight(endpoint, "/") && (agent == "" || session.Agent == "" || agent == session.Agent)
}

// Run hands the request to the node's own runner. The request carries the
// resolved id, so an event handler or a wrapper can tell where work happened.
func (r *Router) Run(ctx context.Context, req executor.Request, emit func(executor.Event)) (executor.Result, error) {
	id := r.reg.Resolve(req.Node)
	req.Node = id

	spec, exists := r.reg.Spec(id)
	if !exists {
		return executor.Result{ExitCode: -1}, fmt.Errorf("节点 %s 不存在；不会回退到默认节点", id)
	}
	// The dispatch-time admission refuses tunnel nodes until the file contract
	// lands (docs/14 §7.3); this backstop keeps a caller that skipped admission
	// from opening a session that cannot work.
	remote := r.reg.RemoteFor(id)
	if remote != nil && !remote.DispatchReady {
		reason := remote.DispatchBlockedReason
		if reason == "" {
			reason = "隧道节点暂不能承接意图"
		}
		return executor.Result{ExitCode: -1}, fmt.Errorf("节点 %s：%s", id, reason)
	}
	llmEnv, llmArgs := r.reg.llm()
	switch {
	case spec.Audit:
		ex := executor.Audit{
			Bin:     spec.Bin,
			Extra:   append(append([]string{}, llmArgs...), spec.Args...),
			Env:     append(envList(spec.Env), llmEnv...),
			Timeout: r.timeout,
		}
		res, err := ex.Run(ctx, req, emit)
		if err != nil {
			return res, fmt.Errorf("节点 %s（audit %s）：%w", id, spec.Bin, err)
		}
		return res, nil
	case spec.AOP != "":
		// The host owns its model configuration; passing CYBER_* here would be
		// a lie the host cannot honour (docs/14).
		ex := aopexec.Executor{
			BaseURL: spec.AOP, Token: spec.Token, Agent: spec.Agent, Timeout: r.timeout,
			// A tunnel node shares no filesystem: the case inputs ride the turn
			// and the node's own zrcin binary is what its sessions call.
			CaseSeed:    remote != nil,
			NodeCLIPath: remoteCLIPath(remote),
			// The node reaches the console on its own loopback mirror, not
			// on the platform's port (docs/14 §7.2).
			ConsoleMirrorPort: remoteConsolePort(remote),
		}
		res, err := ex.Run(ctx, req, emit)
		if err != nil {
			return res, fmt.Errorf("节点 %s（AOP %s）：%w", id, spec.AOP, err)
		}
		return res, nil
	default:
		// Unreachable for specs built through New; kept so a hand-built Spec
		// fails loudly instead of running somewhere unexpected.
		return executor.Result{ExitCode: -1}, fmt.Errorf("节点 %s 没有 aop 也不是 audit：不能执行", id)
	}
}

// remoteCLIPath reads the reported CLI path without dereferencing nil.
func remoteCLIPath(remote *executor.RemoteNode) string {
	if remote == nil {
		return ""
	}
	return remote.CLIPath
}

// remoteConsolePort reads the node's console mirror port; 0 keeps the
// platform's own address (a node that has not reported yet, or a local one).
func remoteConsolePort(remote *executor.RemoteNode) int {
	if remote == nil {
		return 0
	}
	return remote.ConsoleMirrorPort
}

func envList(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
