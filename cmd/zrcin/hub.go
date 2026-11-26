// The hub is the instance: it owns the open projects, builds one API server and
// one decider per project, and routes /p/<id>/ to the right one.
//
// Routing is a plain handler rather than a registered ServeMux tree so that a
// project created at runtime is reachable immediately, without mutating a live
// mux.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/c1ayooo/4zrcin/internal/aopexec"
	"github.com/c1ayooo/4zrcin/internal/api"
	"github.com/c1ayooo/4zrcin/internal/bundle"
	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/decider"
	"github.com/c1ayooo/4zrcin/internal/deploy"
	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/evidence"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/fleet"
	"github.com/c1ayooo/4zrcin/internal/host"
	"github.com/c1ayooo/4zrcin/internal/ioa"
	"github.com/c1ayooo/4zrcin/internal/kb"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/node"
	"github.com/c1ayooo/4zrcin/internal/noderes"
	"github.com/c1ayooo/4zrcin/internal/notify"
	"github.com/c1ayooo/4zrcin/internal/pocs"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/relay"
	"github.com/c1ayooo/4zrcin/internal/report"
	"github.com/c1ayooo/4zrcin/internal/settings"
	"github.com/c1ayooo/4zrcin/internal/store"
	"github.com/c1ayooo/4zrcin/internal/tunnel"
	"github.com/c1ayooo/4zrcin/internal/verifier"
	"github.com/c1ayooo/4zrcin/internal/vulnlib"
	"github.com/c1ayooo/4zrcin/internal/web"
)

type hubConfig struct {
	DataDir string
	Nodes   *node.Registry
	// APIBase is this console's own URL. The commander session calls the
	// control-plane API with it (D78).
	APIBase string
	// InstanceSecret is the instance's JWT signing key. The local host does
	// not get it: 4zrcin derives the scoped agent token from it and puts that
	// in the host environment instead, so no harness session (commander or
	// executor) can reach the operator-only routes (D109).
	InstanceSecret string
	// Collab issues the per-run participant tokens. It is process-local: a
	// token belongs to a live run, and a restart ends that run.
	Collab *collab.Registry
	// SessionEnv is the path to the shell init the host sessions source
	// (BASH_ENV/ENV). It has no credential: it exports the caller's own token
	// from its case directory at shell start.
	SessionEnv string
	// LocalHost is the `aiscan web` this process started. A settings change
	// restarts it so the local node picks up the new model without a zrcin restart.
	LocalHost *host.Host

	// RemoteNodes and Enroll are the node ingress of docs/14: the records and
	// the public routing shell. Both are nil unless --public-url is set; when
	// nil every /join, /node and /tunnel route answers 404.
	RemoteNodes *enroll.Service
	Enroll      *enroll.Ingress
	// Tunnel is the live tunnel surface (sessions, teardown); nil unless the
	// ingress is on and the binary carries the tunnel build.
	Tunnel tunnel.Ingress
	// Dist reports which node artifacts this platform can hand out.
	Dist *enroll.Dist
	// RelayStatus reports the platform's reverse connection to its public
	// entry (docs/14 §7.5); nil when the platform is its own public host.
	RelayStatus func() relay.State
	// Deploys runs operator-initiated SSH deployments (docs/14 §7.4). Nil
	// unless the node ingress is on: a host that cannot reach the platform's
	// public URL could not come back anyway.
	Deploys *deploy.Service

	// IOA tails the local host's IOA space and stores v1 application records
	// for the console. It is read-only and optional (nil when not started).
	IOA          *ioa.Observer
	Timeout      time.Duration
	MaxEvidence  int
	Scope        string
	Skills       []string
	SkillsDir    string
	ReportSkills []string
	MaxRuns      int
	// MaxWorkers caps live intent runs across all projects (0 = uncapped),
	// the instance-wide counterpart of sapper's MAX_WORKERS (docs/17 §3.5).
	MaxWorkers  int
	IdleTimeout time.Duration
	KB          *kb.Library
	KBDir       string
	Debounce    time.Duration
	Heartbeat   time.Duration
	// RoundTimeout bounds one commander round's wall clock (0 = unbounded): the
	// wake loop serves rounds one at a time, so a round that never ends queues
	// every decision behind it (D91).
	RoundTimeout time.Duration
	NoDecider    bool
	// NoVerify starts every project with reviews closed: the verifier exists (so
	// the operator can open reviews at runtime from the project page), but no
	// review run can start and the prompt shows unverified facts with their text.
	// It is a general operator setting, not an evaluation-only one.
	NoVerify       bool
	DefaultProject string
}

// kit is one project's runtime: its server, its routes, its decider and the
// switch that stops all of them when the project is deleted.
type kit struct {
	proj   *project.Project
	srv    *api.Server
	mux    *http.ServeMux
	dec    *decider.Runner
	cancel context.CancelFunc
}

type hub struct {
	auxiliaryGate *executor.Gate
	cfg           hubConfig
	mgr           *project.Manager
	ctx           context.Context
	settings      *settings.Store
	pocs          *pocs.Store
	vulns         *vulnlib.Library
	// IM 通知：notifyStore 持通道配置（data/notify.json），notifier 是投递引擎。
	// 两者都可能为 nil（配置文件不可写时降级为无通知，控制台相应报错）。
	notifyStore *notify.Store
	notifier    *notify.Engine
	// localResources is the control plane's own host sample, refreshed on a
	// ticker and served with the local node row (docs/14 §7.3).
	localResources atomic.Value
	// vulnQuarantine is the index this start moved aside because it could not be
	// parsed (startup, not a live failure). It is served with the project index
	// so the console can say it; nil means this start quarantined nothing.
	vulnQuarantine *vulnQuarantine
	static         http.Handler
	// probe answers "is this node actually reachable" for the 态势 page; it is
	// cached for a few seconds.
	probe *nodeProber

	mu         sync.RWMutex
	kits       map[string]*kit
	dispatchMu sync.Mutex
	// transferMu serialises export/import with project deletion. Export walks a
	// quiesced directory and deletion removes it; running both at once would let
	// each see a tree the other is changing.
	transferMu sync.Mutex
	// Runtime patches merge omitted fields with the last save. Keep the read,
	// persistence and live concurrency update in the same critical section.
	runtimeSettingsMu sync.Mutex

	// ioaClients are per host: the embedded IOA server is on the same endpoint
	// as AOP, but its space/sender/target ids are resolved lazily and cached.
	ioaMu      sync.Mutex
	ioaClients map[string]*ioa.Client
	ioaSources map[string]*ioa.Observer

	// modelLimits is what the last /api/models probe learned about each model's
	// max_output_tokens. It exists so a saved max_tokens is clamped to what the
	// model actually accepts; an empty map simply means "not detected yet".
	modelLimitsMu sync.Mutex
	modelLimits   map[string]int

	// createFault is a test-only seam: when set, it fails the named required
	// write during project creation ("premise_policy", "description",
	// "options", "goal") so the rollback path can be exercised end to end
	// through the HTTP handler. Production builds never set it (nil).
	createFault func(step string) error
}

func newHub(ctx context.Context, cfg hubConfig, mgr *project.Manager,
	st *settings.Store, lib *pocs.Store, vulns *vulnlib.Library) *hub {
	// The review/report gate is instance-wide and its capacity is a persisted
	// runtime setting. It is created once here and only resized afterwards: the
	// verifier and report generators capture the pointer, so replacing it on a
	// settings save would keep routing new work through the old gate.
	auxCapacity := 1
	if st != nil {
		auxCapacity = st.RuntimeSettings().AuxiliaryConcurrency
	}
	// IM 通知（docs/05）：配置在 data/notify.json，引擎是 hub 生命周期内的
	// 一个 goroutine。打不开配置只降级为无通知，不阻断控制台启动。
	var notifyStore *notify.Store
	var notifier *notify.Engine
	if ns, err := notify.OpenStore(filepath.Join(cfg.DataDir, "notify.json")); err != nil {
		log.Printf("notify: 通知配置不可用：%v（本次启动没有 IM 推送）", err)
	} else {
		notifyStore = ns
		names := func(projectID string) string {
			if p := mgr.Get(projectID); p != nil {
				return p.Name
			}
			return ""
		}
		notifier = notify.NewEngine(ns, names, notifyLink(cfg.APIBase))
		go notifier.Run(ctx, 3)
	}
	h := &hub{
		auxiliaryGate: executor.NewGate(auxCapacity),
		cfg:           cfg,
		mgr:           mgr,
		ctx:           ctx,
		settings:      st,
		pocs:          lib,
		vulns:         vulns,
		static:        web.Static(),
		probe:         newNodeProber(cfg.Nodes),
		kits:          map[string]*kit{},
		ioaClients:    map[string]*ioa.Client{},
		modelLimits:   map[string]int{},
		notifyStore:   notifyStore,
		notifier:      notifier,
	}
	if cfg.LocalHost != nil {
		h.startLocalResourceSampler(ctx)
	}
	if cfg.Deploys != nil {
		cfg.Deploys.SetOnFinish(func(v deploy.JobView) {
			if h.notifier == nil {
				return
			}
			title, severity := "SSH 部署成功："+v.Host+"（节点 "+v.NodeID+"）", ""
			summary := "已回连，等待首次心跳。"
			if v.State == deploy.StateFailed {
				title = "SSH 部署失败：" + v.Host
				severity = "high"
				summary = v.Error
				if summary == "" {
					summary = "任务在 " + v.Steps[len(v.Steps)-1].Label + " 一步失败。"
				}
			}
			h.notifier.Emit(notify.EventNodeDeploy, v.ID, "", title, summary, severity)
		})
	}
	return h
}

// localSample wraps a snapshot for atomic.Value.
type localSample noderes.Snapshot

// startLocalResourceSampler refreshes the control plane's own host sample on
// the heartbeat cadence. The local node's cards read the latest snapshot; the
// local scan's pid comes from the host status at sample time.
func (h *hub) startLocalResourceSampler(ctx context.Context) {
	go func() {
		tick := time.NewTicker(30 * time.Second)
		sample := func() {
			st := h.cfg.LocalHost.Status()
			h.localResources.Store(localSample(noderes.NewSampler(h.cfg.DataDir, st.NodePID).Sample(600 * time.Millisecond)))
		}
		sample()
		for {
			select {
			case <-ctx.Done():
				tick.Stop()
				return
			case <-tick.C:
				sample()
			}
		}
	}()
}

