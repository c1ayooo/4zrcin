package bench

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kksty/tianwu/internal/connectretry"
)

// Retryable says whether one failed platform call may be tried again. The rule
// the run uses is deliberately narrow:
//
//   - a connect failure (timeout, dropped SYN, 5xx/503) says nothing about the
//     request, so the same request is safe to repeat;
//   - a business refusal (404, 422, invalid_state without the slot marker) means
//     the platform understood us and said no — repeating it produces the same
//     answer and, in an unattended run, a spin.
//
// 409 duplicate is not a failure at all: KindOf already reports it as success.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if connectretry.IsTimeout(err) {
		return true
	}
	var f *Failure
	if errors.As(err, &f) {
		return f.Status >= 500
	}
	// A bare transport error (connection reset, EOF from a proxy) is connect
	// class too: no answer was received, so no answer can be contradicted.
	var opErr interface{ Timeout() bool }
	if errors.As(err, &opErr) {
		return true
	}
	return strings.Contains(err.Error(), "connection reset") ||
		strings.Contains(err.Error(), "connectex")
}

// Attempt is one line of the attempt log. Every try is recorded, success or
// failure, because "we retried" is otherwise unreconstructable: the platform's
// own view only shows the call that eventually landed.
type Attempt struct {
	At      time.Time `json:"at"`
	Op      string    `json:"op"`
	Attempt int       `json:"attempt"`
	OK      bool      `json:"ok"`
	Milli   int64     `json:"duration_ms"`
	Kind    string    `json:"kind,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// AttemptLog appends attempt lines and syncs each one. It is separate from the
// submitter log on purpose: mixing "we retried a GET" into the submitter log
// would make "how many submissions did we make" unanswerable.
type AttemptLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

func OpenAttemptLog(path string) (*AttemptLog, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &AttemptLog{path: path, f: f}, nil
}

func (l *AttemptLog) Append(a Attempt) error {
	if a.At.IsZero() {
		a.At = time.Now()
	}
	line, err := json.Marshal(a)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return err
	}
	return l.f.Sync()
}

func (l *AttemptLog) Path() string { return l.path }

func (l *AttemptLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// StepKind is one action the supervisor takes on a poll.
type StepKind string

const (
	StepEnsureGoal StepKind = "ensure_goal"
	StepStart      StepKind = "start_challenge"
	StepWriteScope StepKind = "write_scope"
	StepStop       StepKind = "stop"
)

// Step is one planned action, with everything the caller needs to perform it.
type Step struct {
	Kind   StepKind
	Code   string // challenge unique_code, or the stop reason
	Text   string // goal statement or scope text
	Addrs  []string
	Reason string
}

// State is what the supervisor remembers between polls. It is not a source of
// truth — the snapshot is — only a cache of what was already done, so a poll
// does not rewrite the same scope or re-create the same goal.
type State struct {
	GoalsEnsured map[string]bool
	ScopeAddrs   []string
	// Started records the challenges this run already asked the platform to
	// start. It is what separates "not up yet, we are waiting" from "we asked and
	// the platform still says stopped" — only the second one is a stop.
	Started map[string]bool
}

// Plan turns one snapshot into the next actions. It is pure: the caller performs
// them and records what happened.
func Plan(st State, snap Snapshot, snapshotFile string) []Step {
	var steps []Step
	if st.GoalsEnsured == nil {
		st.GoalsEnsured = map[string]bool{}
	}
	open := snap.Open()
	if len(snap.Challenges) == 0 {
		// An empty list is not "everything is done": nothing was measured. Say so
		// and stop rather than let the run continue under an empty plan.
		return []Step{{Kind: StepStop, Code: "no_challenges", Reason: "平台返回的题目列表为空"}}
	}
	if len(open) == 0 {
		return []Step{{Kind: StepStop, Code: "all_completed", Reason: "平台显示所有题目均已通关"}}
	}
	for _, ch := range open {
		if !st.GoalsEnsured[ch.UniqueCode] {
			steps = append(steps, Step{Kind: StepEnsureGoal, Code: ch.UniqueCode, Text: GoalStatement(ch)})
		}
	}
	waiting, unstarted := false, false
	for _, ch := range open {
		if ch.ContainerStatus == ContainerAvailable {
			continue
		}
		if ch.ContainerStatus == ContainerPending || ch.ContainerStatus == ContainerStopPending {
			// The platform is mid-transition; a second start would only add noise.
			waiting = true
			continue
		}
		if !st.Started[ch.UniqueCode] {
			unstarted = true
			steps = append(steps, Step{Kind: StepStart, Code: ch.UniqueCode})
		}
	}
	addrs := snap.AllowedAddrs()
	if len(addrs) == 0 {
		if waiting || unstarted {
			// Nothing legal to dispatch yet, but the run is making progress: the
			// caller starts what it can and polls again. The scope is left alone —
			// writing an empty one would read as "no restriction".
			return steps
		}
		// Every open challenge was asked to start and the platform still reports
		// nothing available. That is the case worth stopping for: a commander
		// reading the previous list would aim at containers that may already
		// belong to other challenges, and we have no new address to replace it.
		return []Step{{Kind: StepStop, Code: "no_available_address",
			Reason: "已请求启动但快照里仍没有任何 available 容器（" + snapshotFile + "）；不清空起点，也不继续派发"}}
	}
	if !sameAddrs(addrs, st.ScopeAddrs) {
		text, err := ScopeText(addrs)
		if err != nil {
			return []Step{{Kind: StepStop, Code: "scope_unrenderable", Reason: err.Error()}}
		}
		steps = append(steps, Step{Kind: StepWriteScope, Text: text, Addrs: addrs})
	}
	return steps
}

func sameAddrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}

// ProgressDiff describes a disagreement between what the run believed and what
// the platform says. The platform always wins; this type exists so the
// correction is logged with its size instead of happening silently.
type ProgressDiff struct {
	Code          string `json:"unique_code"`
	WasCorrect    int    `json:"was_correct_flags"`
	NowCorrect    int    `json:"now_correct_flags"`
	WasCumulative int    `json:"was_cumulative_score"`
	NowCumulative int    `json:"now_cumulative_score"`
	WasCompleted  bool   `json:"was_completed"`
	NowCompleted  bool   `json:"now_completed"`
}

// DiffProgress compares the previous view with the new snapshot. A non-empty
// result means the local counters were wrong and are being overwritten.
func DiffProgress(prev map[string]Challenge, snap Snapshot) []ProgressDiff {
	var out []ProgressDiff
	for _, ch := range snap.Challenges {
		old, ok := prev[ch.UniqueCode]
		if !ok {
			continue
		}
		if old.CorrectFlagCount != ch.CorrectFlagCount || old.IsCompleted != ch.IsCompleted {
			out = append(out, ProgressDiff{
				Code: ch.UniqueCode, WasCorrect: old.CorrectFlagCount, NowCorrect: ch.CorrectFlagCount,
				WasCompleted: old.IsCompleted, NowCompleted: ch.IsCompleted,
			})
		}
	}
	return out
}

// DigestSnapshot indexes a snapshot by challenge code for DiffProgress.
func DigestSnapshot(snap Snapshot) map[string]Challenge {
	out := make(map[string]Challenge, len(snap.Challenges))
	for _, ch := range snap.Challenges {
		out[ch.UniqueCode] = ch
	}
	return out
}
