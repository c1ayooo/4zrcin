package executor

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// These tests pin the process-mode path (runProcess → parseEvent → executor's
// shared Absorb) scenario by scenario, across every absorb-relevant Result
// field. They were written before the r10 absorb merge to pin the then-current
// behavior; where the merge deliberately changed this path (per-call usage now
// accumulates instead of first-event-wins, and a real total marks the usage
// known), the test name or comment says so — the change is sanctioned by the
// r10/A ticket, everything else is pinned against silent drift.
//
// The process parser never produces per-call breakdowns, request counters,
// emitters or judge usage (process.go parseEvent reads only total_tokens and
// detail.context_tokens/context_window), so those fields are pinned at their
// current values (zero) and the judge scenarios live on the AOP path only.

// streamEvents runs the events through a real child process speaking
// stream-json on stdout, exactly the way the audit executor consumes a harness.
func streamEvents(t *testing.T, lines ...string) Result {
	t.Helper()
	var bin string
	var args []string
	if runtime.GOOS == "windows" {
		parts := make([]string, len(lines))
		for i, line := range lines {
			parts[i] = "echo " + line
		}
		bin, args = "cmd", []string{"/c", strings.Join(parts, " & ")}
	} else {
		// The lines are plain JSON (no quotes of their own beyond the JSON
		// double quotes, no % that printf would eat), so a single-quoted printf
		// format with \n separators streams one event per line.
		bin, args = "sh", []string{"-c", "printf '" + strings.Join(lines, `\n`) + `\n'`}
	}
	res, err := runProcess(context.Background(), bin, args, t.TempDir(), nil, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// checkResult pins every absorb-relevant Result field. A field the scenario
// does not exercise must hold its zero value, so a refactor cannot move it
// unobserved. Events/ExitCode/Output are runProcess's own bookkeeping and are
// asserted separately where they matter.
func checkResult(t *testing.T, name string, res, want Result) {
	t.Helper()
	if res.Tokens != want.Tokens ||
		res.InputTokens != want.InputTokens ||
		res.OutputTokens != want.OutputTokens ||
		res.CachedInputTokens != want.CachedInputTokens ||
		res.CacheWriteTokens != want.CacheWriteTokens ||
		res.UsageRequests != want.UsageRequests ||
		res.UsageMissing != want.UsageMissing ||
		res.UsageKnown != want.UsageKnown ||
		res.StopReason != want.StopReason ||
		res.IsError != want.IsError ||
		res.Text != want.Text ||
		res.LastState != want.LastState ||
		res.Compacted != want.Compacted ||
		res.ContextTokens != want.ContextTokens ||
		res.ContextWindow != want.ContextWindow ||
		res.FinalContextTokens != want.FinalContextTokens ||
		res.JudgeJEV != want.JudgeJEV ||
		res.JudgeLLM != want.JudgeLLM {
		t.Fatalf("%s:\n got %+v\nwant %+v", name, res, want)
	}
}

// Turn end carrying the complete usage payload. The wire has breakdown and
// request counters, but the process parser reads only the total, so the
// breakdown fields stay zero; a real total still marks the usage as known
// (this changed in the r10 absorb merge — the simple absorb left it false).
func TestProcessPathTurnEndWithFullUsage(t *testing.T) {
	res := streamEvents(t,
		`{"turn_ended":{"stop_reason":"completed","usage":{"total_tokens":"360","detail":{"context_tokens":"22000","context_window":"200000","input_tokens":"300","output_tokens":"60","cache_read":"120","cache_write":"15","requests":"4"}}}}`,
	)
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	checkResult(t, "turn end with full usage", res, Result{
		Tokens:             360,
		StopReason:         "completed",
		UsageKnown:         true,
		ContextTokens:      22000,
		ContextWindow:      200000,
		FinalContextTokens: 22000,
	})
}

// Two per-call usage events, then a turn end WITH a total, then a bare turn
// end without any usage. The final total must survive the bare repeat.
func TestProcessPathUsageThenTurnEndTotalsThenBareTurnEnd(t *testing.T) {
	res := streamEvents(t,
		`{"usage":{"total_tokens":"100","detail":{"context_tokens":"1000","context_window":"200000"}}}`,
		`{"usage":{"total_tokens":"50","detail":{"context_tokens":"1100"}}}`,
		`{"turn_ended":{"stop_reason":"completed","usage":{"total_tokens":"360","detail":{"context_tokens":"12000"}}}}`,
		`{"turn_ended":{"stop_reason":"completed"}}`,
	)
	checkResult(t, "usage + final total + bare end", res, Result{
		Tokens:             360,
		StopReason:         "completed",
		UsageKnown:         true,
		ContextTokens:      12000,
		ContextWindow:      200000,
		FinalContextTokens: 12000,
	})
}

// Two per-call usage events and a turn end that carries NO total (a canceled
// turn): the per-call totals accumulate. This changed in the r10 absorb
// merge — before it, only the first usage event's total was kept (the ticket
// sanctions the sum as more accurate accounting).
func TestProcessPathRepeatedUsageAccumulates(t *testing.T) {
	res := streamEvents(t,
		`{"usage":{"total_tokens":"100"}}`,
		`{"usage":{"total_tokens":"50"}}`,
		`{"turn_ended":{"stop_reason":"canceled"}}`,
	)
	checkResult(t, "repeated usage accumulates", res, Result{
		Tokens:     150,
		StopReason: "canceled",
		UsageKnown: true,
	})
}

// Usage events report no request counters on this path, so the
// all-missing / partial-missing scenarios cannot arise here: UsageRequests and
// UsageMissing are pinned at zero by the other tests in this file.

// An error event after a completed turn end keeps the turn's stop reason but
// marks the run failed.
func TestProcessPathErrorAfterTurnEnd(t *testing.T) {
	res := streamEvents(t,
		`{"turn_ended":{"stop_reason":"completed"}}`,
		`{"error":{"code":"boom","message":"provider exploded"}}`,
	)
	checkResult(t, "error after turn end", res, Result{
		StopReason: "completed",
		IsError:    true,
	})
}

// An error event with no stop reason yet names the failure itself.
func TestProcessPathErrorWithoutStopReason(t *testing.T) {
	res := streamEvents(t,
		`{"error":{"code":"boom","message":"provider exploded"}}`,
	)
	checkResult(t, "error first", res, Result{
		StopReason: "error",
		IsError:    true,
	})
}

// Compaction status events mark the turn compacted; a later usage event
// reports the post-compaction context size as final while the peak is kept.
func TestProcessPathCompactionThenLaterUsage(t *testing.T) {
	res := streamEvents(t,
		`{"usage":{"total_tokens":"700","detail":{"context_tokens":"110000","context_window":"200000"}}}`,
		`{"status":{"state":"compact_start"}}`,
		`{"status":{"state":"compact_end"}}`,
		`{"usage":{"total_tokens":"80","detail":{"context_tokens":"20000"}}}`,
	)
	checkResult(t, "compaction then usage", res, Result{
		// Per-call usage accumulates since the r10 absorb merge (700 + 80).
		Tokens:             780,
		LastState:          "compact_end",
		Compacted:          true,
		UsageKnown:         true,
		ContextTokens:      110000,
		ContextWindow:      200000,
		FinalContextTokens: 20000,
	})
}

// When compaction is the last thing the turn reported, the pre-compaction
// size is stale and final stays unknown.
func TestProcessPathCompactionLastDropsFinalContext(t *testing.T) {
	res := streamEvents(t,
		`{"usage":{"total_tokens":"700","detail":{"context_tokens":"110000"}}}`,
		`{"status":{"state":"compact_end"}}`,
	)
	checkResult(t, "compaction last", res, Result{
		Tokens:        700,
		LastState:     "compact_end",
		Compacted:     true,
		UsageKnown:    true,
		ContextTokens: 110000,
	})
}

// An assistant message becomes the run's text; a user message or an assistant
// message without text must not.
func TestProcessPathAssistantMessages(t *testing.T) {
	withText := streamEvents(t,
		`{"message":{"role":"user","content":[{"text":"干活"}]}}`,
		`{"message":{"role":"assistant","content":[{"text":"做完了"}]}}`,
	)
	checkResult(t, "assistant text", withText, Result{Text: "做完了"})

	empty := streamEvents(t,
		`{"message":{"role":"assistant","content":[]}}`,
	)
	checkResult(t, "assistant without text", empty, Result{})
}

// When the harness only streamed deltas, runProcess folds them into the one
// assistant message the ledger would otherwise never see, and absorb takes it.
func TestProcessPathFoldedDeltasBecomeOneMessage(t *testing.T) {
	res := streamEvents(t,
		`{"message_delta":{"text":"hel"}}`,
		`{"message_delta":{"text":"lo"}}`,
	)
	if res.Events != 1 {
		t.Fatalf("events = %d, want the single folded message", res.Events)
	}
	checkResult(t, "folded deltas", res, Result{Text: "hello"})
}

// The judge scenarios (emitter-recognised judge usage, kind=judge_usage with
// an explicit judge) have no process-mode producer: parseEvent sets neither
// Emitter nor Judge, so those live in internal/aopexec's characterization
// tests only.