// serveNodeDeploys starts an SSH deployment (docs/14 §7.4). It is an operator
// route: the job is created and run by the platform, and the credentials in
// the request never leave the job.
func (h *hub) serveNodeDeploys(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Deploys == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "SSH 部署未开启：它需要平台有可回连的公网地址（--public-url https://你的域名）",
		})
		return
	}
	if r.Method == http.MethodGet {
		// The page restores what is deploying from here: the running jobs
		// first (newest first), then the ones that already ended.
		writeJSON(w, http.StatusOK, map[string]any{"jobs": h.cfg.Deploys.List(20)})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只接受 GET 与 POST"})
		return
	}
	var req deploy.Options
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON"})
		return
	}
	view, err := h.cfg.Deploys.Start(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// serveNodeDeployItem reports one deployment job's steps and log.
func (h *hub) serveNodeDeployItem(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Deploys == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/node-deploys/")
	if r.Method == http.MethodDelete {
		if err := h.cfg.Deploys.Remove(id); err != nil {
			if errors.Is(err, deploy.ErrJobRunning) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": true})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只接受 GET 与 DELETE"})
		return
	}
	view, ok := h.cfg.Deploys.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个部署任务"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// serveEnrollments is the operator face of the node ingress (docs/14 §7.1):
// create an invitation and list the existing ones. The plaintext token appears
// only in the create response; every list entry carries its digest's record,
// never the secret.
func (h *hub) serveEnrollments(w http.ResponseWriter, r *http.Request) {
	if h.cfg.RemoteNodes == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "节点接入未开启：启动 zrcin 时加 --public-url https://你的域名",
		})
		return
	}
	switch r.Method {
	case http.MethodGet:
		body := map[string]any{"enrollments": h.cfg.RemoteNodes.ListEnrollments()}
		if h.cfg.Dist != nil {
			body["dist"] = h.cfg.Dist.Status()
		}
		writeJSON(w, http.StatusOK, body)
	case http.MethodPost:
		var req struct {
			Name       string `json:"name"`
			Recipe     string `json:"recipe"`
			Deploy     string `json:"deploy"`
			TTLMinutes int    `json:"ttl_minutes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON"})
			return
		}
		created, err := h.cfg.RemoteNodes.Create(enroll.CreateOptions{
			Name: req.Name, Recipe: req.Recipe, Deploy: req.Deploy, TTLMinutes: req.TTLMinutes,
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// The plaintext token appears exactly once, in this response: never let
		// a cache or an intermediary keep it.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, created)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只接受 GET 与 POST"})
	}
}

// serveRemoteNodes lists the joined tunnel nodes with their live session
// facts (docs/14 §7.1). Local and direct nodes stay on the project node list;
// this surface is about the ingress.
func (h *hub) serveRemoteNodes(w http.ResponseWriter, r *http.Request) {
	if h.cfg.RemoteNodes == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "节点接入未开启：启动 zrcin 时加 --public-url https://你的域名",
		})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只接受 GET"})
		return
	}
	rows := make([]map[string]any, 0)
	for _, node := range h.cfg.RemoteNodes.Nodes() {
		row := map[string]any{
			"id": node.ID, "name": node.Name, "state": node.State,
			"recipe": node.Recipe, "deploy": node.Deploy,
			"hostname": node.Hostname, "os": node.OS, "arch": node.Arch,
			"online": node.Online, "dispatch_ready": node.DispatchesOK,
			"last_seen_at": node.LastSeenAt,
			// The node cards (docs/14 §7.3): sample, derived health, and the
			// runtime facts the identity/runtime rows read.
			"resources":           node.Resources,
			"health":              node.Health,
			"launcher_version":    node.LauncherVersion,
			"uptime_seconds":      node.UptimeSeconds,
			"scan_restarts":       node.ScanRestarts,
			"mirror_hub_port":     node.MirrorHubPort,
			"mirror_console_port": node.MirrorConsolePort,
		}
		if node.LastError != "" {
			row["last_error"] = node.LastError
		}
		if h.cfg.Tunnel != nil {
			if info, up := h.cfg.Tunnel.Session(node.ID); up && info.Up {
				row["tunnel"] = map[string]any{
					"up": true, "bytes_in": info.BytesIn, "bytes_out": info.BytesOut,
					"since": info.Since,
				}
			} else {
				row["tunnel"] = map[string]any{"up": false}
			}
		}
		rows = append(rows, row)
	}
	ingress := map[string]any{"enabled": true}
	if h.cfg.RelayStatus != nil {
		ingress["relay"] = h.cfg.RelayStatus()
	}
	body := map[string]any{"nodes": rows, "ingress": ingress}
	if h.cfg.Dist != nil {
		body["dist"] = h.cfg.Dist.Status()
	}
	writeJSON(w, http.StatusOK, body)
}

// serveRemoteNodeItem is the per-node operator surface: POST …/destroy begins
// the teardown (docs/14 §7.3) and POST …/forget finalises a node that will never
// report again.
func (h *hub) serveRemoteNodeItem(w http.ResponseWriter, r *http.Request) {
	if h.cfg.RemoteNodes == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只接受 POST"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/remote-nodes/")
	id, action, ok := strings.Cut(rest, "/")
	if !ok || strings.TrimSpace(id) == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个节点"})
		return
	}
	switch action {
	case "destroy":
		view, err := h.cfg.RemoteNodes.Destroy(id)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, enroll.ErrNoSuchNode) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		// Close the live tunnel and unbind the node everywhere: a destroyed
		// node must not linger as a project's default either.
		if h.cfg.Tunnel != nil {
			_ = h.cfg.Tunnel.Close(id)
		}
		h.dropNodeFromProjects(id)
		writeJSON(w, http.StatusOK, map[string]any{"node": view, "projects_unbound": true})
	case "forget":
		view, err := h.cfg.RemoteNodes.MarkDestroyed(id)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, enroll.ErrNoSuchNode) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"node": view})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

// dropNodeFromProjects removes the node (as itself and as its discovered
// scan_<id> agent) from every project's allowlist and default.
func (h *hub) dropNodeFromProjects(nodeID string) {
	if h.mgr == nil {
		return
	}
	drop := map[string]bool{nodeID: true, "scan_" + nodeID: true}
	for _, p := range h.mgr.All() {
		if !p.BindingConfigured() {
			continue
		}
		nodes, def := p.NodeBinding()
		kept := make([]string, 0, len(nodes))
		changed := false
		for _, id := range nodes {
			if drop[id] {
				changed = true
				continue
			}
			kept = append(kept, id)
		}
		if !changed {
			continue
		}
		nextDef := def
		if drop[def] {
			nextDef = ""
			if len(kept) > 0 {
				nextDef = kept[0]
			}
		}
		_ = p.SetNodeBinding(kept, nextDef)
	}
}

// serveEnrollmentItem revokes one unjoined invitation (DELETE).
func (h *hub) serveEnrollmentItem(w http.ResponseWriter, r *http.Request) {
	if h.cfg.RemoteNodes == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只接受 DELETE"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/enrollments/")
	if strings.TrimSpace(id) == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个接入记录"})
		return
	}
	if err := h.cfg.RemoteNodes.RevokeEnrollment(id); err != nil {
		status := http.StatusConflict
		if errors.Is(err, enroll.ErrNoSuchEnrollment) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// rememberModelLimits merges one /models catalogue into the cache.
func (h *hub) rememberModelLimits(limits map[string]int) {
	if len(limits) == 0 {
		return
	}
	h.modelLimitsMu.Lock()
	if h.modelLimits == nil {
		h.modelLimits = map[string]int{}
	}
	for id, n := range limits {
		h.modelLimits[id] = n
	}
	h.modelLimitsMu.Unlock()
}

// modelOutputLimit is the detected max_output_tokens for a model, or 0 when it
// has not been detected (Anthropic does not publish one).
func (h *hub) modelOutputLimit(model string) int {
	h.modelLimitsMu.Lock()
	defer h.modelLimitsMu.Unlock()
	return h.modelLimits[strings.TrimSpace(model)]
}

// clampMaxTokens applies the model's upper bound to an explicitly configured
// max_tokens. Zero means "not set" and stays zero: the harness/provider default
// then applies, which is already within the model's limit.
func (h *hub) clampMaxTokens(m settings.Model) settings.Model {
	limit := h.modelOutputLimit(m.Model)
	if limit > 0 && m.MaxTokens > limit {
		log.Printf("settings: max_tokens %d 超过模型 %s 的上限 %d，按上限使用", m.MaxTokens, m.Model, limit)
		m.MaxTokens = limit
	}
	return m
}

// start builds every project that exists at boot.
//
// A restart is an operator action, not permission to spend: every saved
// project comes up paused even when its manifest said running, and stays
// paused until the operator explicitly resumes it. The pause is written to the
// manifest so a second restart cannot silently resume an engagement either; it
// is also applied in memory when that write fails, so a read-only disk cannot
// turn a restart into an auto-start.
func (h *hub) start() {
	for _, p := range h.mgr.All() {
		h.attachBoot(p)
	}
}

// attachBoot is the boot path: it persists the default-paused state first, then
// builds the runtime from it.
func (h *hub) attachBoot(p *project.Project) *kit {
	// A cause persisted before the restart still owns the classification: the
	// forced boot pause must not rewrite "commander requested stop" into the
	// manual-pause default. A project that was running at boot gets the restart
	// cause, which is what actually happened — not a manual pause nobody made.
	wasPaused := p.IsPaused()
	if !wasPaused {
		if err := p.SetPausedWithKind(true, project.PauseKindRestart); err != nil {
			log.Printf("project %s: 无法持久化启动暂停状态：%v（本进程仍按暂停启动）", p.ID, err)
		} else {
			log.Printf("project %s: 重启后默认暂停，等你点「继续项目」", p.ID)
		}
	}
	// The boot reason is the persisted cause's own sentence, not a fixed one: the
	// console shows this prose on the first screen ("why is it stopped"), so a
	// commander stop must not come back as a restart and a cause-less pause must
	// not come back as either. Only an empty kind — which this boot never writes
	// — falls back to "no recorded cause".
	kind := p.PauseKindOf()
	k := h.attachState(p, true, pauseKindReason(kind))
	// Re-apply the persisted cause to the runtime classifier: the console's pause
	// badge and append-and-continue both switch on the machine code.
	if kind != "" {
		k.srv.RestorePauseKind(kind)
	}
	return k
}

// Persisted pause causes (internal/project.Manifest.PauseKind): the hub maps the
// stored machine code to the same operator-facing wording the live pause paths
// use; a restart must not change why the project says it is paused.
const (
	pauseKindCompletionStandby = project.PauseKindCompletionStandby
	pauseKindCommanderStandby  = project.PauseKindCommanderStandby
)

func pauseKindReason(kind string) string {
	switch kind {
	case pauseKindCommanderStandby:
		return "指挥官请求停止项目：操作员确认后点「继续项目」"
	case pauseKindCompletionStandby:
		return "目标已完成，项目待命：新增目标或点「继续项目」后恢复派发"
	case project.PauseKindRestart:
		return "控制面重启后项目默认暂停：确认现场后点「继续项目」"
	case "", "unrecorded":
		// No stored cause: a manifest written before the codes existed. Saying
		// "the operator paused it" would invent the one fact we do not have.
		return "暂停原因未记录：确认现场后点「继续项目」"
	default:
		return "已暂停（原因码 " + kind + "）：确认现场后点「继续项目」"
	}
}

// attach builds a project's runtime from the manifest pause state. Runtime
// creation (create project, re-open after edits) keeps that state; only the
// boot path (attachBoot) forces a pause.
func (h *hub) attach(p *project.Project) *kit {
	paused := p.IsPaused()
	kind := ""
	reason := ""
	if paused {
		kind = p.PauseKindOf()
		reason = pauseKindReason(kind)
	}
	k := h.attachState(p, paused, reason)
	if paused {
		// Re-apply the persisted cause to the runtime classifier: the console's
		// pause badge and append-and-continue switch on it, never on the prose.
		k.srv.RestorePauseKind(kind)
	}
	return k
}

// instanceReviewsDisabled is the instance default for the review switch: the
// value saved in the instance settings, or the --no-verify boot flag when no
// value was ever saved. It is read when a project attaches (boot, creation,
// re-open) and reported to the console, so a project with no override and the
// settings page never disagree about which default is in force.
func (h *hub) instanceReviewsDisabled() bool {
	if h.settings != nil {
		return h.settings.RuntimeSettings().ReviewsDefaultDisabled(h.cfg.NoVerify)
	}
	return h.cfg.NoVerify
}

func (h *hub) attachState(p *project.Project, paused bool, pauseReason string) *kit {
	// Each project gets its own context so a delete can stop its decider and
	// watchers without touching its siblings. A hub built without a parent
	// context (tests construct the struct directly) still needs a cancellable
	// child; nil would panic inside context.WithCancel.
	parent := h.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)

	srv := api.New(p.Store, h.cfg.Nodes.Executor(h.cfg.Timeout), p.Dir, p.CaseDir)
	if h.settings != nil {
		// The instance fence rides every dispatch and commander turn (spec #1
		// 工单3): the read is the settings store's lock-protected view, so a
		// save applies from the next round without a restart.
		srv.GlobalFence = func() settings.Fence { return h.settings.FenceOf() }
	}
	wireShells(srv, h.cfg.RemoteNodes, h.cfg.InstanceSecret)
	// The fence membership writes (join/leave, pending resolve) go through the
	// project manager: it owns the manifest, its normalization and the
	// dispatch-side snapshot the next round reads.
	srv.PersistScopeRules = func(rules *project.ScopeRules) (project.ScopeRules, error) {
		return h.mgr.SetScopeRules(p.ID, rules)
	}
	// The asset page reads this project's vulnerability-library entries: the
	// library is instance state that outlives the project, so the read is a
	// callback rather than a store the project server owns.
	if h.vulns != nil {
		srv.VulnEntries = func() []vulnlib.Entry { return h.vulns.List(p.ID, "", 0) }
	}
	if h.auxiliaryGate != nil {
		// Premise reviews are auxiliary runs; let independent directions use
		// the same operator-tuned capacity instead of one serial pump per
		// project. The gate's Capacity is live, so a settings save changes it
		// without rebuilding the pump.
		srv.ReviewConcurrency = h.auxiliaryGate.Capacity
	}
	srv.Ctx = ctx
	srv.Project = p
	// Scope and constraints live in project.json, which outlives the ledger; the
	// document row and its revision history live in the ledger. A save must write
	// both, or a restart would show the old text with a new revision.
	srv.PersistDoc = func(kind, text string) error {
		switch kind {
		case store.DocumentScope:
			return h.mgr.SetScope(p.ID, text)
		case store.DocumentConstraints:
			return h.mgr.SetDescription(p.ID, text)
		}
		return nil
	}
	srv.SetPaused(paused)
	if h.cfg.Nodes != nil {
		srv.LocalNode = h.cfg.Nodes.LocalID()
	}
	// Degraded start (D101): the console is allowed to run while the local
	// host's provider never initialized; dispatch to that host is refused until
	// it is ready (a settings save revives it).
	if h.cfg.LocalHost != nil {
		srv.LocalHostStatus = h.cfg.LocalHost.Status
		srv.LocalResources = func() *noderes.Snapshot {
			if v, ok := h.localResources.Load().(localSample); ok {
				s := noderes.Snapshot(v)
				return &s
			}
			return nil
		}
		// One peak-memory record per settled run: the only way to name a
		// multi-GiB child process (a PoC interpreter) after the fact. Bookkeeping
		// only — nothing is capped or killed (internal/api/proc_peak.go).
		srv.ChildPeaks = h.cfg.LocalHost.ChildPeakRSS
	}
	srv.Collab = h.cfg.Collab
	if h.settings != nil {
		srv.ModelSettings = func() (string, string, int) { m := h.settings.Effective(); return m.Provider, m.Model, m.ContextWindow }
	}

	// Secret redaction backstop: the gateway key, historic keys and
	// the instance signing secret are replaced with *** at the journal/pulse
	// write points. The harness reads provider config from the environment
	// (host env is needed for that), so the secret does reach the host process;
	// the run journal and ledger must not keep a copy of it.
	srv.Secrets = func() []string {
		var out []string
		if secret := strings.TrimSpace(h.cfg.InstanceSecret); secret != "" {
			// The instance secret signs operator sessions; it stays out of run
			// journals. Harness sessions no longer inherit a derived token (each
			// gets its own per-run credential in its case), so there is no second
			// static secret to redact here.
			out = append(out, secret)
		}
		if h.settings != nil {
			out = append(out, h.settings.SecretValues()...)
		}
		return out
	}
	srv.SetContextWindow(h.modelContext())
	srv.Timeout = h.cfg.Timeout
	// The project's persisted switch wins over the instance default: a restart
	// must not re-close a review the operator opened on the project page. With no
	// stored value the instance default applies — the console's saved setting
	// when it has one, else the --no-verify boot flag (see instanceReviewsDisabled).
	reviewsDisabled := h.instanceReviewsDisabled()
	if v, ok := p.ReviewsDisabledOf(); ok {
		reviewsDisabled = v
	}
	srv.SetReviewsDisabled(reviewsDisabled)
	srv.SetReviewBootDisabled(h.cfg.NoVerify)
	if reviewsDisabled {
		// Boot state only: the project page can open reviews again at runtime
		// (POST /p/<id>/api/verifier/enable), which is why the switch is runtime
		// state and not a "no verifier was built" fact.
		srv.SetReviewsPaused(true)
	}
	srv.MaxRuns = h.cfg.MaxRuns
	srv.MaxWorkers = h.cfg.MaxWorkers
	srv.GlobalRunning = h.globalRunning
	srv.InstanceDispatch = &h.dispatchMu
	srv.IdleTimeout = h.cfg.IdleTimeout
	srv.KBDir = h.cfg.KBDir
	srv.KB = h.cfg.KB
	srv.Description = p.DescriptionText()
	srv.Scope = h.scopeOf(p)
	srv.APIBase = h.cfg.APIBase
	// say is a host-side delivery: the harness already has the inbox, IOA is
	// just its HTTP face. An audit-only or IOA-less host leaves this nil and
	// the API returns 503 instead of pretending it can interrupt.
	srv.Say = func(ctx context.Context, in model.Intent, sess aopexec.SessionInfo, text string, interrupt bool) (string, error) {
		return h.say(ctx, p, in, sess, text, interrupt, "", false, nil)
	}
	// SayEvent carries the message's stable event id into the v1 envelope, so
	// a retried delivery is deduped by identity instead of by retry count.
	srv.SayEvent = func(ctx context.Context, in model.Intent, sess aopexec.SessionInfo, text string, interrupt bool, eventID string) (string, error) {
		return h.say(ctx, p, in, sess, text, interrupt, eventID, false, nil)
	}
	// A result message with a chain becomes a task.result record instead of a
	// plain message: the chain is what lets the harness reject cycles and what
	// the observer uses for cross-restart dedupe.
	srv.SayResult = func(ctx context.Context, in model.Intent, sess aopexec.SessionInfo, text, eventID string, chain []string) (string, error) {
		return h.say(ctx, p, in, sess, text, false, eventID, true, chain)
	}
	srv.Skills = h.cfg.Skills
	srv.SkillsDir = h.cfg.SkillsDir
	srv.POCs = h.pocs
	if h.cfg.MaxEvidence > 0 {
		srv.Ingest.MaxEvidence = h.cfg.MaxEvidence
	}

	// The verifier and the report generator both re-run a harness in the
	// project's own workspace, so they are per project: a report must never
	// read another engagement's evidence (D7, D26). They go through the same
	// node router as intents — the run records Purpose, not a fake node id.
	if h.cfg.Nodes != nil {
		admitAuxiliary := func(ctx context.Context) (func(), error) {
			return h.auxiliaryGate.Acquire(ctx)
		}
		vExec := srv.DefaultModelExecutor(h.cfg.Nodes.Executor(h.cfg.Timeout))
		// The verifier is always built, even under --no-verify: --no-verify is only
		// the boot state of the runtime review switch, so the operator can open
		// reviews from the project page without restarting the instance. Nothing
		// can run while the switch is closed (the endpoint, the pump, the fixed
		// settlement phase and the prompt all read it).
		vf := verifier.New(p.Store, vExec, p.CaseDir)
		vf.Policy = p.PremisePolicyView
		vf.ScopeFor = srv.Constraints
		vf.Admit = admitAuxiliary
		vf.Scope = srv.Constraints()
		vf.Timeout = h.cfg.Timeout
		vf.Skills = h.cfg.Skills
		vf.Track = srv.TrackReviewRun
		// The review is a run like any other: it writes the same event journal,
		// so the console can show what it executed and what it cost (D138).
		vf.Log = srv.OpenPurposeRunLog
		// The reviewer's injection boundary gets the same journal anchor as the
		// dispatcher's and the commander's: section name plus byte count, never
		// the text, so an audit can tell which version of the paragraph that
		// review read.
		vf.NoteInjected = func(runID, section string, n int) {
			srv.NoteInjected(runID, "boundary", "", n, section)
		}
		// A review must run where the finding was seen; the default node is a
		// fallback for facts whose origin run is gone (docs/14).
		vf.NodeFor = func(f model.Fact) string { return h.factNode(p, f) }
		// A review runs in a fresh, empty directory, so the proof paths in its
		// brief point at bytes it cannot open. Give it the project's read-only
		// surface instead of copying evidence into the run: the registry mints a
		// credential bound to this project and this run, and the console gate
		// admits only facts/evidence/run-events reads with it (auth.go
		// verifierReadAllowed). Without a registry there is no credential, and the
		// reviewer falls back to the host's own tools — the same shape a project
		// with collaboration disabled already has.
		if h.cfg.Collab != nil {
			vf.IssueRead = func(runID string, factIDs, evidenceIDs []string) (verifier.ReadCredential, error) {
				token, err := h.cfg.Collab.IssueReadOnly(p.ID, runID, factIDs, evidenceIDs)
				if err != nil {
					return verifier.ReadCredential{}, err
				}
				return verifier.ReadCredential{APIBase: h.cfg.APIBase, ProjectID: p.ID, Token: token}, nil
			}
			vf.ReleaseRead = h.cfg.Collab.RevokeRun
		}
		srv.Verifier = vf
		// Reviews are fixed, not switched. Every project is verified_only, so a
		// direction's own conclusions are reviewed in the settlement phase and
		// the automatic pump exists only for facts a downstream direction cites
		// as a necessary premise (reviewPolicy().RequiredOnly). SetAutoVerify(false)
		// is therefore not a configuration read: it records that the optional
		// broad pump — every reviewable fact, not just cited premises — stays off
		// in production. Only tests enable that broad mode. The retired
		// verify_auto manifest key is parsed and ignored (project.Project).
		// Setting the broad pump off re-reads the effective switch and arms the
		// durable candidate set: SetAutoVerify calls SchedulePendingReviews. The
		// switch was applied above from the project's persisted value, so this is
		// the one arm-at-attach point. A second guard here that read
		// h.cfg.NoVerify was dead code — reverting it to the boot flag left every
		// observation unchanged, because this call had already armed the queue from
		// the effective value. The behaviour is pinned by a review run actually
		// starting (hub_review_schedule_test.go), not by a boolean.
		srv.SetAutoVerify(false)
		srv.Reports = &report.Generator{
			ScopeFor: srv.Constraints,
			Policy:   p.PremisePolicyView,
			Admit:    admitAuxiliary,
			Store:    p.Store,
			Exec:     srv.DefaultModelExecutor(h.cfg.Nodes.Executor(h.cfg.Timeout)),
			CaseRoot: p.CaseDir,
			Scope:    srv.Constraints(),
			Timeout:  h.cfg.Timeout,
			Skills:   h.cfg.ReportSkills,
			Track:    srv.TrackPurposeRun,
			// The writing pass is journaled too; it is the most expensive step of
			// delivery, so it deserves the same audit trail (D138).
			Log: srv.OpenPurposeRunLog,
			// Writing the report is a control-plane task; it follows the
			// project default rather than a single fact's origin.
			NodeFor: func() string {
				if h.cfg.Nodes == nil {
					return ""
				}
				if id := h.cfg.Nodes.LocalID(); id != "" {
					return id
				}
				return h.cfg.Nodes.Default()
			},
		}
	}

	// Every vuln fact is mirrored into the instance library the moment it is
	// written (D34). The hook also fires on review, so a verdict reaches the
	// library without a second path.
	p.Store.OnFact(func(f model.Fact, uris []string) {
		h.recordVuln(p, f, uris)
	})
	h.backfill(p)

	mux := http.NewServeMux()
	srv.Register(mux)
	mux.HandleFunc("GET /{$}", web.Index())

	k := &kit{proj: p, srv: srv, mux: mux, cancel: cancel}

	// One decider per project. It reads only its own ledger, so two
	// engagements cannot see each other's facts or intents (D33). Post-handoff
	// session rotation is built in (P0-8, D80): a valid handoff receipt closes
	// the pressure cycle and the next round opens a fresh commander session from
	// the note + board, so a long engagement never keeps paying for a 85-100%
	// context. There is no switch to turn it off.
	if !h.cfg.NoDecider {
		r := decider.NewRunner(p.Store, srv.State, p.DecisionsPath())
		r.Scope = srv.CommanderDocs()
		r.ModelName = h.modelName()
		r.ContextWindow = srv.ModelContext()
		if name, window := srv.CommanderModelInfo(); name != "" {
			r.ModelName = name
			r.ContextWindow = window
		}
		r.Debounce = h.cfg.Debounce
		r.Heartbeat = h.cfg.Heartbeat
		r.RoundTimeout = h.commanderRoundTimeout(p)
		r.ReviewsOff = srv.ReviewsDisabled
		r.DispatchReady = func() error {
			if h.cfg.Nodes == nil || h.cfg.Nodes.LocalID() == "" {
				return errors.New("本机执行宿主未配置，无法派发")
			}
			// A degraded local host (D101) would fail every run in
			// milliseconds; don't let the decider dispatch into it.
			return srv.CheckCommanderHost()
		}
		// The commander round runs inside the harness, in a session that keeps
		// its own context (D78). The round hands it the new board and how full
		// that session is; at 85% it becomes a handoff round (D80). The session
		// acts through the control-plane API.
		r.Commander = func(ctx context.Context, round string, resume bool) (decider.RoundResult, error) {
			res, acts, err := srv.StartCommander(ctx, round, resume)
			return decider.RoundResult{
				RunID:         res.LedgerRunID,
				Tokens:        res.Tokens,
				Actions:       acts,
				ContextTokens: res.FinalContextTokens,
				ContextWindow: res.ContextWindow,
				Compacted:     res.Compacted,
			}, err
		}
		// The runner cannot know whether the AOP session still exists; the API
		// answers with the same executor.CanResume StartCommander uses, so the
		// prompt only injects the whole note when the memory is really gone
		// (D89).
		r.Session = srv.CommanderRoundContext
		// A finished engagement pauses itself. Waiting for the operator to notice
		// an idle commander kept the header button on 「暂停项目」 after every goal
		// was met (and let heartbeats keep waking for nothing); this flips the
		// project to paused with a reason that says it was completion, not the
		// operator. New work still needs an explicit 「继续项目」.
		r.Settled = func() {
			srv.AutoPause("目标已完成：项目自动暂停；新增工作后点「继续项目」重新派发")
		}
		r.OnWake = func(reasons []string, heartbeat bool, promptBytes int) {
			if srv.Metrics != nil {
				srv.Metrics.Wake(reasons, heartbeat, promptBytes)
			}
		}
		srv.Decider = r
		if paused {
			if pauseReason == "" {
				pauseReason = "项目已暂停：等你点「继续项目」"
			}
			r.PauseFor(pauseReason)
		}
		k.dec = r
	}

	h.mu.Lock()
	h.kits[p.ID] = k
	h.mu.Unlock()
	// Recover owns the one crash sweep (message/handoff repair, run
	// reconciliation, pump). Do not run a second reconcile here: attachState is
	// also used for a newly created, imported or rebuilt runtime, and a future
	// caller attaching a live project must not cancel its running rows.
	srv.Recover()
	go srv.Watch(ctx)
	if k.dec != nil {
		k.dec.Start(ctx)
	}

	return k
}

// factNode is where the fact was observed: the node of the run behind its
// evidence, else the intent's last run. Empty means the origin is unknown, and
// the verifier falls back to the default node.
func (h *hub) factNode(p *project.Project, f model.Fact) string {
	for _, id := range f.EvidenceIDs {
		e, ok := p.Store.EvidenceByID(id)
		if !ok || e.RunID == "" {
			continue
		}
		if r, ok := p.Store.GetRun(e.RunID); ok && r.Node != "" {
			return r.Node
		}
	}
	if in, ok := p.Store.GetIntent(f.IntentID); ok && in.RunID != "" {
		if r, ok := p.Store.GetRun(in.RunID); ok {
			return r.Node
		}
	}
	return ""
}

// recordVuln copies the evidence into the library and upserts the entry. It is
// called from the store hook, outside the store lock.
func (h *hub) recordVuln(p *project.Project, f model.Fact, uris []string) {
	if f.Kind != "vuln" {
		return
	}
	kept, dropped := h.vulns.KeepEvidence(p.ID, p.CaseDir, uris)
	// 首次申报才推送：同一事实的复核更新（unverified → verified）会再次
	// Upsert，但那不是「新漏洞」。判断必须在 Upsert 之前，之后已无旧键可查。
	firstSeen := !h.vulns.Has(p.ID, f.ID)
	in, _ := p.Store.GetIntent(f.IntentID)
	// The library outlives the project, so it carries the proof mode and the
	// latest verdict: after a delete there is no ledger left to ask (D105).
	// Reviews are newest first; an entry with no review is left nil rather than
	// given an empty verdict.
	var review *vulnlib.Review
	if rs := p.Store.Reviews(f.ID); len(rs) > 0 {
		r := rs[0]
		review = &vulnlib.Review{
			Verdict:     r.Verdict,
			Notes:       r.Notes,
			VerifierRun: r.VerifierRun,
			CreatedAt:   r.CreatedAt,
		}
	}
	entry, err := h.vulns.Upsert(vulnlib.Entry{
		ProjectID:  p.ID,
		Project:    p.Name,
		SourceID:   f.ID,
		IntentID:   f.IntentID,
		Intent:     in.Title,
		Title:      f.Title,
		Summary:    f.Summary,
		Doc:        f.Doc,
		Confidence: f.Confidence,
		Severity:   f.Severity,
		EntityRef:  f.EntityRef,
		Proof:      f.Proof,
		Status:     f.Status,
		Review:     review,
		Evidence:   kept,
		Dropped:    dropped,
		CreatedAt:  f.CreatedAt,
	})
	if err != nil {
		log.Printf("vulnlib: 记录 %s 失败：%v", f.ID, err)
		return
	}
	// 漏洞入库事件（docs/05）：引擎盲插，通道勾选与级别门槛在 fan-out 过滤。
	if firstSeen && h.notifier != nil {
		title := f.Title
		if title == "" {
			title = "未命名漏洞"
		}
		summary := f.Summary
		if summary == "" && in.Title != "" {
			summary = "来源意图：" + in.Title
		}
		h.notifier.Emit(notify.EventVulnRecorded, entry.ID, p.ID, title, summary, f.Severity)
	}
}

// backfill mirrors vulns that were declared before the library existed (or
// before this process started). Upsert is idempotent, so running it at every
// start is safe.
func (h *hub) backfill(p *project.Project) {
	db := p.Store.Snapshot()
	for _, f := range db.Facts {
		if f.Kind != "vuln" {
			continue
		}
		uris := make([]string, 0, len(f.EvidenceIDs))
		for _, id := range f.EvidenceIDs {
			if e, ok := p.Store.EvidenceByID(id); ok {
				uris = append(uris, e.URI)
			}
		}
		h.recordVuln(p, f, uris)
	}
}

func (h *hub) kit(id string) *kit {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.kits[id]
}

// globalRunning sums live intent runs across all projects for --max-workers.
// It snapshots the kit list under the hub lock and asks each server with its
// own store lock. Admission holds dispatchMu until the new run is durable, so
// another project cannot consume the same capacity between count and claim.
func (h *hub) globalRunning() int {
	h.mu.RLock()
	kits := make([]*kit, 0, len(h.kits))
	for _, k := range h.kits {
		kits = append(kits, k)
	}
	h.mu.RUnlock()
	n := 0
	for _, k := range kits {
		if k != nil && k.srv != nil {
			n += k.srv.RunningIntentCount()
		}
	}
	return n
}

func (h *hub) scopeOf(p *project.Project) string {
	if scope := p.ScopeText(); scope != "" {
		return scope
	}
	return h.cfg.Scope
}

// say delivers one operator/commander message into the live AOP session of an
// intent. It refuses a marker that points at another host or agent: sending the
// note to the wrong machine is worse than failing loudly (D69).
func (h *hub) say(ctx context.Context, p *project.Project, in model.Intent, sess aopexec.SessionInfo, text string, interrupt bool, eventID string, taskResult bool, chain []string) (string, error) {
	if h.cfg.Nodes == nil {
		return "", api.ErrSayUnavailable
	}
	// A result message with a chain is a task.result record; everything else is
	// a v1 message event.
	send := func(spec node.Spec) (string, error) {
		client := h.ioaFor(spec)
		if taskResult {
			return client.SendResult(ctx, sess.Session, text, eventID, chain)
		}
		return client.SendEvent(ctx, sess.Session, text, interrupt, eventID)
	}
	nodeID := strings.TrimSpace(in.Node)
	if nodeID == "" && h.cfg.Nodes != nil {
		nodeID = h.cfg.Nodes.LocalID()
	}
	if nodeID == "" {
		nodeID = h.cfg.Nodes.Default()
	}
	spec, ok := h.cfg.Nodes.Spec(nodeID)
	if !ok {
		return "", fmt.Errorf("意图的节点 %s 不在清单里", nodeID)
	}
	if spec.Audit {
		return "", fmt.Errorf("意图跑在 audit 节点 %s，没有可插话的 AOP 会话", nodeID)
	}
	if spec.AOP == "" {
		return "", api.ErrSayUnavailable
	}
	if sess.URL != "" && !sameEndpoint(spec.AOP, sess.URL) {
		return "", fmt.Errorf("会话属于 %s，不是意图节点 %s", sess.URL, spec.AOP)
	}
	if spec.Agent != "" && sess.Agent != "" && spec.Agent != sess.Agent {
		return "", fmt.Errorf("会话跑在 agent %s 上，不是 %s", sess.Agent, spec.Agent)
	}
	msgID, err := send(spec)
	if err != nil {
		return "", fmt.Errorf("%w：%v", api.ErrSayUnavailable, err)
	}
	return msgID, nil
}

func (h *hub) ioaFor(spec node.Spec) *ioa.Client {
	key := spec.AOP + "\x00" + spec.Token + "\x00" + spec.Agent
	h.ioaMu.Lock()
	defer h.ioaMu.Unlock()
	if h.ioaClients == nil {
		h.ioaClients = map[string]*ioa.Client{}
	}
	if c := h.ioaClients[key]; c != nil {
		return c
	}
	c := ioa.New(spec.AOP, spec.Token, spec.Agent)
	h.ioaClients[key] = c
	return c
}

func sameEndpoint(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(strings.TrimSpace(a), "/"), strings.TrimRight(strings.TrimSpace(b), "/"))
}

// modelName is the console-visible model from the active profile. The client
// itself lives in the harness host; the control plane only needs the name.
func (h *hub) modelName() string {
	if h.settings != nil {
		return h.settings.Effective().Model
	}
	return ""
}

// modelContext is the context budget the console measures live progress
// against before the harness reports the model's resolved window: the active
// harness profile's context_window, or the unknown-model default. Once a run
// reports usage.detail.context_window, the run's own value wins (D80).
func (h *hub) modelContext() int {
	if h.settings != nil {
		if n := h.settings.Effective().ContextWindow; n > 0 {
			return n
		}
	}
	return 128000
}

// defaultID is where "/" sends the browser.
func (h *hub) defaultID() string {
	if h.cfg.DefaultProject != "" && h.mgr.Get(h.cfg.DefaultProject) != nil {
		return h.cfg.DefaultProject
	}
	if p := h.mgr.First(); p != nil {
		return p.ID
	}
	return ""
}

func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		// Liveness only, no instance data: a probe must not leak the ledger or
		// the build. Version sits behind auth on /api/version.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/api/version":
		writeJSON(w, http.StatusOK, map[string]any{"version": buildVersion})
	case r.URL.Path == "/api/settings":
		h.serveSettings(w, r)
	case r.URL.Path == "/api/runtime-settings":
		h.serveRuntimeSettings(w, r)
	case r.URL.Path == "/api/fence":
		h.serveFence(w, r)
	case r.URL.Path == "/api/fence/preset":
		h.serveFencePreset(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/fence/hits"):
		h.serveFenceHits(w, r)
	case r.URL.Path == "/api/models":
		h.serveModels(w, r)
	case r.URL.Path == "/api/settings/check":
		h.serveModelCheck(w, r)
	case r.URL.Path == "/api/fleet":
		h.serveFleet(w, r)
	case r.URL.Path == "/api/notify" || strings.HasPrefix(r.URL.Path, "/api/notify/"):
		h.serveNotify(w, r)
	case r.URL.Path == "/api/ioa/events":
		h.serveIOAEvents(w, r)
	case r.URL.Path == "/api/pocs":
		h.servePOCs(w, r)
	case r.URL.Path == "/api/pocs/import":
		h.importPOCs(w, r)
	case r.URL.Path == "/api/kb":
		h.serveKB(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/kb/"):
		h.serveKBManagement(w, r)
	// The global shell: projects, the vulnerability library, POCs and settings.
	// It is an instance view, so it lives outside /p/<id>/.
	case r.URL.Path == "/g" || strings.HasPrefix(r.URL.Path, "/g/"):
		web.Index()(w, r)
		return
	case strings.HasPrefix(r.URL.Path, "/agent/"):
		h.serveAgent(w, r)
	case strings.HasPrefix(r.URL.Path, "/p/"):
		h.serveProject(w, r)
	case r.URL.Path == "/api/projects":
		h.serveProjects(w, r)
	case r.URL.Path == "/api/projects/import":
		// Import is not a project id. It must be matched before the item route,
		// which otherwise reads "import" as the id and answers 405.
		h.importProject(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/projects/"):
		h.serveProjectItem(w, r)
	// Node ingress (docs/14 §7.1): token-authenticated by its own handlers,
	// outside the operator login gate. A disabled ingress is indistinguishable
	// from a route that does not exist.
	case strings.HasPrefix(r.URL.Path, "/join/"),
		strings.HasPrefix(r.URL.Path, "/node/"),
		strings.HasPrefix(r.URL.Path, "/tunnel/"):
		if h.cfg.Enroll == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		h.cfg.Enroll.ServeHTTP(w, r)
	case r.URL.Path == "/api/remote-nodes":
		h.serveRemoteNodes(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/remote-nodes/"):
		h.serveRemoteNodeItem(w, r)
	case r.URL.Path == "/api/node-deploys":
		h.serveNodeDeploys(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/node-deploys/"):
		h.serveNodeDeployItem(w, r)
	case r.URL.Path == "/api/enrollments":
		h.serveEnrollments(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/enrollments/"):
		h.serveEnrollmentItem(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/exports/"):
		h.serveExport(w, r)
	case r.URL.Path == "/api/vulns":
		h.serveVulns(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/vulns/evidence/"):
		h.serveVulnEvidence(w, r)
	case strings.HasPrefix(r.URL.Path, "/static/"):
		h.static.ServeHTTP(w, r)
	case r.URL.Path == "/":
		// The landing page is the instance view; a project is entered from it.
		http.Redirect(w, r, "/g/", http.StatusFound)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "接口在项目下：/p/<项目>/api/...；跨项目接口是 /api/projects 和 /api/vulns",
		})
	}
}

// serveSettings is the instance-level model configuration. It edits the active
// profile of the harness's own file (~/.cyber/cyber.yaml), so the value the
// console shows is the value the harness, the REPL and every local run use.
func (h *hub) serveSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.settings.View())
	case http.MethodPut:
		var in settings.View
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON"})
			return
		}
		// Merge over the effective config, not just the file: a key that came
		// from the environment must survive a save that leaves the field alone.
		next := settings.ApplyView(h.settings.Effective(), in)
		next = h.clampMaxTokens(next)
		if err := next.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// Product sections (MCP/search) are validated before anything is
		// persisted, so a bad alias cannot leave the model half of a combined
		// save applied.
		productIn := settings.ProductInput{SearchKey: in.SearchTavilyKey, ClearSearchKey: in.ClearSearchKey}
		if in.McpServers != nil {
			productIn.MCPServers = &in.McpServers
		}
		if err := h.settings.ValidateProduct(productIn); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		productChanged, err := h.settings.ProductChanged(productIn)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// Validate the merged host document before persisting anything: a legacy
		// top-level product section would make the next host start fail with the
		// harness's own "unknown configuration field".
		if err := h.settings.ValidateRuntimeConfig(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "宿主运行配置不可用：" + err.Error()})
			return
		}
		// The scan capture retention budget is a startup setting too. Validate it
		// (and detect a real change) before anything is written, so a rejected
		// value cannot leave the model half of a combined save applied.
		var trafficChanged bool
		if in.TrafficBodyRetentionBytes != nil {
			if err := h.settings.ValidateTrafficBodyRetention(*in.TrafficBodyRetentionBytes); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			current := h.settings.TrafficRetention()
			desiredExplicit := *in.TrafficBodyRetentionBytes != 0
			trafficChanged = desiredExplicit != current.Explicit || (desiredExplicit && *in.TrafficBodyRetentionBytes != current.Bytes)
		}
		// The capture section (extensions.traffic) follows the same rule: closed
		// validation and change detection up front, write after the model half.
		var captureChanged bool
		if in.TrafficCaptureWrite != nil {
			if err := h.settings.ValidateTrafficCapture(*in.TrafficCaptureWrite); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			var err error
			captureChanged, err = h.settings.TrafficCaptureDiffers(*in.TrafficCaptureWrite)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
		// The node egress proxy (cyberhub.proxy) follows the same rule again:
		// validate and detect a change before anything persists, so a rejected
		// proxy URL cannot ride behind a 200 that saved the model half.
		var proxyChanged bool
		if in.NodeProxyWrite != nil {
			if err := h.settings.ValidateNodeProxy(*in.NodeProxyWrite); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			var err error
			proxyChanged, err = h.settings.NodeProxyDiffers(*in.NodeProxyWrite)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
		before := h.settings.Effective()
		// Save fills the runtime block from the stored file under writeMu, so this
		// model-only save cannot clobber a concurrent PUT /api/runtime-settings
		// with a snapshot taken before it (the value is a separate instance knob).
		if err := h.settings.Save(settings.File{Model: next}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if in.McpServers != nil || strings.TrimSpace(in.SearchTavilyKey) != "" || in.ClearSearchKey {
			if err := h.settings.SaveProduct(productIn); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "模型已保存，但产品配置保存失败：" + err.Error()})
				return
			}
		}
		if in.TrafficBodyRetentionBytes != nil {
			if err := h.settings.SetTrafficBodyRetention(*in.TrafficBodyRetentionBytes); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "模型已保存，但 scan 节点捕获保留上限保存失败：" + err.Error()})
				return
			}
		}
		if in.TrafficCaptureWrite != nil {
			if err := h.settings.SetTrafficCapture(*in.TrafficCaptureWrite); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "模型已保存，但流量捕获配置保存失败：" + err.Error()})
				return
			}
		}
		if in.NodeProxyWrite != nil {
			if err := h.settings.SetNodeProxy(*in.NodeProxyWrite); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "模型已保存，但执行出网代理保存失败：" + err.Error()})
				return
			}
		}
		// Apply only a real change. The runtime yaml is regenerated by
		// applyModelPath before any push, so the host's hot update and the file a
		// later restart reads carry the same model connection and the same scan
		// capture retention budget.
		applied, requiresRestart, restartReason := "saved", false, ""
		after := h.settings.Effective()
		// traffic.body_retention_bytes is read by scan at process start: like the
		// product sections it can never be hot-applied. A model change in the same
		// save rides the restart instead of applying online first.
		restartParts := []string{}
		if productChanged {
			restartParts = append(restartParts, "MCP/search 等产品配置只在 scan 启动时读取")
		}
		if trafficChanged {
			restartParts = append(restartParts, "scan 节点捕获保留上限只在 scan 启动时读取")
		}
		if captureChanged {
			restartParts = append(restartParts, "流量捕获配置（capture_mode/HTTPS 解密/CA）只在 scan 启动时读取")
		}
		if proxyChanged {
			restartParts = append(restartParts, "执行出网代理（cyberhub.proxy）只在 scan 启动时读取")
		}
		restartReasonText := ""
		if len(restartParts) > 0 {
			restartReasonText = strings.Join(restartParts, "；") + "，需要重启本机宿主后生效"
		}
		if after != before || productChanged || trafficChanged || captureChanged || proxyChanged {
			applied, requiresRestart, restartReason, err = h.applyModelPath(next, true, restartReasonText)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":    "设置已保存，准备宿主运行配置失败：" + err.Error(),
					"code":     "settings_saved_apply_failed",
					"settings": h.settings.View(),
				})
				return
			}
		}
		view := h.settings.View()
		view.Applied, view.RequiresRestart, view.RestartReason = applied, requiresRestart, restartReason
		writeJSON(w, http.StatusOK, view)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET / PUT"})
	}
}

// runtimeSettingsView is the console's read of the instance runtime knobs.
// MaxAuxiliaryConcurrency is read-only metadata for the console's numeric input.
type runtimeSettingsView struct {
	AuxiliaryConcurrency    int `json:"auxiliary_concurrency"`
	MaxAuxiliaryConcurrency int `json:"max_auxiliary_concurrency"`
	// ReviewsDisabled is the instance default for the review switch: what a
	// project with no override runs with. ReviewsDisabledStored says whether that
	// value came from a saved setting or is still the --no-verify boot flag, so
	// the console can label the switch truthfully instead of implying a save that
	// never happened.
	ReviewsDisabled       bool `json:"reviews_disabled"`
	ReviewsDisabledStored bool `json:"reviews_disabled_stored"`
}

// serveRuntimeSettings edits the instance-level knobs that live outside the
// harness profile (the review/report concurrency and the review default). It sits
// on the operator surface, not under /p/<id>/: the gate is process-wide, so the
// value is not a per-project setting. A PUT persists first and then resizes the
// live gate; in-flight reviews keep their acquired slot, so the new bound applies
// to new acquisitions without restarting the instance.
//
// The review default is read when a project attaches, not on every dispatch: a
// PUT here changes what the next boot, creation or re-open inherits, and leaves
// running projects on the switch they attached with. Changing a live project's
// review state is the project page's own control — doing it here as a side effect
// would cancel review runs on projects the operator never touched.
// serveFence is the instance fence's read/write surface (spec #1 工单3): GET
// returns the stored red lines as the operator sees them, PUT normalizes and
// persists — the node picks the new set up on the next round, remote nodes
// included, because the fence rides every turn instead of a restart-time
// file.
// serveFencePreset loads the download / delete / upload command presets into
// the instance fence (POST /api/fence/preset): rows the operator does not
// carry yet are appended through the same normalization a hand save runs, and
// a row whose id or pattern is already present is skipped — loading twice is
// a no-op, and an operator's edited row is never overwritten. Every preset
// row is review: the operator answers each live invocation.
func (h *hub) serveFencePreset(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "实例围栏不可用：设置存储未初始化"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	merged, added, err := settings.ApplyPresetCommands(h.settings.FenceOf())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if added > 0 {
		if err := h.settings.SetFence(merged); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "预设载入保存失败：" + err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fence": merged, "added": added})
}

func (h *hub) serveFence(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "实例围栏不可用：设置存储未初始化"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"fence": h.settings.FenceOf()})
	case http.MethodPut:
		// Tri-state lists mirror the runtime settings' field contract: absent
		// keeps the stored rows (a switch-only save must not wipe them), an
		// explicit array replaces them, null clears them. Unknown fields are
		// rejected the same way, so a misspelled key cannot silently save an
		// empty fence.
		var in struct {
			Commands       *json.RawMessage `json:"commands"`
			Domains        *json.RawMessage `json:"domains"`
			EnabledCommand *bool            `json:"enabled_commands"`
			EnabledDomain  *bool            `json:"enabled_domains"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法的实例围栏：" + err.Error()})
			return
		}
		if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体只能包含一个 JSON 对象"})
			return
		}
		stored := h.settings.FenceOf()
		fence := settings.Fence{Commands: stored.Commands, Domains: stored.Domains}
		if in.EnabledCommand != nil {
			fence.EnabledCommands = *in.EnabledCommand
		} else {
			fence.EnabledCommands = stored.EnabledCommands
		}
		if in.EnabledDomain != nil {
			fence.EnabledDomains = *in.EnabledDomain
		} else {
			fence.EnabledDomains = stored.EnabledDomains
		}
		if in.Commands != nil {
			if string(*in.Commands) == "null" {
				fence.Commands = nil
			} else if err := json.Unmarshal(*in.Commands, &fence.Commands); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "commands 不是合法的规则数组：" + err.Error()})
				return
			}
		}
		if in.Domains != nil {
			if string(*in.Domains) == "null" {
				fence.Domains = nil
			} else if err := json.Unmarshal(*in.Domains, &fence.Domains); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "domains 不是合法的规则数组：" + err.Error()})
				return
			}
		}
		saved, err := settings.NormalizeFence(fence)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := h.settings.SetFence(saved); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"fence": saved})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET 与 PUT"})
	}
}

