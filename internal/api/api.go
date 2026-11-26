// Package api exposes the ledger over HTTP and owns run dispatch.
package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/c1ayooo/4zrcin/internal/aopexec"
	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/decider"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/host"
	"github.com/c1ayooo/4zrcin/internal/kb"
	"github.com/c1ayooo/4zrcin/internal/ledger"
	"github.com/c1ayooo/4zrcin/internal/lifecycle"
	"github.com/c1ayooo/4zrcin/internal/metrics"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/pocs"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/report"
	"github.com/c1ayooo/4zrcin/internal/retrieval"
	"github.com/c1ayooo/4zrcin/internal/settings"
	"github.com/c1ayooo/4zrcin/internal/state"
	"github.com/c1ayooo/4zrcin/internal/store"
	"github.com/c1ayooo/4zrcin/internal/verifier"
)

type Server struct {
	Embedder retrieval.Embedder
	Store    *store.Store
	Exec     executor.Executor
	DataDir  string
	// PersistDoc writes one saved document to the project manifest (scope and
	// constraints live in project.json, which outlives the ledger). The hub
	// supplies it; a bare test server leaves it nil and only the in-memory
	// snapshot and the ledger revision change.
	PersistDoc func(kind, text string) error
	// GlobalFence reads the instance fence (spec #1 工单3): the red lines ride
	// every dispatch and commander turn as the 4zrcin.GlobalFence extension.
	// The hub supplies it (the fence is instance state, not project state); a
	// bare test server leaves it nil and the turn carries no extension. Typed,
	// not `any`: a wrong concrete type must be a compile error — silently
	// stripping the red lines is exactly what this field exists to prevent.
	GlobalFence func() settings.Fence
	// Metrics is the Phase 1 sidecar recorder. It never writes the ledger and
	// nil (a bare test server or an unwritable directory) is a no-op.
	Metrics *metrics.Recorder
	// Lifecycle owns admission, the claim, the live-run registries, cancellation
	// and settlement. Handlers keep HTTP decoding/identity/prompt/views and call
	// this one implementation; New initializes it before any request can arrive.
	Lifecycle *lifecycle.Service
	// Project is the engagement this server fronts. It supplies project policy
	// and lifetime; execution is always on the local host. Nil is for bare tests.
	Project  *project.Project
	CaseRoot string
	// Scope is injected into every dispatch brief. Empty means "as authorised,
	// do not widen", which is a sentence, not a policy engine.
	Scope string
	// Description is the operator's prose for this engagement (boundaries,
	// forbidden actions, platform rules, how to treat target output). The
	// program only carries it into the brief; it never parses or enforces it.
	Description string
	// Timeout bounds one run. It lives here, not in the executor, because a
	// timeout is the controller's decision and must not look like a failure.
	Timeout time.Duration
	// MaxRuns caps concurrent intent runs (purpose runs are not counted).
	// 0 leaves it uncapped; the hub sets it from --max-runs.
	MaxRuns int
	// MaxWorkers is the instance-wide cap on concurrent intent runs across all
	// projects (--max-workers, 0 = uncapped). The hub supplies GlobalRunning,
	// which sums the live runs of every project so the cap is a real instance
	// cap, not a per-project one (sapper's MAX_WORKERS; docs/17 §3.5).
	MaxWorkers int
	// GlobalRunning reports how many intent runs are live across the instance.
	// nil means the server is not attached to a hub (tests), so only MaxRuns
	// applies.
	GlobalRunning func() int
	// InstanceDispatch serializes admission through the durable run record
	// across projects. Standalone servers use dispatchMu alone.
	InstanceDispatch *sync.Mutex
	// IdleTimeout cancels a run that has emitted no event for this long and
	// holds its intent (the commander can retry). 0 disables the sweep.
	IdleTimeout time.Duration
	// ModelStallLimit is how many consecutive "empty assistant reply, then
	// llm_request again, no tool call / turn end" cycles cancel a run with
	// model_stall. A weak model can keep the event stream alive while making no
	// progress, so idle-timeout never fires; this is the liveness stop for that
	// (final-e2e I1). New() defaults it to 3; 0 disables the detector.
	ModelStallLimit int
	Ingest          *ledger.Ingestor
	// State is the decider's short memory (docs/03), a single markdown file.
	State *state.File
	// APIBase is the control plane's own base URL (http://host:port). The
	// commander session needs it to call the API with its own tools; empty
	// falls back to the loopback default.
	APIBase string
	// Decider is nil when no model is configured; every caller must tolerate it.
	Decider *decider.Runner
	// Skills are default SKILL.md paths handed to every dispatch (D23).
	Skills []string
	// SkillsDir is where a dispatch resolves skill names from. It is
	// instance-level, not project-level: house style is not an engagement.
	SkillsDir string
	// Say injects text into a live AOP session. The hub wires it to the host's
	// embedded IOA server; nil means this instance has no say channel (audit
	// only, or a harness without IOA).
	Say func(ctx context.Context, in model.Intent, sess aopexec.SessionInfo, text string, interrupt bool) (string, error)
	// SayEvent is Say with the message's stable event id, which becomes the v1
	// envelope id so a retried delivery is deduped by identity. Nil falls back
	// to Say.
	SayEvent func(ctx context.Context, in model.Intent, sess aopexec.SessionInfo, text string, interrupt bool, eventID string) (string, error)
	// SayResult delivers a result as a v1 task.result event with its chain, when
	// the message kind is result. Nil falls back to the plain message event.
	SayResult func(ctx context.Context, in model.Intent, sess aopexec.SessionInfo, text, eventID string, chain []string) (string, error)
	// Ctx is the project's lifetime context. Report and verifier runs inherit
	// it, so the same switch that stops the decider and the watchers on delete
	// also reaches work that dispatch never started.
	Ctx context.Context
	// Verifier re-runs the proof behind report-bound facts; nil disables it.
	Verifier *verifier.Verifier
	// Reports writes the deliverable directory; nil disables it.
	Reports *report.Generator
	// POCs is the neutron template directory shared with the executor.
	POCs *pocs.Store
	// KBDir is the offline knowledge-base directory (entries/*.json). It is
	// pointed at in the executor brief; the decider gets a read tool over the
	// same entries.
	KBDir string
	KB    *kb.Library
	// AutoVerify selects the broad review pump: every reviewable fact, not only
	// the premises a downstream direction cites. It is not an operator setting —
	// reviews are fixed by the premise policy, so every production hub passes
	// false and the pump serves required candidates only; tests turn it on to
	// exercise the broad path. The retired verify_auto key never reaches here.
	AutoVerify bool
	// ReviewConcurrency is how many directions may have an automatic premise
	// review in flight at once. The hub wires it to the auxiliary-concurrency
	// gate; nil/<=0 keeps the historical serial pump. One direction is still
	// reviewed serially, so its cursor and cooldown remain per-direction.
	ReviewConcurrency func() int
	// ArsenalDirs are the arsenal roots ("<dir>/bin" + manifest.json) this
	// instance reports. Empty means instance + user defaults (tools.go).
	ArsenalDirs []string
	// ModelSettings supplies the single model gateway identity for run records.
	settingsMu    sync.RWMutex
	ModelSettings func() (provider, model string, contextWindow int)
	// LocalNode is the node the control plane started. A named profile is
	// served by a local host pool, so only this node can honour one.
	LocalNode string
	// LocalHostStatus reports whether the local host is usable right now. nil
	// disables the gate (bare test servers). A control plane can start in
	// degraded mode when the model endpoint is down (host.ErrNotReady); the
	// default-model dispatch runs on that host, so refusing it here yields one
	// actionable error instead of runs that fail in milliseconds inside the
	// harness (D101). Named-profile runs are not gated: their host comes from
	// the profile pool.
	LocalHostStatus func() host.HostStatus
	// ChildPeaks samples the peak resident set of the local host's child
	// processes at every settled run (nil on bare test servers and audit-only
	// instances, where there is nothing to sample). Bookkeeping for after-the-
	// fact diagnosis: nothing here caps memory, kills a process, or feeds
	// scheduling (see proc_peak.go).
	ChildPeaks func() []host.PeakRSS
	// Collab issues the per-run identity tokens. nil disables the participant
	// API (bare test servers); the console and dispatch still work.
	Collab *collab.Registry

	// Secrets returns the values that must never reach a run journal or a ledger
	// pulse: every profile API key (including rotation spares) and the console
	// token. The hub wires it; nil disables redaction (bare test servers).
	// journal.go replaces each occurrence with *** at the single journal write
	// point and in the pulse text — a backstop, not a reason to print secrets.
	Secrets func() []string
	// secretMu guards the redaction table cached by journal.go.
	secretMu   sync.Mutex
	secretAt   time.Time
	secretPats []secretPattern

	// guardrailMu guards the cached tool-approval projection the operator inbox
	// reads (guardrail.go). It is a read cache, not scheduling state: the inbox
	// never dials the host, the pending endpoint refreshes this snapshot.
	guardrailMu     sync.Mutex
	guardrailAt     time.Time
	guardrailState  string
	guardrailReason string
	guardrailCounts map[string]int

	// contextWindow is the configured model context budget, used to turn a
	// run's live context_tokens into a pressure bar. It changes when the
	// settings page saves, so it is atomic rather than a plain field.
	contextWindow atomic.Int64
	// lastWatch is when the watchdog last swept (unix nano). It is observation
	// for the console's guard panel, not scheduling state: the loop itself owns
	// its ticker.
	lastWatch atomic.Int64
	// paused is the project-level gate (projectPause). A decision round that
	// already passed the decider's own pause check used to dispatch anyway;
	// starting a run is the point where "paused" must be enforced.
	paused         atomic.Bool
	closeoutCancel context.CancelFunc // Guarded by controlMu.
	closeout       atomic.Bool        // One explicit commander closeout; never resumes dispatch.
	closing        atomic.Bool        // Process lifetime only; never changes the project manifest.
	controlMu      sync.Mutex
	// docWriteMu serialises document saves per project: the save writes the
	// manifest first and rolls it back when the ledger refuses, so two writers
	// must not interleave those steps (internal/api/documents.go).
	docWriteMu sync.Mutex
	// deleting closes the same gate for a project that is being removed: a
	// request that slips in between WaitIdle and RemoveAll would recreate the
	// case directory inside the tree being deleted.
	deleting atomic.Bool
	// pauseCause is the structured reason a pause was applied (pauseCauseManual /
	// pauseCauseCompletion / pauseCauseCommander). It is guarded by controlMu:
	// every pause/resume path already serialises there, and append-and-continue
	// classifies inside that same section. The manifest persists the same cause
	// next to the boolean, and a boot attach restores it, so a restart keeps
	// saying who stopped the project instead of collapsing to "operator".
	pauseCause string
	// scopeMu guards resumeScope. The operator's "continue current goal" resume
	// attaches the named goal to exactly one commander round (StartCommander
	// consumes it), so the round continues that goal without a persistent
	// instruction or dialogue. A later generic resume never inherits it.
	scopeMu     sync.Mutex
	resumeScope *resumeScope
	// persistPaused writes the manifest's paused flag. nil means "write the real
	// project manifest"; a test injects a failure to prove that a durable write
	// failure leaves the runtime paused in agreement with the manifest instead of
	// silently widening the divergence.
	persistPaused func(bool) error
	// persistPausedKind persists the pause together with its structured cause, so
	// a restart still says who stopped the project (manual / completion stand-by
	// / commander stand-by) instead of collapsing every pause into "operator".
	// nil falls back to persistPaused or the real manifest.
	persistPausedKind func(paused bool, kind string) error

	mu       sync.Mutex
	accessMu sync.Mutex
	// identityMu serializes publishing and removing the case's shared run
	// identity. An old completion must not unlink a replacement's credential.
	identityMu sync.Mutex
	// commanderMu guards commanderIdent, the commander session's credential. It
	// is reused across rounds (per session, not per round) so a long-lived shell
	// holding the exported ZRCIN_AUTH does not go stale between rounds.
	commanderMu    sync.Mutex
	commanderIdent *collab.Identity
	// browserActive is guarded by accessMu. Native network I/O does not hold
	// that lock, so occupancy remains readable and conflicts fail promptly.
	browserActive map[string]bool
	artifactMu    sync.Mutex
	// pumpMu serialises child/wake scheduling. StartRun is itself serialized;
	// this only keeps two pumps from scanning the same queue and logging the
	// same capacity errors.
	pumpMu sync.Mutex
	// reviewsPaused is the review kill switch, independent of the project pause
	// (D124/D137): an operator can stop reviews without resuming the commander,
	// or keep reviewing while the engagement is paused.
	reviewsPaused atomic.Bool
	// verifyDisabled is the runtime review switch: no review run can start, so no
	// verdict can ever arrive. It starts as --no-verify at boot, but the operator
	// can open and close it per project at runtime (POST /api/verifier/disable and
	// /api/verifier/enable), which is why it is atomic rather than a boot-time
	// field. It is deliberately distinct from reviewsPaused (a temporary pause):
	// only this state relaxes premise admission and shows unverified text, because
	// a pause is reversible and a direction dispatched under it would otherwise be
	// frozen without the review it was promised.
	verifyDisabled atomic.Bool
	// reviewBootDisabled is the instance's boot value (--no-verify), kept apart
	// from the runtime switch: the project may close or open reviews later, and
	// an export has to be able to say both "how it was started" and "how it stood
	// when the archive was written". It never changes at runtime.
	reviewBootDisabled bool
	// reviewPhases tracks the fixed-review phases currently running, keyed by
	// intent id. A review pause suspends the phase (it must not publish a
	// verdict), and resuming re-arms the directions that were left in reviewing;
	// this registry is what keeps a resume from starting a second phase on a
	// direction whose goroutine is still alive.
	reviewPhases sync.Map
	// recent records the successful ledger mutations made through the API
	// (intent/steer/say/park/cancel/retry/complete/playbook). A commander round
	// takes the ones recorded while it ran, so the console shows what actually
	// happened instead of what the model claimed (D78).
	recentMu  sync.Mutex
	recent    []recentAction
	recentSeq uint64
	// toolCache keeps the leaderboard cheap: a run log is re-read only when its
	// size or mtime changed (tools.go).
	toolCache map[string]toolFile
	// agentMux is the participant API, mounted by the hub under
	// /agent/<project>/api/. It is a separate mux because its authentication is
	// a per-run collab token, not the console token.
	agentMux *http.ServeMux
	// journals is the single writer per live run's JSONL log (journal.go).
	// Dispatch opens one for the run's lifetime; message delivery and injection
	// audit append through it instead of touching the dispatch goroutine's
	// encoder. journalsMu guards the map only, never the Encode call.
	journalsMu sync.Mutex
	journals   map[string]*runJournal
	// verifyBatchMu guards verifyBatch: a project runs at most one batch review
	// at a time, so a second click cannot double the token bill (D100).
	verifyBatchMu sync.Mutex
	verifyBatch   bool
	// reviewFailMu guards reviewFailed: the automatic review queue's retry
	// cooldown. An automatic review that ends without a usable verdict leaves the
	// fact unverified, so without this every wake would spend another model run
	// on the same known-bad candidate (F11, auto_verify.go).
	reviewFailMu      sync.Mutex
	autoReviewMu      sync.RWMutex
	reviewFailed      map[string]reviewFailure
	reviewPumpMu      sync.Mutex
	reviewPumpRunning bool
	reviewPumpEpoch   uint64
	reviewPumpDue     map[string]time.Time
	reviewPumpWake    chan struct{}
	reviewPumpCancel  context.CancelFunc
	// slimRetryMu guards slimRetryFor: the one automatic context-limit recovery
	// hands "build the degraded brief" to the next dispatch of this direction.
	// In-process only: the retry starts in the same process right after the
	// failed run settles (context_limit.go).
	slimRetryMu  sync.Mutex
	slimRetryFor map[string]bool
}

