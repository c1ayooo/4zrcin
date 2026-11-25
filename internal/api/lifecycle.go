package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

func (s *Server) CancelProjectWorkWithCause(cause, reason string) int {
	return s.Lifecycle.CancelProjectWorkWithCause(cause, reason)
}

// CancelProjectWorkExcept is the commander-standby variant: the run that asked
// for the stop is excluded, because it must finish its answer and its own
// delivery checkpoint. See Lifecycle.CancelProjectWorkExcept.
func (s *Server) CancelProjectWorkExcept(cause, reason, skipRunID string) int {
	if s.Lifecycle == nil {
		return 0
	}
	return s.Lifecycle.CancelProjectWorkExcept(cause, reason, skipRunID)
}

// CancelReviewsWithCause stops the live reviews only; project work is untouched.
func (s *Server) CancelReviewsWithCause(cause, reason string) int {
	return s.Lifecycle.CancelReviewsWithCause(cause, reason)
}

// TrackPurposeRun hands one report run to the control plane. The returned func
// unregisters it and reports the structured cancel cause ("" when nobody
// stopped it); report.Generate calls it exactly once, after the executor
// returns and before the run row is settled. Verifier runs use TrackReviewRun:
// reviews are decoupled from the project pause (D137).
func (s *Server) TrackPurposeRun(runID string, cancel context.CancelFunc) func() string {
	return s.Lifecycle.TrackPurposeRun(runID, cancel)
}

// TrackReviewRun hands one verifier run to the control plane. It is the review
// half of TrackPurposeRun, with one deliberate difference: the project pause
// does not cancel it. An operator may re-prove a finding while the engagement
// is paused; only delete/shutdown, or the explicit review switch, stop it
// (D137). Registering under reviewRuns is also what keeps CancelAllWithCause
// (shutdown/delete) complete while CancelProjectWorkWithCause stays selective.
func (s *Server) TrackReviewRun(runID string, cancel context.CancelFunc) func() string {
	return s.Lifecycle.TrackReviewRun(runID, cancel)
}

// WaitIdle waits for every live run of this project, including purpose/review
// runs and in-flight browser commands. The registry read lives in the lifecycle
// service; Recover is the only thing that settles a row left running by an
// earlier process.
func (s *Server) WaitIdle(d time.Duration) bool {
	return s.Lifecycle.WaitIdle(d)
}