func (h *hub) serveRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil || h.auxiliaryGate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "运行时设置不可用：设置存储或并发闸门未初始化"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.runtimeSettingsView())
	case http.MethodPut:
		var in struct {
			// RawMessage keeps three cases apart per field: absent (keep the
			// stored value), null (a 400 — neither setting has a third state to
			// clear into), and a value to validate.
			AuxiliaryConcurrency json.RawMessage `json:"auxiliary_concurrency"`
			ReviewsDisabled      json.RawMessage `json:"reviews_disabled"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法的运行时设置：" + err.Error()})
			return
		}
		if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体只能包含一个 JSON 对象"})
			return
		}
		h.runtimeSettingsMu.Lock()
		defer h.runtimeSettingsMu.Unlock()
		next := h.settings.RuntimeSettings()
		changed := false
		if len(in.AuxiliaryConcurrency) > 0 {
			if strings.TrimSpace(string(in.AuxiliaryConcurrency)) == "null" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "auxiliary_concurrency 不能是 null：要给一个 1 到最大值的整数"})
				return
			}
			var n int
			if err := json.Unmarshal(in.AuxiliaryConcurrency, &n); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "auxiliary_concurrency 必须是整数"})
				return
			}
			next.AuxiliaryConcurrency = n
			changed = true
		}
		if len(in.ReviewsDisabled) > 0 {
			if strings.TrimSpace(string(in.ReviewsDisabled)) == "null" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "reviews_disabled 不能是 null：要给 true 或 false"})
				return
			}
			var b bool
			if err := json.Unmarshal(in.ReviewsDisabled, &b); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "reviews_disabled 必须是 true / false"})
				return
			}
			next.ReviewsDisabled = &b
			changed = true
		}
		if !changed {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少要修改的字段：auxiliary_concurrency / reviews_disabled"})
			return
		}
		if err := next.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := h.settings.SetRuntime(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// The gate pointer is stable (verifier/report closures capture it), so
		// this resize is what makes the save live.
		h.auxiliaryGate.SetCapacity(next.AuxiliaryConcurrency)
		writeJSON(w, http.StatusOK, h.runtimeSettingsView())
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET / PUT"})
	}
}

// runtimeSettingsView is the GET/PUT response: the effective values plus the
// maximum a PUT may set.
func (h *hub) runtimeSettingsView() runtimeSettingsView {
	rt := h.settings.RuntimeSettings()
	_, stored := rt.ReviewsDisabledOf()
	return runtimeSettingsView{
		AuxiliaryConcurrency:    rt.AuxiliaryConcurrency,
		MaxAuxiliaryConcurrency: settings.MaxAuxiliaryConcurrency,
		ReviewsDisabled:         rt.ReviewsDefaultDisabled(h.cfg.NoVerify),
		ReviewsDisabledStored:   stored,
	}
}

// serveIOAEvents returns the observer's append-only v1 record log. It is an
// instance view: IOA messages are addressed to sessions/tasks, not to a 4zrcin
// project. Runtime lifecycle records are observations, never project facts.
func (h *hub) serveIOAEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	records, err := h.nativeIOARecords()
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errNoIOAObserver) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	const limit = 500
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	cursor := ""
	if h.cfg.IOA != nil {
		cursor = h.cfg.IOA.Cursor()
	}
	// The instance view is exactly where a record from another project shows
	// up, so label every project's sessions; otherwise the console can only
	// print an opaque session id next to the message.
	sessions := make(map[string]string)
	h.mu.RLock()
	kits := make([]*kit, 0, len(h.kits))
	for _, k := range h.kits {
		kits = append(kits, k)
	}
	h.mu.RUnlock()
	for _, k := range kits {
		for id, label := range sessionLabels(k) {
			sessions[id] = label
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cursor":   cursor,
		"records":  records,
		"sessions": sessions,
	})
}

// serveFleet is the 态势 page's single snapshot across every project. It is
// built from the kits the hub already owns, so the page never fans out one
// board request per project (and never shows N different "now"s).
func (h *hub) serveFleet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	h.mu.RLock()
	kits := make([]*kit, 0, len(h.kits))
	for _, k := range h.kits {
		kits = append(kits, k)
	}
	h.mu.RUnlock()

	sources := make([]fleet.Source, 0, len(kits))
	for _, k := range kits {
		st := decider.Status{Enabled: false}
		if k.dec != nil {
			st = k.dec.Status()
		}
		sources = append(sources, fleet.Source{
			ID: k.proj.ID, Name: k.proj.Name, Scope: h.scopeOf(k.proj),
			Store: k.srv.Store, Decider: st,
		})
	}
	writeJSON(w, http.StatusOK, fleet.Build(time.Now(), sources, h.cfg.Nodes.Infos(), h.probe.Health(r.Context())))
}

// applyModel rebuilds the decider's client, pushes the harness env/flags to the
// node registry, and restarts the local host so it reads the new model
// connection. A remote AOP host keeps its own host-side configuration. It is the
// restart path, used by callers that cannot use the online update (boot, tests).
//
// A restart is a window in which the local host has no provider yet. Dispatching
// a commander round into that window used to burn a failed round ("harness 报告
// 这一轮失败 / agent provider is nil"): the wake is deferred until the host is
// ready again, which AddOnReady reports (D111). A save that changes nothing does
// not restart and wakes immediately as before.
func (h *hub) applyModel(m settings.Model) error {
	_, _, _, err := h.applyModelPath(m, false, "")
	return err
}

// applyModelPath is the settings save's single apply decision.
//
// onlineFirst asks the running host to commit the shared llm document itself
// (ConfigService/UpdateConfig) and waits for its agents to report the new
// provider/model; the host then keeps every live session, which a restart would
// kill. A non-empty restartReason is a setting read only at scan process start
// (MCP/search product sections, the traffic capture retention budget): it
// disables the hot path because a restart is the only way to apply it. A failed
// hot apply falls back to the restart — never to "applied" without evidence.
//
// It returns how the save reached the host ("online"|"restart"|"saved"),
// whether a restart is still needed, and the reason the console should show (the
// hot failure, or why a restart is required).
func (h *hub) applyModelPath(m settings.Model, onlineFirst bool, restartReason string) (string, bool, string, error) {
	m = h.clampMaxTokens(m)
	args := m.Args()
	if h.settings != nil && (h.cfg.Nodes != nil || h.cfg.LocalHost != nil) {
		var err error
		args, err = runtimeHarnessArgs(h.settings, h.cfg.DataDir, m)
		if err != nil {
			return "", false, "", err
		}
	}
	restartRequired := strings.TrimSpace(restartReason) != ""
	if onlineFirst && !restartRequired {
		if applied, reason := h.applyModelOnline(m, args); applied {
			h.applyModelState(m)
			h.wakeAll("settings")
			return "online", false, "", nil
		} else {
			restartReason = reason
			restartRequired = true
			log.Printf("settings: 在线应用未生效（%s），回退重启本机宿主", reason)
		}
	}
	if h.cfg.Nodes != nil {
		h.cfg.Nodes.SetLLM(m.Env(), args)
	}
	restarting := false
	if h.cfg.LocalHost != nil {
		// The host is a child process: it read the old connection at start. The
		// scoped agent token (never the operator's login token) rides along so
		// harness sessions keep their battlefield API access after a settings
		// save restarts the host (D109).
		env := hostEnv(m.Env(), h.cfg.SessionEnv)
		restarting = h.cfg.LocalHost.Update(env, args)
	}
	h.applyModelState(m)
	if h.cfg.LocalHost == nil {
		// An audit instance (or a console without a managed local host) has
		// nothing to restart here; the saved values apply at the next launch.
		if restartRequired {
			return "saved", false, "配置已保存；本机没有运行中的宿主，下次启动时生效", nil
		}
		return "saved", false, "", nil
	}
	if restarting {
		log.Printf("settings: 本机宿主按新配置重启中；就绪后再唤醒各项目（避免在重启窗口开轮）")
		return "restart", restartRequired, restartReason, nil
	}
	h.wakeAll("settings")
	if restartRequired {
		// A startup-only setting always changes the generated runtime file's
		// content-addressed path, so reaching here means the diff did not reach
		// the file; report the restart rather than pretending it applied.
		return "saved", true, restartReason, nil
	}
	return "saved", false, "", nil
}

// applyModelState refreshes the live projections that do not need a restart:
// every project's context budget and the commander's model label.
func (h *hub) applyModelState(m settings.Model) {
	cw := m.ContextWindow
	if cw <= 0 {
		cw = 128000
	}
	h.mu.RLock()
	for _, k := range h.kits {
		k.srv.SetContextWindow(cw)
		if k.dec != nil {
			name, window := k.srv.CommanderModelInfo()
			if name == "" {
				name, window = m.Model, cw
			}
			k.dec.SetModelName(name)
			k.dec.SetContextWindow(window)
		}
	}
	h.mu.RUnlock()
	if !m.Configured() {
		log.Printf("settings: 模型未配置，指挥官关闭")
	} else {
		log.Printf("settings: model=%s endpoint=%s context=%d max_tokens=%d",
			m.Model, m.BaseURL, m.ContextWindow, m.MaxTokens)
	}
}

// applyModelOnline commits the model connection to the running local host and
// confirms its agent picked it up. On success the next launch's env/args are
// staged (not applied): the live child keeps its sessions, a crash restart still
// comes up on the saved connection.
func (h *hub) applyModelOnline(m settings.Model, args []string) (bool, string) {
	local := h.cfg.LocalHost
	if local == nil {
		return false, "本机没有受管宿主"
	}
	status := local.Status()
	if !status.Online {
		return false, "本机宿主不在线"
	}
	if !m.Configured() {
		return false, "模型未配置，无法在线应用"
	}
	if _, _, _, ok := h.settingsOnlineLink(); !ok {
		return false, "本机宿主没有可拨号的管理端点"
	}
	ctx, cancel := context.WithTimeout(context.Background(), host.HotApplyTimeout+5*time.Second)
	defer cancel()
	if err := host.PushRuntimeConfig(ctx, local.Endpoint(), local.Token(), m.ConnectConfig(), local.Agent(), m.Provider, m.Model, host.HotApplyTimeout); err != nil {
		return false, err.Error()
	}
	local.StageConfig(hostEnv(m.Env(), h.cfg.SessionEnv), args)
	if h.cfg.Nodes != nil {
		h.cfg.Nodes.SetLLM(m.Env(), args)
	}
	return true, ""
}

func (h *hub) wakeAll(reason string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, k := range h.kits {
		if k.dec != nil {
			k.dec.Wake(reason)
		}
	}
}

// servePOCs and importPOCs are instance-level: a template library is tooling,
// not an engagement, so the global shell talks to /api/pocs directly.
func (h *hub) servePOCs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	if h.pocs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "POC 库未配置"})
		return
	}
	// The library is thousands of templates. Search and paging happen here so
	// opening the page does not ship/render the whole library: total is the
	// whole library, matched is what q keeps (ignoring offset/limit). No
	// parameters keeps the old whole-library response for callers that want it.
	q := r.URL.Query().Get("q")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit > 500 {
		limit = 500
	}
	all := h.pocs.List()
	page, matched := pocs.Page(all, q, offset, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"dir":     h.pocs.Dir,
		"pocs":    page,
		"total":   len(all),
		"matched": matched,
	})
}

func (h *hub) importPOCs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	if h.pocs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "POC 库未配置"})
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON"})
		return
	}
	p := strings.TrimSpace(req.Path)
	if p == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要 path：模板文件或目录（相对 <data>）"})
		return
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(h.cfg.DataDir, filepath.FromSlash(p))
	}
	res, err := h.pocs.Import(p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// serveKB is the instance-level offline library: world knowledge shared by
// every project and read by both roles. The console reads it whole (a few
// hundred KB at most) and filters client-side, so a bare request returns every
// entry; q/tags/limit switch to the scored search, which is what the commander
// needs — pulling the whole library per lookup is tens of KB of context for one
// fact. The executor does not call this endpoint: it reads the case copy.
func (h *hub) serveKB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	if h.cfg.KB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "知识库未配置"})
		return
	}
	query := r.URL.Query()
	q := strings.TrimSpace(query.Get("q"))
	var tags []string
	for _, raw := range query["tags"] {
		for _, part := range strings.Split(raw, ",") {
			if tag := strings.TrimSpace(part); tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	limit, _ := strconv.Atoi(query.Get("limit"))

	entries := h.cfg.KB.Entries()
	if q != "" || len(tags) > 0 || query.Has("limit") {
		entries = h.cfg.KB.Search(q, tags, limit)
	}
	if entries == nil {
		entries = []kb.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dir":               h.cfg.KBDir,
		"entries":           kbEntriesResponse(entries),
		"revisions":         kb.Revisions(entries),
		"deleted_revisions": h.cfg.KB.DeletedRevisions(),
	})
}

// serveAgent mounts the per-project participant API under
// /agent/<project>/api/. Authentication is the run's collab token, resolved to
// a project+participant+run; the console token is deliberately not accepted
// here, and the project in the URL must match the token.
//
// Scope: this answers how the participant face is mounted and authenticated. It
// owns neither the route list (internal/api/agent.go registerAgent) nor the
// project-face visibility rules (agentRouteHidden in cmd/zrcin/auth.go).
func (h *hub) serveAgent(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/agent/")
	projectID, tail, ok := strings.Cut(rest, "/")
	if !ok || strings.TrimSpace(projectID) == "" || !strings.HasPrefix(tail, "api/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "协作接口路径是 /agent/<项目>/api/..."})
		return
	}
	if h.cfg.Collab == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "协作身份未启用"})
		return
	}
	token := ""
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		token = strings.TrimSpace(strings.TrimPrefix(hdr, "Bearer "))
	}
	ident, ok := h.cfg.Collab.Resolve(token)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "协作令牌无效或已过期"})
		return
	}
	if ident.ProjectID != projectID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "协作令牌不属于这个项目"})
		return
	}
	k := h.kit(projectID)
	if k == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + projectID})
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + tail
	r2.URL.RawPath = ""
	r2 = r2.WithContext(api.WithIdentity(r2.Context(), ident))
	k.srv.AgentHandler().ServeHTTP(w, r2)
}

func (h *hub) serveProject(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/p/")
	id, tail, _ := strings.Cut(rest, "/")
	k := h.kit(id)
	if k == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + id})
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + tail
	r2.URL.RawPath = ""
	if r2.URL.Path == "/api/ioa/events" {
		h.serveProjectIOA(w, r2, k)
		return
	}
	k.mux.ServeHTTP(w, r2)
}

type projectInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Scope       string    `json:"scope,omitempty"`
	Description string    `json:"description,omitempty"`
	Created     time.Time `json:"created"`
	Intents     int       `json:"intents"`
	Facts       int       `json:"facts"`
	Evidence    int       `json:"evidence"`
	Running     int       `json:"running"`
	// MaxRuns is this project's own override; null means "inherit the instance
	// default". MaxRunsEffective is what dispatch enforces tonight (0 = 不限).
	MaxRuns          *int `json:"max_runs"`
	MaxRunsEffective int  `json:"max_runs_effective"`
	// RoundTimeout is this project's own commander round wall clock (a duration
	// string like "15m"); empty means "inherit the instance --round-timeout"
	// and "0" means unlimited (D91).
	RoundTimeout string `json:"round_timeout,omitempty"`
	// There is no verify_auto field here on purpose: the retired per-project
	// review *policy* is fixed by the premise policy (verified_only), and a legacy
	// manifest value is parsed and ignored rather than echoed back as if it did
	// something. The review *switch* below is a different thing and is reported.
	//
	// PremisePolicy is the effective admission mode. It has exactly one value
	// (verified_only); a legacy manifest that says standard or strict is migrated
	// on load, so there is no instance value to inherit and nothing to choose.
	PremisePolicy string `json:"premise_policy"`
	// ReviewsDisabled is the effective switch: the project's own value when the
	// operator set one, else the instance default.
	ReviewsDisabled bool `json:"reviews_disabled"`
	// ReviewsDisabledOverride is the project's own stored value, null when the
	// project follows the instance default. Without it the console cannot tell
	// "follows the instance (currently closed)" from "overridden closed" — both
	// report ReviewsDisabled=true.
	ReviewsDisabledOverride *bool `json:"reviews_disabled_override"`
	// ReviewsCanceled is how many live review runs the request that produced this
	// view canceled (0 elsewhere, omitted). A settings write that closes the
	// switch reports it instead of letting the console claim nothing was running.
	ReviewsCanceled int `json:"reviews_canceled,omitempty"`
	// ScopeRules is the project's machine-matchable test range (the traffic/
	// command audit pipeline's config), absent when never configured. It is
	// reported so the console can show and edit the stored draft; it never
	// rides into a brief.
	ScopeRules *project.ScopeRules `json:"scope_rules,omitempty"`
	// Proxy is the project's own upstream proxy (the project settings card's
	// config), absent when never configured. Reported so the console can show
	// and edit the stored draft; it never rides into a brief.
	Proxy *project.EgressProxy `json:"proxy,omitempty"`
}

func (h *hub) info(p *project.Project) projectInfo {
	db := p.Store.Snapshot()
	out := projectInfo{
		ID:               p.ID,
		Name:             p.Name,
		Scope:            p.ScopeText(),
		Description:      p.DescriptionText(),
		Created:          p.Created,
		Intents:          len(db.Intents),
		Facts:            len(db.Facts),
		Evidence:         len(db.Evidence),
		MaxRunsEffective: h.cfg.MaxRuns,
	}
	out.ReviewsDisabled = h.instanceReviewsDisabled()
	if v, ok := p.ReviewsDisabledOf(); ok {
		out.ReviewsDisabled = v
		out.ReviewsDisabledOverride = &v
	}
	// A running project reports the switch it actually runs with. The instance
	// default is read at attach, so after the default changes the two can differ
	// until the next attach; saying the config-level value here would make the
	// console claim a switch that has not moved.
	if k := h.kit(p.ID); k != nil && k.srv != nil {
		out.ReviewsDisabled = k.srv.ReviewsDisabled()
	}
	out.RoundTimeout = p.RoundTimeoutRaw()
	out.PremisePolicy = p.PremisePolicyView()
	if v, ok := p.MaxRunsOverride(); ok {
		out.MaxRuns = &v
		out.MaxRunsEffective = v
	}
	if v, ok := p.ScopeRulesOf(); ok {
		stored := v
		out.ScopeRules = &stored
	}
	if v, ok := p.ProxyOf(); ok {
		stored := v
		out.Proxy = &stored
	}
	for _, r := range db.Runs {
		if r.Status == "running" {
			out.Running++
		}
	}
	return out
}

// commanderRoundTimeout is the cap this project's decider runs rounds under:
// the manifest override when it has a valid one, else the instance
// --round-timeout. A hand-edited invalid value is reported and ignored here
// rather than silently becoming "inherit" (D91).
func (h *hub) commanderRoundTimeout(p *project.Project) time.Duration {
	if p == nil {
		return h.cfg.RoundTimeout
	}
	d, ok, err := project.ParseRoundTimeout(p.RoundTimeoutRaw())
	if err != nil {
		log.Printf("project %s: round_timeout 非法（%v）：用实例默认 %s", p.ID, err, h.cfg.RoundTimeout)
		return h.cfg.RoundTimeout
	}
	if ok {
		return d
	}
	return h.cfg.RoundTimeout
}

func (h *hub) serveProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		all := h.mgr.All()
		out := make([]projectInfo, 0, len(all))
		for _, p := range all {
			out = append(out, h.info(p))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": out, "max_runs": h.cfg.MaxRuns,
			// Directories that did not load ride along so the console can say
			// which project is missing and why. The list is empty on a healthy
			// instance; the instance keeps serving the projects that did load.
			"skipped_projects": h.mgr.Skipped(),
			// A vulnerability-library index this start could not parse was moved
			// aside and the library started empty (cmd/zrcin/vuln_library.go). It
			// rides along for the same reason skipped_projects does: a
			// degradation the operator has to know about cannot live only in
			// the process log. null on a healthy start, so the console can tell
			// "nothing quarantined now" from "this build does not report it".
			"vuln_library_quarantine": h.vulnQuarantine,
			// The instance default for --round-timeout rides along so the
			// per-project form can show "inherit …" instead of guessing.
			"round_timeout_seconds": int(h.cfg.RoundTimeout / time.Second),
			// The same for the review switch: a project with no override reviews
			// exactly like this. The new-project form shows it next to 复核 and
			// the project page uses it for "跟随实例（当前：开/关）".
			"reviews_disabled_default": h.instanceReviewsDisabled(),
		})
	case http.MethodPost:
		var req struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
			// Description is the operator's prose: boundaries, forbidden
			// actions, platform rules, how to treat target output. The program
			// carries it into every brief and never parses it (D59).
			Description string `json:"description"`
			// A project is created together with the operator-defined endpoint
			// goal: Scope is the 起点 (brief), Goal is the 终点 (statement).
			// An engagement without a statement has nothing to judge "done"
			// against.
			Goal string `json:"goal"`
			// QuantifiedTarget is the legacy quantified criterion from older
			// consoles; the current console sends only 起点 + 终点. Kept so
			// programmatic callers keep working.
			QuantifiedTarget string `json:"quantified_target"`
			// The three dispatch/verification options a new project can carry.
			// They exist so an operator sets them once, at creation, instead of
			// creating, pausing, opening settings and resuming. Omitted fields
			// write nothing: the project keeps the instance defaults exactly as
			// a project created before these fields existed.
			ReviewsDisabled *bool           `json:"reviews_disabled"`
			MaxRuns         json.RawMessage `json:"max_runs"`
			RoundTimeout    *string         `json:"round_timeout"`
			// ScopeRules optionally carries the machine-matchable traffic
			// fence, same shape as PUT /api/projects/{id}. Absent or null
			// stores nothing and the project stays unfenced (record-only).
			ScopeRules json.RawMessage `json:"scope_rules"`
			// Fence is the shorthand for the fence shape applied on top of
			// (or instead of) the rule table: "unfenced" stores nothing,
			// "shadow" judges and forwards, "enforced" gives verdicts teeth.
			Fence string `json:"fence"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON"})
			return
		}
		// Validate every option before the project exists. A rejected value must
		// not leave a half-created engagement behind: there is nothing to roll
		// back and nothing the operator has to clean up.
		var limit *int
		if len(req.MaxRuns) > 0 {
			v, err := parseMaxRuns(req.MaxRuns)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			limit = v
		}
		if req.RoundTimeout != nil {
			if _, _, err := project.ParseRoundTimeout(*req.RoundTimeout); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
		// The fence shorthand and rule table resolve to a concrete rule set
		// before the project exists: an invalid value is a 400, not a rollback.
		createRules, err := buildCreateScopeRules(req.ScopeRules, req.Fence)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// The simplified console asks for one paragraph and no name: derive a
		// readable project name from it instead of rejecting the request. An
		// explicit name still wins, so programmatic callers are unchanged.
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = projectNameFromBrief(req.Scope)
		}
		p, err := h.mgr.Create(name, req.Scope)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// From here on the project exists on disk and in the manager. Every
		// required write (premise policy, description, options, first goal)
		// happens before attach, and any failure rolls the whole thing back:
		// the operator must never get a 201 for an engagement without its
		// goal, nor a project_id that leads to a 404 because the runtime was
		// never attached.
		committed := false
		defer func() {
			if committed {
				return
			}
			// Roll back: stop the ledger, delete the directory, forget the
			// project. Cleanup failures are logged, not swallowed: a
			// half-removed project must be visible in the instance log.
			if err := p.Store.Close(); err != nil {
				log.Printf("project %s: 回滚关闭账本失败：%v", p.ID, err)
			}
			if err := os.RemoveAll(p.Dir); err != nil {
				log.Printf("project %s: 回滚删除目录失败：%v", p.ID, err)
			}
			h.mgr.Remove(p.ID)
		}()
		rollback := func(err error) {
			log.Printf("project %s: 创建失败已回滚：%v", p.ID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "项目创建失败（" + err.Error() + "）：没有保留任何项目，请重试",
				"code":  "project_creation_rolled_back",
			})
		}
		// fault is the test-only injection point (h.createFault): it reports a
		// failure for the named step before the real write runs, so tests can
		// exercise the rollback path through the handler itself.
		fault := func(step string) error {
			if h.createFault == nil {
				return nil
			}
			return h.createFault(step)
		}
		// New engagements are always proof-first: the standard mode is legacy
		// read-only for projects created before this rule and can only migrate
		// to verified_only.
		if err := fault("premise_policy"); err != nil {
			rollback(err)
			return
		}
		if _, err := p.SetPremisePolicy(project.PremisePolicyVerifiedOnly); err != nil {
			rollback(err)
			return
		}
		if desc := strings.TrimSpace(req.Description); desc != "" {
			if err := fault("description"); err != nil {
				rollback(err)
				return
			}
			if err := h.mgr.SetDescription(p.ID, desc); err != nil {
				rollback(err)
				return
			}
		}
		// Options are persisted before the runtime exists: attach reads the
		// review switch into the server and the round wall clock into the
		// decider, and the first commander round must already run under them.
		// Input was validated above, so a failure here is a disk write.
		if err := fault("options"); err != nil {
			rollback(fmt.Errorf("创建选项写入：%w", err))
			return
		}
		if err := h.applyCreateOptions(p, req.ReviewsDisabled, limit, req.RoundTimeout); err != nil {
			rollback(fmt.Errorf("创建选项写入：%w", err))
			return
		}
		// The traffic fence is persisted here too: a project created with a
		// fence must dispatch its first round under it, exactly like the
		// review and timeout options above. The table was validated before
		// creation, so a failure here is a disk write.
		if err := fault("scope_rules"); err != nil {
			rollback(err)
			return
		}
		if createRules != nil {
			if _, err := h.mgr.SetScopeRules(p.ID, createRules); err != nil {
				rollback(fmt.Errorf("访问围栏写入：%w", err))
				return
			}
			// The fence rows are the first asset records: an engagement the
			// operator fenced this deliberately should open the asset page
			// populated, not empty (spec #1 工单2).
			if err := fault("assets"); err != nil {
				rollback(err)
				return
			}
			if err := seedFenceAssets(p, *createRules); err != nil {
				rollback(fmt.Errorf("围栏资产写入：%w", err))
				return
			}
		}
		// The endpoint goal is written before attach too: a creation that
		// reports success must never be one whose first round has nothing to
		// judge "done" against. Programmatic callers that omit the goal keep
		// the legacy shape — for them the goal arrives later, by design.
		if goal := strings.TrimSpace(req.Goal); goal != "" {
			if err := fault("goal"); err != nil {
				rollback(err)
				return
			}
			if _, err := p.Store.AddGoal(model.Goal{
				Statement:        goal,
				QuantifiedTarget: strings.TrimSpace(req.QuantifiedTarget),
				// The endpoint written at project creation is the operator's: the
				// console's history must not show the first goal as unattributed.
				UpdatedBy: collab.OperatorID,
			}); err != nil {
				rollback(err)
				return
			}
		}
		h.attach(p)
		committed = true
		log.Printf("project: 新建 %s（%s）", p.ID, p.Name)
		writeJSON(w, http.StatusCreated, h.info(p))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET / POST"})
	}
}