// runWarningEvent is the synthetic journal line for one handoff warning. It uses
// the same top-level "kind" key as executor.Event so the JSONL stays one format;
// the console renderer ignores kinds it does not know (task-17).
type runWarningEvent struct {
	Kind string    `json:"kind"` // "warning"
	At   time.Time `json:"at"`
	Text string    `json:"text"`
	V    int       `json:"v,omitempty"` // stamped by runJournal.append
}

// defaultModelStallLimit is the liveness default: three consecutive empty
// replies with an llm_request between them and no tool call / turn end is a
// stuck model, not a slow one.
const defaultModelStallLimit = 3

// modelStallEnv overrides the threshold without a new command-line flag:
// "0" disables the detector, a positive integer changes the cycle count.
// The Server field wins when it was set explicitly (tests do that); New() only
// consults the environment for its default.
const modelStallEnv = "ZRCIN_MODEL_STALL_LIMIT"

func configuredModelStallLimit() int {
	raw := strings.TrimSpace(os.Getenv(modelStallEnv))
	if raw == "" {
		return defaultModelStallLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		log.Printf("api: %s=%q 不是非负整数，按默认 %d", modelStallEnv, raw, defaultModelStallLimit)
		return defaultModelStallLimit
	}
	return n
}

// modelStallWarning is the structured warning a stall writes onto an intent run.
// It is operator-facing advice, not a task verdict: the run is canceled because
// it made no progress, and the direction is held for an explicit retry.
const modelStallWarning = "模型连续空回复（model_stall）：本轮被看门狗取消（模型多次只回空内容、随后又请求 LLM，却没有工具调用/回合结束）。" +
	"意图已置 failed+Hold，不会自动重派；建议检查外部模型网关及工具调用响应，再用 retry 显式重试。"