// runEvents returns the tail of a run's JSONL log.
//
// The id comes from the URL and is used as one file-name segment: a %2F in the
// path is unescaped by net/http before PathValue, so without the check
// `/api/runs/..%2F..%2Fx/events` would read any `*.jsonl` on the host. Run ids
// are opaque generated identifiers; nothing else has a journal.
func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isPlainID(id) {
		writeErr(w, 404, "no log for run "+id)
		return
	}
	// Support runs never exist for a commander/executor session. harnessSession
	// covers both the participant identity and the commander's marked project
	// request, so neither credential can confirm a support run exists. A review
	// credential is not a harness session, but it is held by the same kind of
	// independent run: without the reviewer marker a reviewer could read the
	// previous review's journal, which is exactly the independence this role
	// exists to keep. The intent narrowing below stays session-only — a reviewer
	// has no intent, and applying it would 403 every log it is allowed to read.
	if harnessSession(r) || IsReviewerSurface(r.Context()) {
		run, ok := s.Store.GetRun(id)
		if !ok || supportPurpose(run.Purpose) {
			writeErr(w, 404, "no log for run "+id)
			return
		}
		if ident, agent := identityOf(r); agent && ident.IntentID != run.IntentID {
			// The journal is a direction's full execution record; a participant
			// reads its own. The commander keeps the project-wide view.
			writeErr(w, 403, "只能读取自己意图的事件日志；其他意图通过事实与证据读取")
			return
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	events, total, err := readRunEventWindow(s.DataDir, id, limit, r.URL.Query().Get("include_input") == "true")
	if err != nil {
		if os.IsNotExist(err) {
			// A run without a journal is not an HTTP bug: verifier/report runs
			// started before D138 never wrote one, and dispatch closes the
			// journal when a run settles. Say which case this is instead of
			// leaking the raw "no log for run r_…" English phrase.
			writeErr(w, 404, "这次运行没有事件日志：它没有写 runs/"+id+".jsonl。复核/报告运行在升级前不写日志，升级后新运行会写；已结束的运行日志已随运行关闭。")
			return
		}
		writeErr(w, 500, "读取运行日志失败："+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"events": events, "total": total})
}

// readRunEventTail returns the last `lines` non-empty JSONL events of one run's
// log plus the total number of events it holds. It is the single reader for
// both the console's `GET /api/runs/{id}/events` and the agent's
// `GET /agent/<project>/api/runs/{id}/events/tail?lines=N`: two copies of "read
// the end of a file" would drift on the same data the whole observability layer
// depends on (agent.md:26).
//
// The caller owns id validation: `isPlainID` is the path-traversal guard, and
// the run's project scope comes from the Store instance, not from this helper.
func readRunEventTail(dataDir, runID string, lines int) ([]json.RawMessage, int, error) {
	return readRunEventWindow(dataDir, runID, lines, false)
}

// Keep the submitted input visible even after it falls outside the recent tail.
// The operator can inspect exactly what 4zrcin submitted, without rebuilding a
// prompt from current project state or pretending to expose host-only history.
//
// The file is streamed, not slurped: a long engagement's journal can reach
// hundreds of megabytes, and the caller only wants its tail. The ring keeps
// at most `lines` events, so one request's memory is bounded by the answer it
// already returns instead of by the whole file.
func readRunEventWindow(dataDir, runID string, lines int, includeInput bool) ([]json.RawMessage, int, error) {
	path := filepath.Join(dataDir, "runs", runID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	type keptEvent struct {
		position int
		raw      json.RawMessage
	}
	keepAll := lines <= 0
	var tail []keptEvent
	start := 0
	var extra []keptEvent
	total := 0
	reader := bufio.NewReaderSize(f, 1<<20)
	firstLine := true
	for {
		line, readErr := reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if firstLine {
				firstLine = false
				// One writer creates the file, so the first line is its format:
				// refuse the whole file rather than return rows whose meaning
				// this build would have to guess.
				if v := journalFileVersion([]byte(trimmed)); v != executor.JournalSchemaVersion {
					return nil, 0, fmt.Errorf("%w（文件 v%d，当前 v%d）", errJournalVersion, v, executor.JournalSchemaVersion)
				}
			}
			total++
			event := keptEvent{position: total, raw: json.RawMessage(trimmed)}
			switch {
			case keepAll:
				tail = append(tail, event)
			case len(tail) < lines:
				tail = append(tail, event)
			default:
				// Circular buffer: the oldest event gives way to the newest.
				tail[start] = event
				start = (start + 1) % lines
			}
			if includeInput && !keepAll {
				var probe struct {
					Kind string `json:"kind"`
				}
				if json.Unmarshal([]byte(trimmed), &probe) == nil && (probe.Kind == "input" || probe.Kind == "prompt") {
					// An input event outside the tail is retained explicitly: it is
					// the one line the operator must always be able to audit.
					extra = append(extra, event)
				}
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, 0, readErr
			}
			break
		}
	}
	ordered := tail
	if !keepAll && start > 0 {
		ordered = make([]keptEvent, 0, len(tail))
		ordered = append(ordered, tail[start:]...)
		ordered = append(ordered, tail[:start]...)
	}
	byPosition := make(map[int]json.RawMessage, len(ordered)+len(extra))
	positions := make([]int, 0, len(ordered)+len(extra))
	for _, event := range append(ordered, extra...) {
		if _, seen := byPosition[event.position]; seen {
			continue
		}
		byPosition[event.position] = event.raw
		positions = append(positions, event.position)
	}
	sort.Ints(positions)
	out := make([]json.RawMessage, 0, len(positions))
	for _, position := range positions {
		out = append(out, byPosition[position])
	}
	return out, total, nil
}