// applyCreateOptions persists the options a project is created with, through the
// same setters the settings endpoints use (a manifest rewrite that restores the
// field when the write fails). It runs before attach, so nothing has to be
// restarted or re-armed for the first round to see them. An omitted option
// writes nothing: "inherit the instance default" is the manifest's own zero
// value, not a second stored copy of the instance flag.
func (h *hub) applyCreateOptions(p *project.Project, reviewsDisabled *bool, maxRuns *int, roundTimeout *string) error {
	if reviewsDisabled != nil {
		if err := p.SetReviewsDisabled(*reviewsDisabled); err != nil {
			return fmt.Errorf("reviews_disabled: %w", err)
		}
	}
	if maxRuns != nil {
		if err := h.mgr.SetMaxRuns(p.ID, maxRuns); err != nil {
			return fmt.Errorf("max_runs: %w", err)
		}
	}
	if roundTimeout != nil {
		if err := h.mgr.SetRoundTimeout(p.ID, *roundTimeout); err != nil {
			return fmt.Errorf("round_timeout: %w", err)
		}
	}
	return nil
}

// serveProjectPolicy is the in-project 策略 panel's single aggregate: the
// derived fence mode, the stored rule table, the host capture projection and
// the audit guarantee. The guaranteed builtin command audit is enforced on the
// execution side (harness builtin high-risk set, reason prefix "builtin."),
// so from here it is a constant fact to render, not a configurable field.
// Guardrail pending calls keep their own live endpoint — the panel reads it
// directly instead of this aggregate duplicating session state.
func (h *hub) serveProjectPolicy(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	p := h.mgr.Get(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + id})
		return
	}
	view := map[string]any{
		"project":    map[string]any{"id": p.ID, "name": p.Name},
		"fence_mode": p.FenceMode(),
		"audit":      map[string]any{"guaranteed": true},
	}
	if rules, ok := p.ScopeRulesOf(); ok {
		view["scope_rules"] = rules
	}
	if proxy, ok := p.ProxyOf(); ok {
		view["proxy"] = proxy
	}
	if h.settings != nil {
		view["capture"] = h.settings.TrafficCapture()
	}
	writeJSON(w, http.StatusOK, view)
}