// purposeStallWarning is modelStallWarning for a purpose run (commander,
// verifier, report). Those runs have no intent to hold, so the warning must not
// claim one was: the session is kept and the next wake / operator re-trigger is
// the way back (D108).
const purposeStallWarning = "模型连续空回复（model_stall）：本轮被看门狗取消（模型多次只回空内容、随后又请求 LLM，却没有工具调用/回合结束）。" +
	"本 run 是 purpose run，没有可挂起的意图；已取消并保留会话，不会自动重派。建议检查外部模型网关及工具调用响应，再唤醒下一轮或由操作员显式重试。"

// modelStallDetector counts one run's consecutive "empty assistant reply then
// llm_request" cycles. It is per run (dispatch and commander each hold their
// own), so a healthy run cannot mask a stalled one and vice versa.
//
// Sequence it looks for, from the final E2E:
//
//	message(role=assistant, text="")  -> status(llm_request)  -> repeat
//
// A tool call, a tool result, a turn end, an error, or a non-empty assistant
// reply breaks the streak: those are progress.
type modelStallDetector struct {
	limit       int
	emptyReply  bool
	consecutive int
	triggered   bool
}

func newModelStallDetector(limit int) *modelStallDetector {
	if limit <= 0 {
		return nil // disabled
	}
	return &modelStallDetector{limit: limit}
}