// serveProjectAudit is the policy panel's audit trail: every guardrail
// decision recorded in this project's run journals, newest first, as one
// flat list the operator can filter. The journals are streamed — a long
// engagement's log can reach hundreds of megabytes, so the reader keeps only
// the rows it returns, never the whole file.
//
// Filters: intent (exact run.IntentID), tool (exact), action (record/review/
// block), source — "builtin" selects rows whose reason carries the builtin."
// prefix (the execution-side guaranteed command audit, patch 23), "rule"
// selects project-scope-rule verdicts (rule=<id> prefix, no builtin prefix),
// and anything else (including absent) selects all decisions.
func (h *hub) serveProjectAudit(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	p := h.mgr.Get(id)
	k := h.kit(id)
	if p == nil || k == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + id})
		return
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	intent, tool, action, source := q.Get("intent"), q.Get("tool"), q.Get("action"), q.Get("source")

	rows := make([]auditRow, 0, 64)
	for _, run := range k.srv.Store.Runs() {
		if intent != "" && run.IntentID != intent {
			continue
		}
		rows = appendAuditRows(rows, k.srv.DataDir, run.ID, run.IntentID, source, tool, action)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At.Equal(rows[j].At) {
			return rows[i].RunID+"|"+rows[i].CallID > rows[j].RunID+"|"+rows[j].CallID
		}
		return rows[i].At.After(rows[j].At)
	})
	total := len(rows)
	if offset >= total {
		writeJSON(w, http.StatusOK, map[string]any{"rows": []auditRow{}, "total": total})
		return
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows[offset:end], "total": total})
}

// auditRow is one guardrail decision as the audit trail renders it. Builtin is
// the zrcin-side recognition of the execution-side guarantee: the reason text
// carries the "builtin." prefix (harness patch 23) and no project setting can
// turn the row off, so the panel badges it as 保底.
type auditRow struct {
	RunID       string    `json:"run_id"`
	IntentID    string    `json:"intent_id"`
	At          time.Time `json:"at"`
	Tool        string    `json:"tool,omitempty"`
	Action      string    `json:"action,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	RuleID      string    `json:"rule_id,omitempty"`
	CallID      string    `json:"call_id,omitempty"`
	OperationID string    `json:"operation_id,omitempty"`
	Builtin     bool      `json:"builtin"`
}

// appendAuditRows streams one run's journal and keeps the guardrail decisions
// that match the filters. A malformed line or a journal that does not exist is
// skipped — a settled run with no journal is normal (verifier/report runs and
// pre-upgrade runs never wrote one), and one damaged line must not blank the
// whole trail.
func appendAuditRows(rows []auditRow, dataDir, runID, intentID, source, tool, action string) []auditRow {
	f, err := os.Open(filepath.Join(dataDir, "runs", runID+".jsonl"))
	if err != nil {
		return rows
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(line, &probe) != nil || probe.Kind != executor.KindGuardrailDecision {
			continue
		}
		var e executor.Event
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if action != "" && e.GuardrailAction != action {
			continue
		}
		if tool != "" && e.GuardrailTool != tool {
			continue
		}
		builtin := strings.HasPrefix(e.GuardrailReason, "builtin.")
		switch source {
		case "builtin":
			if !builtin {
				continue
			}
		case "rule":
			if builtin || e.GuardrailRuleID == "" {
				continue
			}
		}
		rows = append(rows, auditRow{
			RunID:       runID,
			IntentID:    intentID,
			At:          e.At,
			Tool:        e.GuardrailTool,
			Action:      e.GuardrailAction,
			Reason:      e.GuardrailReason,
			RuleID:      e.GuardrailRuleID,
			CallID:      e.GuardrailCallID,
			OperationID: e.GuardrailOperationID,
			Builtin:     builtin,
		})
	}
	return rows
}

// buildCreateScopeRules resolves the creation-time fence shorthand and rule
// table into the rule set to store, or nil for "store nothing" (the default is
// unfenced). It is pure: the POST handler calls it before the project exists,
// so an invalid value is a 400 rather than a mid-creation rollback.
//
// Precedence: "unfenced" means no rule table at all (an explicit scope_rules
// alongside it is a caller error); "shadow"/"enforced" enable the submitted
// table — or a fresh record-default table when none was given — with the
// matching verdict mode; a scope_rules object without a fence is stored
// exactly as submitted. Validation is unconditional, so a value that is only
// valid once enabled is rejected here like everywhere else.
func buildCreateScopeRules(raw json.RawMessage, fence string) (*project.ScopeRules, error) {
	fence = strings.ToLower(strings.TrimSpace(fence))
	switch fence {
	case "", project.FenceUnfenced, project.FenceShadow, project.FenceEnforced:
	default:
		return nil, fmt.Errorf("fence 只能是 unfenced/shadow/enforced（收到 %q）", fence)
	}
	hasRules := len(raw) > 0 && string(raw) != "null"
	if fence == project.FenceUnfenced && hasRules {
		return nil, errors.New("fence=unfenced 与 scope_rules 不能同时给出：不设围栏就没有规则表")
	}
	if !hasRules && fence != project.FenceShadow && fence != project.FenceEnforced {
		return nil, nil
	}
	var rules project.ScopeRules
	if hasRules {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return nil, fmt.Errorf("scope_rules 不是合法的规则表：%w", err)
		}
	} else {
		// The fallback is fixed (spec #1 工单5): out-of-range traffic is
		// refused and the asset enters the pending-approval flow. The
		// review value carries that semantics — the fork refuses a
		// default-missed request and flags the sidecar instead of holding it.
		rules = project.ScopeRules{DefaultAction: project.ScopeActionReview}
	}
	switch fence {
	case project.FenceShadow:
		rules.Enabled, rules.Shadow = true, true
	case project.FenceEnforced:
		rules.Enabled, rules.Shadow = true, false
	}
	normalized, err := project.NormalizeScopeRules(rules)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

// seedFenceAssets writes the creation-time fence rows into the asset table
// (spec #1 工单2): the operator's most deliberate range statement should open
// the asset page populated, not empty. Hostname rows arrive already
// canonicalized to the bare domain (internal/project), a CIDR row is a range;
// a "*" row fences everything and names no asset. Every seeded row is a
// target the operator put inside the fence on purpose.
func seedFenceAssets(p *project.Project, rules project.ScopeRules) error {
	for _, rule := range rules.Rules {
		if rule.Host == "*" {
			continue
		}
		kind := model.AssetKindDomain
		if strings.Contains(rule.Host, "/") {
			kind = model.AssetKindRange
		}
		if _, _, err := p.Store.UpsertAsset(model.Asset{
			Kind: kind, Key: rule.Host, Target: true,
			Fence: model.AssetFenceIn, Source: model.AssetSourceFence,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *hub) serveProjectItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目"})
		return
	}
	if action != "" {
		if action == "export" {
			h.exportProject(w, r, id)
			return
		}
		if action == "policy" {
			h.serveProjectPolicy(w, r, id)
			return
		}
		if action == "audit" {
			h.serveProjectAudit(w, r, id)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目操作：" + action})
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.updateProject(w, r, id)
	case http.MethodDelete:
		h.deleteProject(w, r, id)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 PUT / DELETE"})
	}
}

// exportProject writes one whole-project archive under <data>/exports. A
// project transfer must describe one committed state, so it refuses to run
// while the engagement can still write: pause first, let the current round
// settle, then export. The archive is downloadable through /api/exports/{name},
// not returned in the JSON response (it can be hundreds of MB).
func (h *hub) exportProject(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	p := h.mgr.Get(id)
	k := h.kit(id)
	if p == nil || k == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + id})
		return
	}

	h.transferMu.Lock()
	defer h.transferMu.Unlock()
	// Re-read after taking the lock: a concurrent delete may have removed the
	// kit while this request waited.
	p = h.mgr.Get(id)
	k = h.kit(id)
	if p == nil || k == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + id})
		return
	}
	if !p.IsPaused() {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "导出前先暂停项目：导出需要一个不再写入的账本",
			"code":  "project_not_paused",
		})
		return
	}
	if !k.srv.WaitIdle(2 * time.Second) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "还有运行在收尾，等它结束后再导出",
			"code":  "project_busy",
		})
		return
	}
	// Block dispatch/reviews for the short walk itself. The project is already
	// paused, so this cannot abort work; it only closes the window between the
	// idle check and the file listing.
	k.srv.SetDeleting(true)
	defer k.srv.SetDeleting(false)

	dir := filepath.Join(h.cfg.DataDir, "exports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "准备导出目录失败：" + err.Error()})
		return
	}
	base := fmt.Sprintf("%s-%s", id, time.Now().Format("20060102-150405"))
	name := base + ".twproj"
	for i := 2; ; i++ {
		_, statErr := os.Stat(filepath.Join(dir, name))
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "检查导出文件名失败：" + statErr.Error()})
			return
		}
		if i > 1000 {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "导出文件名冲突过多"})
			return
		}
		name = fmt.Sprintf("%s-%d.twproj", base, i)
	}
	out := filepath.Join(dir, name)
	m, err := bundle.ExportProject(p.Dir, p.Store, out, buildVersion)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "导出失败：" + err.Error()})
		return
	}
	info, _ := os.Stat(out)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	log.Printf("project: 导出 %s → %s（%d 个文件，%d 字节，%d 条证据缺失）",
		id, name, m.Counts.Files, size, len(m.Missing))
	writeJSON(w, http.StatusOK, map[string]any{
		"name":     name,
		"url":      "/api/exports/" + name,
		"size":     size,
		"manifest": m,
	})
}

// serveExport streams an archive written by exportProject. Names are flat:
// accepting a path here would let the download route read arbitrary files under
// the data directory.
func (h *hub) serveExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/exports/")
	if name == "" || strings.ContainsAny(name, `/\:`) || name == "." || name == ".." {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个导出包"})
		return
	}
	p := filepath.Join(h.cfg.DataDir, "exports", name)
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个导出包"})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, p)
}

// importReadDeadline is the read budget for one project archive upload. It is
// longer than the instance ReadTimeout because the body limit (up to 4 GiB) is
// not reachable at the default 5-minute body deadline on a slow link.
const importReadDeadline = 30 * time.Minute

// pathWithin reports whether target is base itself or below it. Both paths must
// already be absolute/clean.
func pathWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// importProject accepts either a server-side path (JSON {"path": "..."}) or the
// archive itself as the request body. The upload form is the one the console
// uses; the path form exists for scripted migrations and for a package already
// copied into the data directory.
func (h *hub) importProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxProjectImportBody)
	// The instance's ReadTimeout is 5 minutes (sized for ordinary requests). A
	// legitimate 4 GiB archive on a slow link would hit it, so this one route
	// extends the read deadline; the body cap and the route's operator-only gate
	// still bound it.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetReadDeadline(time.Now().Add(importReadDeadline))
	}
	src := ""
	cleanup := func() {}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "application/json") {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON：" + err.Error()})
			return
		}
		src = strings.TrimSpace(req.Path)
		if src == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要 path：项目包文件名或路径"})
			return
		}
		dataRoot, absErr := filepath.Abs(h.cfg.DataDir)
		if absErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "数据目录不可用：" + absErr.Error()})
			return
		}
		if !filepath.IsAbs(src) {
			src = filepath.Join(dataRoot, filepath.FromSlash(src))
		}
		src = filepath.Clean(src)
		if !pathWithin(dataRoot, src) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "项目包路径必须位于数据目录内"})
			return
		}
	} else {
		f, err := bundle.CreateProjectUpload(h.cfg.DataDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存上传包失败：" + err.Error()})
			return
		}
		src = f.Name()
		cleanup = func() { _ = os.Remove(src) }
		if _, err := io.Copy(f, r.Body); err != nil {
			_ = f.Close()
			cleanup()
			code := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				code = http.StatusRequestEntityTooLarge
			}
			writeJSON(w, code, map[string]any{"error": "上传失败：" + err.Error()})
			return
		}
		if err := f.Close(); err != nil {
			cleanup()
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存上传包失败：" + err.Error()})
			return
		}
	}
	defer cleanup()

	h.transferMu.Lock()
	defer h.transferMu.Unlock()
	m, err := bundle.ImportProject(src, h.cfg.DataDir)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "已经存在") {
			code = http.StatusConflict
		}
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	p, err := h.mgr.Adopt(m.Project.ID)
	if err != nil {
		// The files are already published. Do not delete them silently: the
		// operator needs the path to inspect a half-opened project, and the next
		// boot will either load it or report the same error with the engine's
		// own message.
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "项目包已解压，但打开项目失败：" + err.Error(),
			"path":  filepath.Join(h.cfg.DataDir, "projects", m.Project.ID),
		})
		return
	}
	h.attach(p)
	log.Printf("project: 导入 %s（%s）", p.ID, p.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"project":  h.info(p),
		"manifest": m,
	})
}

// updateProject edits the manifest fields the console owns. Every field is
// optional: an omitted one keeps its value, so the concurrency form can save
// without resending the scope (and vice versa). max_runs additionally accepts
// null, which restores "inherit the instance --max-runs". Model selectors
// are rejected; the external gateway owns model routing. premise_policy is not
// editable here: there is one admission mode and the manifest is normalized to
// it on load, so a write could only restate the current value.
func (h *hub) updateProject(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Scope        *string         `json:"scope"`
		Description  *string         `json:"description"`
		MaxRuns      json.RawMessage `json:"max_runs"`
		RoundTimeout *string         `json:"round_timeout"`
		// ReviewsDisabled is the project's own review switch, three states told
		// apart by the raw JSON: absent leaves it alone, null clears the override
		// so the project follows the instance default again, true/false stores a
		// value. A stored value is what the project page shows as "已覆盖"; the
		// instance default stays visible in the runtime settings.
		ReviewsDisabled json.RawMessage `json:"reviews_disabled"`
		// ScopeRules is the machine-matchable test range, same three raw states:
		// absent leaves it alone, null clears the stored set (record-only, no
		// draft), an object is validated and stored as-is — including
		// enabled=false drafts, which are legal but still fully validated so
		// flipping the switch later cannot arm a set the engine must reject.
		ScopeRules json.RawMessage `json:"scope_rules"`
		// Proxy is the project's own upstream proxy, same three raw states:
		// absent leaves it alone, null clears it (back to the node default
		// route), an object stores a normalized endpoint — protocol/host/port/
		// auth plus the enable switch. It egresses only this project's
		// sessions; other projects on the same node keep their own route.
		Proxy json.RawMessage `json:"proxy"`
		// Retired model selectors: parsed only so an old client gets a clear 400
		// instead of having the field silently ignored by the external gateway.
		WorkerProfile *string `json:"worker_profile"`
		StrongProfile *string `json:"strong_profile"`
		// Retired review switch: parsed only to reject it. Accepting it would
		// store a value the console cannot show and dispatch ignores, which is
		// exactly the "looks configurable, does nothing" state this endpoint used
		// to have. Reviews are fixed by the premise policy.
		VerifyAuto json.RawMessage `json:"verify_auto"`
		// Retired admission switch, same reason: one mode exists and the manifest
		// is already normalized to it, so every write here was a no-op that looked
		// like an operator decision.
		PremisePolicy json.RawMessage `json:"premise_policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON"})
		return
	}
	if req.WorkerProfile != nil || req.StrongProfile != nil {
		writeJSON(w, 400, map[string]any{"error": "模型选择由外部网关管理，请移除 worker_profile/strong_profile"})
		return
	}
	if len(req.VerifyAuto) > 0 {
		writeJSON(w, 400, map[string]any{"error": "verify_auto 已废弃：复核由前提政策 verified_only 固定，没有开关可设；请移除该字段"})
		return
	}
	if len(req.PremisePolicy) > 0 {
		writeJSON(w, 400, map[string]any{"error": "premise_policy 已固定为 verified_only：只有一个准入模式，没有可选项；请移除该字段"})
		return
	}
	p := h.mgr.Get(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "项目不存在"})
		return
	}
	// A delete may have sealed this project between the route dispatch and
	// here: writes during the delete window would race the removal (the kit
	// drains runs while the tree is about to go away).
	if h.projectDeleting(id) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "项目正在删除，设置暂不接受修改",
			"code":  "project_deleting",
		})
		return
	}
	// A settings card submits several fields together. Validate the complete
	// request before any setter persists it, so a bad later field cannot leave
	// an earlier setting applied behind a 400 response.
	var limit *int
	var err error
	if len(req.MaxRuns) > 0 {
		limit, err = parseMaxRuns(req.MaxRuns)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.RoundTimeout != nil {
		if _, _, err := project.ParseRoundTimeout(*req.RoundTimeout); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	// Three shapes for the review switch, all validated before anything is
	// written: absent (leave it), null (clear the override), true/false (store it).
	var reviewOverride *bool
	reviewsSet := false
	if len(req.ReviewsDisabled) > 0 {
		reviewsSet = true
		if strings.TrimSpace(string(req.ReviewsDisabled)) != "null" {
			var b bool
			if err := json.Unmarshal(req.ReviewsDisabled, &b); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "reviews_disabled 必须是 true / false / null"})
				return
			}
			reviewOverride = &b
		}
	}
	// Three shapes for the scope rules too; the object shape is validated here,
	// before any other setter persists, so a bad rule set cannot ride behind a
	// 200 that saved max_runs.
	var scopeRuleSet *project.ScopeRules
	scopeRulesSet := false
	if len(req.ScopeRules) > 0 {
		scopeRulesSet = true
		if strings.TrimSpace(string(req.ScopeRules)) == "null" {
			scopeRuleSet = nil
		} else {
			var rules project.ScopeRules
			if err := json.Unmarshal(req.ScopeRules, &rules); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scope_rules 必须是范围规则对象或 null"})
				return
			}
			normalized, err := project.NormalizeScopeRules(rules)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			scopeRuleSet = &normalized
		}
	}
	// Three shapes for the proxy too, validated before anything persists —
	// same contract as the scope rules above.
	var egressProxy *project.EgressProxy
	proxySet := false
	if len(req.Proxy) > 0 {
		proxySet = true
		if strings.TrimSpace(string(req.Proxy)) == "null" {
			egressProxy = nil
		} else {
			var cfg project.EgressProxy
			if err := json.Unmarshal(req.Proxy, &cfg); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "proxy 必须是代理配置对象或 null"})
				return
			}
			normalized, err := project.NormalizeEgressProxy(cfg)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			egressProxy = &normalized
		}
	}
	// Scope and description are the project documents. Editing them here would
	// bypass the document revision: a console holding an old revision could then
	// overwrite a newer edit without a conflict. Creation still takes both (a new
	// project has no revision to lose), edits go through /api/documents/{kind}.
	if req.Scope != nil || req.Description != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "项目起点与约束现在只能通过 PUT /api/documents/scope 和 /api/documents/constraints 修改（带 revision）；这里不再接受这两个字段",
			"code":  "document_write_moved",
		})
		return
	}
	if len(req.MaxRuns) > 0 {
		if err := h.guardUpdateWrite(w, id); err != nil {
			return
		}
		if err := h.mgr.SetMaxRuns(id, limit); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.RoundTimeout != nil {
		if err := h.guardUpdateWrite(w, id); err != nil {
			return
		}
		if err := h.mgr.SetRoundTimeout(id, *req.RoundTimeout); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
	}
	// The scope rules are manifest-only in this phase: nothing re-briefs the
	// decider and nothing here dispatches — the audit pipeline picks the set up
	// through its own channel (P2). Saving must therefore not pretend to have
	// reconfigured a running engine; the console states that separately.
	if scopeRulesSet {
		if err := h.guardUpdateWrite(w, id); err != nil {
			return
		}
		if _, err := h.mgr.SetScopeRules(id, scopeRuleSet); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
	}
	// The proxy is manifest-only here as well: dispatch rides it to the node as
	// the 4zrcin.EgressProxy extension on the next turn, so an already-running
	// session switches at its next turn and no restart is owed. Saving must not
	// pretend to have reconfigured anything else.
	if proxySet {
		if err := h.guardUpdateWrite(w, id); err != nil {
			return
		}
		if _, err := h.mgr.SetEgressProxy(id, egressProxy); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
	}
	// The review switch is persisted and applied in one call: a settings write
	// that closes reviews must cancel the reviews already running (their verdicts
	// are gone) and one that opens them must re-arm the queue, exactly like the
	// project page's own endpoints. Writing only the manifest would leave the
	// project on the old switch until the next restart.
	reviewsCanceled := 0
	if reviewsSet {
		if k := h.kit(id); k != nil {
			canceled, err := k.srv.SetReviewsOverride(reviewOverride, h.instanceReviewsDisabled())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error": "保存复核开关失败：" + err.Error(),
					"code":  "reviews_persist_failed",
				})
				return
			}
			reviewsCanceled = canceled
		} else {
			// Not attached yet: persist only, the next attach reads the manifest.
			var err error
			if reviewOverride == nil {
				err = p.ClearReviewsDisabled()
			} else {
				err = p.SetReviewsDisabled(*reviewOverride)
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error": "保存复核开关失败：" + err.Error(),
					"code":  "reviews_persist_failed",
				})
				return
			}
		}
	}
	if k := h.kit(id); k != nil {
		// Refresh the project briefing before the next dispatch.
		k.srv.SetProjectSettings(h.scopeOf(k.proj), k.proj.DescriptionText())
		// Nothing in this request changes the review mode or the admission mode:
		// verify_auto and premise_policy are rejected above, so the review pump is
		// neither reconfigured nor re-armed and no commander wake is owed. A
		// settings edit that cannot affect either policy must not pretend to.
		if k.dec != nil {
			k.dec.SetScope(k.srv.CommanderDocs())
			if name, window := k.srv.CommanderModelInfo(); name != "" {
				k.dec.SetModelName(name)
				k.dec.SetContextWindow(window)
			}
			// Round wall clock follows the manifest: the edit applies from the
			// next round, no restart, and no running executor is interrupted.
			k.dec.SetRoundTimeout(h.commanderRoundTimeout(k.proj))
		}
	}
	// The delete path runs outside this handler's locks: re-fetch and refuse
	// instead of panicking on a nil project (h.info would dereference Store).
	current := h.mgr.Get(id)
	if current == nil {
		writeJSON(w, http.StatusGone, map[string]any{
			"error": "项目已删除，设置修改未生效",
			"code":  "project_deleted",
		})
		return
	}
	out := h.info(current)
	out.ReviewsCanceled = reviewsCanceled
	writeJSON(w, http.StatusOK, out)
}