// observe feeds one event and reports whether this event completes the stall.
func (d *modelStallDetector) observe(ev executor.Event) bool {
	if d == nil || d.triggered {
		return false
	}
	switch ev.Kind {
	case executor.KindMessage:
		if ev.Role != "assistant" {
			return false
		}
		if strings.TrimSpace(ev.Text) == "" {
			d.emptyReply = true
			return false
		}
		// A real answer is progress even if it is not a tool call.
		d.emptyReply, d.consecutive = false, 0
	case executor.KindToolCall, executor.KindToolResult, executor.KindTurnEnd, executor.KindError:
		d.emptyReply, d.consecutive = false, 0
	case executor.KindStatus:
		if !isLLMRequestState(ev.State) || !d.emptyReply {
			return false
		}
		d.emptyReply = false
		d.consecutive++
		if d.consecutive >= d.limit {
			d.triggered = true
			return true
		}
	}
	return false
}

// isLLMRequestState recognises the harness status that follows a model call.
// The state vocabulary is the harness's; the word is stable ("llm_request"),
// and an unknown state simply never counts as a stall.
func isLLMRequestState(state string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(state)), "llm_request")
}

// newModelStallDetector builds one detector for one run, honouring the
// configured limit (0 = disabled).
func (s *Server) newRunStallDetector() *modelStallDetector {
	return newModelStallDetector(s.ModelStallLimit)
}

// noteModelStall is the liveness stop for one run: record the structured
// warning on the run (flushed to the journal as kind=warning at settle), then
// cancel it with the model_stall cause. finish() holds the intent as
// failed+Hold, so the pump cannot immediately re-dispatch the same stalled
// direction; a human/decision retry is the explicit way back. A purpose run
// (commander/verifier/report) has no intent to hold, so it gets the variant that
// does not claim a direction was (D108).
//
// It does not touch project state and does not judge the task: it says "no
// progress", never "the task failed" (docs/17 §3.6, agent.md §1).
func (s *Server) noteModelStall(runID string) {
	if runID == "" {
		return
	}
	_, _ = s.Store.UpdateRun(runID, func(r *model.Run) {
		warning := modelStallWarning
		if r.Purpose != "" {
			warning = purposeStallWarning
		}
		appendRunWarning(r, warning)
	})
	reason := "模型连续空回复（model_stall）：模型空转，已取消本轮；请检查外部模型网关，retry 才会重启"
	_ = s.CancelRunWithCause(runID, model.CancelCauseModelStall, reason)
}

// SetContextWindow updates the budget live progress is measured against. The
// hub calls it at attach and whenever the settings page saves.
func (s *Server) SetContextWindow(n int) {
	s.contextWindow.Store(int64(n))
}

// ModelContext is the context budget used before the harness reports the
// model's real window. The harness resolves it (usage.detail.context_window)
// and the run records that; this value is only the fallback for a profile left
// on "auto" and for the console before the first report (D80).
func (s *Server) ModelContext() int {
	if n := int(s.contextWindow.Load()); n > 0 {
		return n
	}
	return 128000
}

func New(st *store.Store, ex executor.Executor, dataDir, caseRoot string) *Server {
	s := &Server{
		Store:     st,
		Exec:      ex,
		DataDir:   dataDir,
		CaseRoot:  caseRoot,
		Metrics:   metrics.Open(dataDir),
		Ctx:       context.Background(),
		Ingest:    &ledger.Ingestor{Store: st, MaxEvidence: 200},
		State:     state.NewLedger(filepath.Join(dataDir, "state.md"), st),
		SkillsDir: filepath.Join(dataDir, "skills"),
		toolCache: map[string]toolFile{},

		// Liveness default: three consecutive empty replies is a stall, not a
		// slow model (ZRCIN_MODEL_STALL_LIMIT overrides; 0 disables). A bare
		// Server literal keeps 0 (disabled) unless the field is set, so tests
		// construct exactly the behaviour they assert.
		ModelStallLimit: configuredModelStallLimit(),
	}
	if s.Metrics != nil && st != nil {
		// A process restart recreates this recorder; the harness sessions named
		// by durable runs are warm, not first. Seed them before any event arrives.
		var sessions []string
		for _, run := range st.Runs() {
			if run.SessionID != "" {
				sessions = append(sessions, run.SessionID)
			}
		}
		s.Metrics.SeedSessions(sessions...)
	}
	s.Embedder = configuredEmbedder()
	s.Lifecycle = newLifecycle(s, st)
	s.agentMux = http.NewServeMux()
	s.registerAgent(s.agentMux)
	return s
}