// guardUpdateWrite refuses a settings write when the project entered its
// delete window after this request started. It returns true when the response
// was written and the caller must stop.
func (h *hub) guardUpdateWrite(w http.ResponseWriter, id string) error {
	if !h.projectDeleting(id) {
		return nil
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": "项目正在删除，设置暂不接受修改",
		"code":  "project_deleting",
	})
	return errors.New("project deleting")
}

// projectDeleting reports whether the project is inside its delete window.
// A project without a kit was never attached, so the flag does not exist for
// it — deletion of an unattached project is transferMu-guarded and fast, and
// the final re-fetch in updateProject covers that gap.
func (h *hub) projectDeleting(id string) bool {
	if k := h.kit(id); k != nil {
		return k.srv.Deleting()
	}
	return false
}

// parseMaxRuns decodes the optional cap: null clears the override, a
// non-negative integer sets it. 0 is legal and means uncapped.
func parseMaxRuns(raw json.RawMessage) (*int, error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, errors.New("max_runs 必须是整数或 null")
	}
	if n < 0 {
		return nil, errors.New("max_runs 不能是负数（0 = 不限）")
	}
	return &n, nil
}

// reopenDeletedLedger reopens a project ledger after a delete failed, but only
// while the file is still there. Opening a missing path creates a fresh, empty
// ledger, and a half-deleted project coming back as an empty one would silently
// replace real data with a new database: a missing ledger is a data-loss
// report, never a recovery.
func reopenDeletedLedger(p *project.Project) (*store.Store, error) {
	if _, err := os.Stat(p.LedgerPath()); err != nil {
		return nil, fmt.Errorf("账本文件已不存在（%v），未重建空账本；从备份恢复后重启 zrcin", err)
	}
	return store.OpenSQLitePooled(p.LedgerPath())
}