// newLifecycle builds the single run-lifecycle implementation for a real
// project. A bare prompt/formatting test server is built with New(nil, ...):
// it has no ledger, so it has no lifecycle, and every lifecycle-dependent
// entry point (dispatch, cancel, recover) is unreachable there. The four gate
// atomics stay on Server as the read surface for handlers.
func newLifecycle(s *Server, st *store.Store) *lifecycle.Service {
	if st == nil {
		return nil
	}
	return lifecycle.New(lifecycle.Options{
		Store: st,
		Gates: lifecycle.Gates{
			Closing:       &s.closing,
			Paused:        &s.paused,
			Deleting:      &s.deleting,
			ReviewsPaused: &s.reviewsPaused,
		},
		InstanceDispatch:   func() *sync.Mutex { return s.InstanceDispatch },
		ProjectLimit:       s.maxRunsLimit,
		RunningIntentCount: s.runningIntentCount,
		MaxWorkers:         func() int { return s.MaxWorkers },
		GlobalRunning: func() int {
			if s.GlobalRunning == nil {
				return 0
			}
			return s.GlobalRunning()
		},
		BlockedPremises: s.blockedPremises,
		PlanRun:         s.planRun,
		Dispatch:        s.dispatch,
		RevokeRun: func(runID string) {
			if s.Collab != nil {
				s.Collab.RevokeRun(runID)
			}
		},
		RemoveRunIdentity: s.removeRunIdentity,
		Redact:            s.redactSecrets,
		AfterSettle:       s.afterIntentSettled,
		WakeDecider: func(reason string) {
			s.wakeCommanderIfNeeded(reason)
		},
		ReviewAfterRun:       s.reviewAfterRun,
		AfterProviderFailure: s.afterProviderFailure,
		Pump:                 s.pump,
		PauseDecider: func(reason string) {
			if s.Decider != nil {
				s.Decider.PauseFor(reason)
			}
		},
		BrowserActiveCount: func() int {
			s.accessMu.Lock()
			defer s.accessMu.Unlock()
			return len(s.browserActive)
		},
	})
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/intents/{id}/context", s.updateTaskContext)
	mux.HandleFunc("GET /api/instructions", s.listInstructions)
	mux.HandleFunc("POST /api/instructions", s.createInstruction)
	mux.HandleFunc("GET /api/instructions/{id}", s.getInstruction)
	mux.HandleFunc("PUT /api/instructions/{id}", s.updateInstruction)
	mux.HandleFunc("GET /api/instructions/{id}/history", s.instructionHistory)
	mux.HandleFunc("POST /api/instructions/{id}/ack", s.acknowledgeInstruction)
	mux.HandleFunc("GET /api/kb", s.worldKB)
	mux.HandleFunc("GET /api/phase1/summary", s.phase1Summary)
	mux.HandleFunc("POST /api/kb/candidates", s.nominateProjectWorldKB)
	mux.HandleFunc("GET /api/traffic/flows", s.listHTTPFlows)
	mux.HandleFunc("GET /api/traffic/flow", s.httpFlow)
	mux.HandleFunc("GET /api/artifacts", s.listArtifacts)
	mux.HandleFunc("GET /api/artifacts/data", s.artifactData)
	mux.HandleFunc("GET /api/access", s.listAccess)
	mux.HandleFunc("POST /api/access", s.createAccess)
	mux.HandleFunc("POST /api/access/{id}/browser", s.browserAction)
	mux.HandleFunc("POST /api/access/{id}/lease", s.browserLease)
	mux.HandleFunc("DELETE /api/access/{id}", s.deleteAccess)
	mux.HandleFunc("GET /api/access/{id}/material", s.accessMaterial)
	mux.HandleFunc("GET /api/operator-requests", s.operatorRequests)
	mux.HandleFunc("GET /api/operator-inbox", s.operatorInbox)
	// Tool approval: the host holds an intercepted tool call until the operator
	// answers. Operator-only, and it never executes the tool here (guardrail.go).
	mux.HandleFunc("GET /api/guardrail/pending", s.guardrailPending)
	mux.HandleFunc("POST /api/guardrail/resolve", s.guardrailResolve)
	mux.HandleFunc("POST /api/operator-requests", s.createOperatorRequest)
	mux.HandleFunc("PUT /api/operator-requests/{id}", s.updateOperatorRequest)
	mux.HandleFunc("POST /api/operator-requests/{id}/resolve", s.resolveOperatorRequest)
	mux.HandleFunc("POST /api/operator-requests/{id}/request-details", s.requestOperatorDetails)
	mux.HandleFunc("POST /api/intents/{id}/operator", s.requestOperator)
	mux.HandleFunc("POST /api/intents/{id}/operator/complete", s.completeOperator)
	mux.HandleFunc("GET /api/nodes", s.listNodes)
	mux.HandleFunc("PUT /api/nodes", s.setNodes)
	mux.HandleFunc("GET /api/nodes/join", s.nodeJoin)
	mux.HandleFunc("GET /api/nodes/{id}/terminal", s.nodeTerminal)
	mux.HandleFunc("POST /api/intents", s.createIntent)
	mux.HandleFunc("POST /api/chat", s.chat)
	// One operator text entry: transient hint vs durable constraint is a
	// lifetime choice, not a second panel (docs/06 D147).
	mux.HandleFunc("POST /api/directives", s.operatorDirective)
	mux.HandleFunc("GET /api/intents", s.listIntents)
	mux.HandleFunc("GET /api/intents/{id}", s.getIntent)
	mux.HandleFunc("GET /api/intents/{id}/readiness", s.getIntentReadiness)
	mux.HandleFunc("POST /api/intents/{id}/run", s.runIntent)
	mux.HandleFunc("POST /api/intents/{id}/children", s.createChildIntent)
	mux.HandleFunc("GET /api/intents/{id}/children", s.listChildIntents)
	mux.HandleFunc("POST /api/intents/{id}/wait", s.waitIntent)
	mux.HandleFunc("POST /api/intents/{id}/handoff", s.handoffIntent)
	mux.HandleFunc("GET /api/intents/{id}/usage", s.intentUsage)
	mux.HandleFunc("GET /api/usage/breakdown", s.usageBreakdown)
	mux.HandleFunc("POST /api/intents/{id}/steer", s.steerIntent)
	mux.HandleFunc("POST /api/intents/{id}/say", s.sayIntent)
	mux.HandleFunc("POST /api/intents/{id}/park", s.parkIntent)
	mux.HandleFunc("POST /api/intents/{id}/cancel", s.cancelIntent)
	mux.HandleFunc("POST /api/intents/{id}/retry", s.retryIntent)
	mux.HandleFunc("GET /api/intents/{id}/retry-preview", s.retryPreview)
	mux.HandleFunc("POST /api/intents/{id}/reopen", s.reopenIntent)
	// Continue is the operator/commander's "same worker session, next turn":
	// it is not on the agent mux because a worker does not continue itself.
	mux.HandleFunc("POST /api/intents/{id}/continue", s.continueIntent)
	mux.HandleFunc("GET /api/facts", s.listFacts)
	mux.HandleFunc("GET /api/changes", s.changes)
	mux.HandleFunc("GET /api/context", s.workingContext)
	mux.HandleFunc("GET /api/experience", s.experienceCandidates)
	mux.HandleFunc("POST /api/retrieval/index", s.indexFacts)
	mux.HandleFunc("GET /api/retrieval/status", s.retrievalStatus)
	mux.HandleFunc("POST /api/context/ack", s.acknowledgeContext)
	mux.HandleFunc("GET /api/graph", s.graphQueryWithOperatorScope)
	mux.HandleFunc("GET /api/graph/export", s.graphExport)
	mux.HandleFunc("GET /api/facts/{id}/history", s.factHistory)
	mux.HandleFunc("GET /api/runs/{id}/output", s.runOutput)
	mux.HandleFunc("GET /api/runs/{id}/history", s.runHistory)
	mux.HandleFunc("GET /api/runs/{id}/commands", s.runCommands)
	mux.HandleFunc("GET /api/runs/{id}/media/{name}", s.runMedia)
	mux.HandleFunc("GET /api/facts/{id}", s.getFact)
	mux.HandleFunc("GET /api/playbooks", s.listPlaybooks)
	mux.HandleFunc("GET /api/playbooks/{id}", s.getPlaybook)
	mux.HandleFunc("GET /api/playbooks/{id}/history", s.playbookHistory)
	mux.HandleFunc("POST /api/playbooks/{id}/archive", s.archivePlaybook)
	mux.HandleFunc("DELETE /api/playbooks/{id}", s.deletePlaybook)
	mux.HandleFunc("POST /api/playbooks/{id}/kb-candidate", s.nominatePlaybookKB)
	mux.HandleFunc("POST /api/playbooks/nominate", s.nominatePlaybook)
	mux.HandleFunc("POST /api/playbooks/{id}/retire", s.retirePlaybook)
	mux.HandleFunc("POST /api/facts/{id}/verify", s.verifyFact)
	// The operator's "review everything before the report" path: one batch,
	// serial, so it does not compete with itself for the node (D100).
	mux.HandleFunc("POST /api/facts/verify-pending", s.verifyPendingFacts)
	// The review switch is independent of the project pause (D137): it stops or
	// permits verifier runs without touching commander or executor dispatch.
	mux.HandleFunc("GET /api/verifier", s.verifierStatus)
	mux.HandleFunc("POST /api/verifier/pause", s.verifierPause)
	mux.HandleFunc("POST /api/verifier/resume", s.verifierResume)
	// The runtime review switch (--no-verify is only its boot state): closing it
	// cancels live reviews and hides the unverified-text gate; opening it re-arms
	// the pending candidates and the suspended fixed phases.
	mux.HandleFunc("POST /api/verifier/disable", s.verifierDisable)
	mux.HandleFunc("POST /api/verifier/enable", s.verifierEnable)
	mux.HandleFunc("POST /api/facts/{id}/review", s.reviewFact)
	// Withdrawal is a state change, not a delete: the authoring direction or the
	// operator says the record should not stand, and the ledger keeps it for
	// audit. See fact_withdraw.go for the actor rules.
	mux.HandleFunc("POST /api/facts/{id}/withdraw", s.withdrawFact)
	mux.HandleFunc("GET /api/reviews", s.listReviews)
	mux.HandleFunc("POST /api/reports", s.createReport)
	mux.HandleFunc("GET /api/reports", s.listReports)
	mux.HandleFunc("GET /api/reports/precheck", s.reportPrecheck)
	mux.HandleFunc("GET /api/reports/{id}", s.getReport)
	mux.HandleFunc("GET /api/reports/{id}/file", s.serveReportFile)
	mux.HandleFunc("POST /api/traffic/import", s.importTraffic)
	mux.HandleFunc("POST /api/assets/import", s.importAssets)
	mux.HandleFunc("GET /api/pocs", s.listPOCs)
	mux.HandleFunc("POST /api/pocs/import", s.importPOCs)
	mux.HandleFunc("GET /api/assets", s.listAssets)
	mux.HandleFunc("GET /api/assets/records", s.listAssetRecords)
	mux.HandleFunc("POST /api/bundles/export", s.exportBundle)
	mux.HandleFunc("POST /api/bundles/import", s.importBundle)
	mux.HandleFunc("GET /api/bundles/{name}", s.getBundle)
	mux.HandleFunc("GET /api/evidence", s.listEvidence)
	mux.HandleFunc("GET /api/evidence/{id}", s.serveEvidence)
	mux.HandleFunc("GET /api/goals", s.listGoals)
	mux.HandleFunc("POST /api/goals", s.createGoal)
	mux.HandleFunc("POST /api/goals/{id}/update", s.updateGoal)
	mux.HandleFunc("POST /api/goals/{id}/close", s.closeGoal)
	mux.HandleFunc("POST /api/goals/{id}/met", s.pinGoal)
	mux.HandleFunc("POST /api/goals/{id}/complete", s.completeGoal)
	mux.HandleFunc("POST /api/goals/{id}/reopen", s.reopenGoal)
	mux.HandleFunc("GET /api/documents", s.listDocuments)
	mux.HandleFunc("GET /api/documents/{kind}/history", s.documentHistory)
	mux.HandleFunc("PUT /api/documents/{kind}", s.saveDocument)
	mux.HandleFunc("GET /api/state", s.getState)
	mux.HandleFunc("PUT /api/state", s.putState)
	mux.HandleFunc("POST /api/decide", s.triggerDecide)
	mux.HandleFunc("POST /api/decider/pause", s.deciderPause)
	mux.HandleFunc("POST /api/decider/resume", s.deciderResume)
	mux.HandleFunc("POST /api/project/pause", s.projectPause)
	mux.HandleFunc("POST /api/project/resume", s.projectResume)
	// The commander's own global stop request. It is the one /api/project/ route
	// the harness surface may reach (cmd/zrcin auth.agentRouteHidden): a round that
	// judged the engagement should stop must be able to say so globally, instead
	// of parking a direction and letting the wake loop spend another round on the
	// parked result. Resume and append-goal stay operator-only.
	mux.HandleFunc("POST /api/project/standby", s.projectStandby)
	mux.HandleFunc("POST /api/project/closeout", s.projectCloseout)
	// Append a new independent goal and, if the project was parked in a resumable
	// stand-by, re-arm it. It sits under /api/project/ on purpose: that segment is
	// operator-only on the harness surface (cmd/zrcin auth.agentRouteHidden), so an
	// agent token cannot append goals or resume the project; the participant mux
	// never registers it either.
	mux.HandleFunc("POST /api/project/append-goal", s.appendGoalAndContinue)
	mux.HandleFunc("GET /api/decisions", s.listDecisions)
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/messages", s.listMessages)
	mux.HandleFunc("GET /api/operator-messages", s.operatorMessages)
	mux.HandleFunc("POST /api/operator-messages/seen", s.operatorMessagesSeen)
	// Operator scope is the operator's own append-only record (authorization /
	// stop) plus the read-only consumption observation; both are operator-only
	// and answered 404 to any harness session (new file operator_scope.go).
	// Operator-only read surfaces with no console client: the new console never
	// calls them (web/src mentions operator-scope only in a comment; history has
	// no call for operator-consumption), they exist for a direct operator/curl
	// read. Kept deliberately — see sweep-2. If a console screen is ever added,
	// drop this note.
	mux.HandleFunc("GET /api/operator-scope", s.operatorScopeList)
	mux.HandleFunc("POST /api/operator-scope", s.operatorScopeCreate)
	mux.HandleFunc("GET /api/operator-consumption", s.operatorConsumption)
	mux.HandleFunc("POST /api/messages", s.agentSendMessage)
	mux.HandleFunc("POST /api/messages/{id}/retract", s.retractMessage)
	mux.HandleFunc("GET /api/participants", s.listParticipants)
	// Peer policy: the operator's message-authorization rules (default open,
	// allowlist restricts who may message whom). Participant reads are on the
	// agent mux; this is the console's edit/read surface.
	mux.HandleFunc("GET /api/peers", s.getPeerPolicy)
	mux.HandleFunc("PUT /api/peers", s.updatePeerPolicy)
	mux.HandleFunc("GET /api/premise-policy", s.getPremisePolicy)
	// Spawn/workers/kill for the operator. Same implementation as the participant
	// routes (workers.go), operator identity and whole-project visibility.
	mux.HandleFunc("POST /api/spawn", s.consoleSpawn)
	mux.HandleFunc("GET /api/workers", s.consoleWorkers)
	mux.HandleFunc("POST /api/workers/{id}/kill", s.consoleKillWorker)
	mux.HandleFunc("GET /api/runs/{id}/events", s.runEvents)
	mux.HandleFunc("GET /api/runs/{id}/context", s.runContext)
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.cancelRun)
	mux.HandleFunc("POST /api/runs/{id}/warnings/dismiss", s.dismissRunWarnings)
	mux.HandleFunc("GET /api/board", s.getBoardWithOperatorScope)
	mux.HandleFunc("GET /api/tools", s.listTools)
	mux.HandleFunc("GET /api/tools/runtime", s.listRuntimeTools)
	// Read-only isolation boundary: what this deployment does not enforce
	// (process/network isolation on Windows and Linux alike). See P0-6.
	s.registerPlatformBoundary(mux)
	s.registerActions(mux)
	s.registerResourceRoutes(mux)
	s.registerFocusRoutes(mux)
	s.registerCommunicationRoutes(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "executor": s.Exec.Name()})
	})
}