// deleteProject removes the engagement: everything below projects/<id>/ goes,
// the process stops touching it first, and the vulnerability library keeps what
// it mirrored (D34). There is no recycle bin on purpose; the confirm parameter
// is the only guard, and it asks for the id you are about to destroy.
func (h *hub) deleteProject(w http.ResponseWriter, r *http.Request, id string) {
	if r.URL.Query().Get("confirm") != id {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "删除会删掉整个项目，需要 confirm=<项目 id>",
		})
		return
	}
	p := h.mgr.Get(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + id})
		return
	}
	// An export walks this directory and a delete removes it. Let them queue
	// behind each other instead of letting one observe a tree the other is
	// changing.
	h.transferMu.Lock()
	defer h.transferMu.Unlock()
	p = h.mgr.Get(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个项目：" + id})
		return
	}

	// Stop writing before removing: a live run's finish() would recreate files
	// in a directory this call is about to delete. Close the dispatch gate
	// first (SetDeleting seals admission for the whole window), then give the
	// live runs one bounded evidence window before canceling them with the
	// restart cause. Only stop the decider/watchdog once nothing is left; a
	// timeout must leave the project working instead of permanently half-stopped
	// (the old order canceled the kit before checking).
	if k := h.kit(id); k != nil {
		k.srv.SetDeleting(true)
		// A delete is a stop, so it gets the same bounded evidence window a
		// process shutdown does: executors write down what they already have
		// before the cancel, instead of burning a turn for nothing. The cause is
		// project_stop, not restart: this is an administrative delete.
		if n, err := k.srv.DrainRunningWithCause(context.Background(), shutdownDrainTimeout,
			model.CancelCauseProjectStop, api.DrainDeleteReason); err != nil {
			log.Printf("project: %s 收尾窗口被中断：%v", id, err)
		} else if n > 0 {
			log.Printf("project: %s 有 %d 个运行在收尾后被取消", id, n)
		}
		if !k.srv.WaitIdle(15 * time.Second) {
			k.srv.SetDeleting(false)
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "还有运行没收尾，稍后重试",
			})
			return
		}
		if err := k.srv.CheckBrowserReleaseForDeletion(); err != nil {
			k.srv.SetDeleting(false)
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		// The commander's session credential is process-wide state: drop it
		// before the runtime goes away, or a token for this project id would
		// keep resolving (and become a second commander if the id is re-created).
		k.srv.RevokeCommanderIdentity()
		k.cancel()
	}

	// Release the ledger handle before walking the tree. The store keeps one
	// idle connection (internal/store/sqlite.go), and Windows refuses to remove
	// a directory whose ledger/WAL files are still open. Everything above has
	// already sealed admission and drained the runs, so no writer is left.
	if err := p.Store.Close(); err != nil {
		log.Printf("project: %s 关闭账本失败：%v", id, err)
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		// The directory is still here, so the project must stay usable: an
		// operator can retry the delete or keep working. The old kit's context
		// is already canceled (decider, watchdog, purpose runs), so reopening
		// only the gate would leave a live-looking project with a dead brain.
		// Rebuild the runtime instead; the new kit starts with the delete gate
		// open and a fresh project context (D75). The ledger was closed above,
		// so the rebuild needs it reopened first; if that fails, the runtime
		// cannot be trusted and the operator is told to restart.
		st, reopenErr := reopenDeletedLedger(p)
		if reopenErr != nil {
			log.Printf("project: %s 目录没删干净，账本也没能重开：%v / %v", id, err, reopenErr)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "目录没删干净：" + err.Error() + "；" + reopenErr.Error(),
			})
			return
		}
		p.Store = st
		if nk := h.attach(p); nk != nil {
			nk.srv.SetDeleting(false)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "目录没删干净：" + err.Error(),
		})
		return
	}
	h.mu.Lock()
	delete(h.kits, id)
	h.mu.Unlock()
	h.mgr.Remove(id)

	kept, err := h.vulns.MarkOrphaned(id)
	if err != nil {
		log.Printf("vulnlib: 标记 %s 的条目失败：%v", id, err)
	}
	log.Printf("project: 删除 %s（漏洞库保留 %d 条）", id, kept)
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":    id,
		"vulns_kept": kept,
		"projects":   h.mgr.Len(),
	})
}

// shutdownDrainTimeout is the longest a process stop or a project delete waits
// for evidence wrap-up. It matches the api drain window ceiling (90s), so a stop
// stays bounded even if a caller passes something larger.
const shutdownDrainTimeout = 90 * time.Second

// shutdownCleanupGrace is the budget the console gives the whole cleanup
// (drain + canceled-run settlement + host stop). It must be larger than
// shutdownDrainTimeout; otherwise shutdownConsole returns while the drain is
// still running and process exit kills the evidence window it just opened.
const shutdownCleanupGrace = shutdownDrainTimeout + 30*time.Second

// hostStopBudget is the slice of the cleanup grace reserved for taking the local
// Hub and scan node down. Without it the drain can use the whole window and the
// cleanup goroutine is cut off, leaving both children listening after zrcin is gone —
// observed twice during exe swaps. The drain is capped to whatever is left.
const hostStopBudget = 5 * time.Second

// drainSettleTimeout is how long DrainAll waits for already-canceled runs to
// settle before hosts are stopped. The run's finish() must not race host
// teardown; the old 5s was shorter than a normal evidence ingest.
const drainSettleTimeout = 15 * time.Second

// DrainAll is the process-stop wiring point for main.go's shutdown goroutine:
//
//  1. seal each project (closing gate + decider pause + review pump stop);
//  2. api.DrainRunning — deliver the shared evidence-wrap-up notice, wait the
//     bounded window, then cancel whatever is still running with the restart
//     cause;
//  3. WaitIdle so the canceled runs settle (their finish() must not race hosts
//     being stopped or the process exiting);
//  4. revoke each dead runtime's commander identity.
//
// Projects are drained in parallel, so the whole stop is bounded by timeout
// rather than timeout×projects. It returns the number of runs the drain canceled.
// main.go must give the cleanup at least shutdownCleanupGrace; otherwise the
// process exits before this function can finish.
func (h *hub) DrainAll(timeout time.Duration) int {
	if timeout <= 0 || timeout > shutdownDrainTimeout {
		timeout = shutdownDrainTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		canceled int
	)
	for _, p := range h.mgr.All() {
		k := h.kit(p.ID)
		if k == nil {
			continue
		}
		wg.Add(1)
		go func(k *kit, id string) {
			defer wg.Done()
			// Seal first: nothing new may start during the evidence window.
			k.srv.BeginDrain()
			n, err := k.srv.DrainRunning(ctx, timeout)
			if err != nil {
				log.Printf("shutdown: %s 的收尾窗口被中断：%v", id, err)
			}
			if !k.srv.WaitIdle(drainSettleTimeout) {
				log.Printf("shutdown: %s 仍有运行未在 %s 内结束", id, drainSettleTimeout)
			}
			// The commander's session credential belongs to this runtime; drop it
			// once the rounds are gone (same rule the old shutdown path had).
			k.srv.RevokeCommanderIdentity()
			mu.Lock()
			canceled += n
			mu.Unlock()
		}(k, p.ID)
	}
	wg.Wait()
	return canceled
}

func (h *hub) serveVulns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	project := r.URL.Query().Get("project")
	q := r.URL.Query().Get("q")
	list := h.vulns.List(project, q, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"vulns": list,
		// total is the whole library; matched is what the current filter hits,
		// including rows beyond this page's limit.
		"total":   h.vulns.Len(),
		"matched": h.vulns.Count(project, q),
	})
}

// serveVulnEvidence streams one evidence copy from the vulnerability library.
//
// The library copies attacker-influenced bytes (a captured body, a dumped
// binary) and serves them from the console's own origin, so the default is a
// preview, not a download: inline, never sniffed, and CSP-sandboxed when the
// browser would render the file as a document. Saving the original is the
// explicit ?download=1 action. This mirrors the project evidence endpoint on
// the same rule; the document-type decision is shared through the evidence
// package instead of being copied per handler.
func (h *hub) serveVulnEvidence(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/vulns/evidence/")
	path, ok := h.vulns.EvidencePath(rel)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这份证据"})
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		// FormatMediaType is the single writer of this header: it emits filename*
		// (RFC 2231/5987) for non-ASCII names and quotes specials.
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": pathpkg.Base(rel)}))
	} else {
		w.Header().Set("Content-Disposition", "inline")
		// The console's preview sheet frames PDFs; the instance-wide
		// X-Frame-Options: DENY (withSecurityHeaders) would block that
		// same-origin frame. Only this endpoint relaxes it, and document types
		// are CSP-sandboxed below.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		if ct, ok := evidence.DocumentType("", path); ok {
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Security-Policy", "sandbox")
		}
	}
	http.ServeFile(w, r, path)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// projectNameFromBrief derives a display name from the one-paragraph project
// brief. The simplified console does not ask for a name; an empty paragraph is
// still a legal (if useless) engagement, so the fallback is explicit instead of
// an error from Manager.Create.
func projectNameFromBrief(brief string) string {
	for _, line := range strings.Split(brief, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			r := []rune(line)
			if len(r) > 40 {
				return string(r[:40]) + "…"
			}
			return line
		}
	}
	return "未命名项目"
}
