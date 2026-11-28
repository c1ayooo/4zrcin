package aopexec

import (
	"testing"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// These tests pin the AOP executor's absorb as it behaves TODAY, scenario by
// scenario, across every absorb-relevant Result field — the counterpart to
// internal/executor's process-path characterization tests. Where the two
// absorbs deliberately diverge (per-call usage events accumulate here, while
// the process path keeps only the first), the test name or comment says so:
// that behavior is the current state being pinned, not a wish.

// checkResult pins every absorb-relevant Result field. A field the scenario
// does not exercise must hold its zero value, so a refactor cannot move it
// unobserved.
func checkResult(t *testing.T, name string, res, want executor.Result) {
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

// Turn end carrying the complete usage payload: total, per-call breakdown and
// request counters all land on the result, and an all-reported turn is known.
func TestAOPPathTurnEndWithFullUsage(t *testing.T) {
	var res executor.Result
	absorb(&res, executor.Event{
		Kind: executor.KindTurnEnd, OK: executor.BoolPtr(true), StopReason: "completed",
		Tokens: 360, InputTokens: 300, OutputTokens: 60,
		CachedInputTokens: 120, CacheWriteTokens: 15,
		UsageRequests: 4, UsageMissing: 0,
		ContextTokens: 22000, ContextWindow: 200000,
	})
	checkResult(t, "turn end with full usage", res, executor.Result{
		Tokens: 360, InputTokens: 300, OutputTokens: 60,
		CachedInputTokens: 120, CacheWriteTokens: 15,
		UsageRequests: 4, UsageKnown: true,
		StopReason:    "completed",
		ContextTokens: 22000, ContextWindow: 200000, FinalContextTokens: 22000,
	})
}

// Two per-call usage events, then a turn end WITH the authoritative total,
// then a bare turn end without any usage. The totals overwrite the running
// sum once and must survive the bare repeat unchanged.
func TestAOPPathUsageThenTurnEndTotalsThenBareTurnEnd(t *testing.T) {
	var res executor.Result
	for range 2 {
		absorb(&res, executor.Event{Kind: executor.KindUsage, Tokens: 120, InputTokens: 100, OutputTokens: 20, CachedInputTokens: 40, CacheWriteTokens: 5, UsageRequests: 1})
	}
	absorb(&res, executor.Event{Kind: executor.KindTurnEnd, OK: executor.BoolPtr(true), StopReason: "completed", Tokens: 360, InputTokens: 300, OutputTokens: 60, CachedInputTokens: 120, CacheWriteTokens: 15, UsageRequests: 4, UsageMissing: 1})
	absorb(&res, executor.Event{Kind: executor.KindTurnEnd, StopReason: "completed"})
	checkResult(t, "usage + final total + bare end", res, executor.Result{
		Tokens: 360, InputTokens: 300, OutputTokens: 60,
		CachedInputTokens: 120, CacheWriteTokens: 15,
		UsageRequests: 4, UsageMissing: 1, UsageKnown: true,
		StopReason: "completed",
	})
}

// Two per-call usage events and a turn end without totals (a canceled turn):
// current state (full absorb): the per-call events ACCUMULATE and nothing
// erases them. This is the deliberate divergence from the process path, which
// keeps only the first usage total — pinned as such over there.
func TestAOPPathRepeatedUsageAccumulates(t *testing.T) {
	var res executor.Result
	for range 2 {
		absorb(&res, executor.Event{Kind: executor.KindUsage, Tokens: 120, InputTokens: 100, OutputTokens: 20, CachedInputTokens: 40, CacheWriteTokens: 5, UsageRequests: 1})
	}
	absorb(&res, executor.Event{Kind: executor.KindTurnEnd, StopReason: "canceled"})
	checkResult(t, "repeated usage accumulates (current state)", res, executor.Result{
		Tokens: 240, InputTokens: 200, OutputTokens: 40,
		CachedInputTokens: 80, CacheWriteTokens: 10,
		UsageRequests: 2, UsageKnown: true,
		StopReason: "canceled",
	})
}

// A turn whose calls all returned no usage stays unknown, not a measured 0;
// a partially reported turn is known with the missing count kept.
func TestAOPPathAllMissingAndPartialUsage(t *testing.T) {
	allMissing := executor.Result{}
	absorb(&allMissing, executor.Event{Kind: executor.KindUsage, UsageRequests: 1, UsageMissing: 1})
	absorb(&allMissing, executor.Event{Kind: executor.KindTurnEnd, OK: executor.BoolPtr(true), StopReason: "completed", UsageRequests: 2, UsageMissing: 2})
	checkResult(t, "all missing", allMissing, executor.Result{
		UsageRequests: 2, UsageMissing: 2,
		StopReason: "completed",
	})

	partial := executor.Result{}
	absorb(&partial, executor.Event{Kind: executor.KindTurnEnd, OK: executor.BoolPtr(true), StopReason: "completed", Tokens: 12, InputTokens: 10, OutputTokens: 2, UsageRequests: 3, UsageMissing: 1})
	checkResult(t, "partial missing", partial, executor.Result{
		Tokens: 12, InputTokens: 10, OutputTokens: 2,
		UsageRequests: 3, UsageMissing: 1, UsageKnown: true,
		StopReason: "completed",
	})
}

// An error event after a completed turn end keeps the turn's stop reason but
// marks the run failed; an error with no stop reason names the failure itself.
func TestAOPPathErrorWithAndWithoutStopReason(t *testing.T) {
	after := executor.Result{}
	absorb(&after, executor.Event{Kind: executor.KindTurnEnd, OK: executor.BoolPtr(true), StopReason: "completed"})
	absorb(&after, executor.Event{Kind: executor.KindError, Text: "boom"})
	checkResult(t, "error after turn end", after, executor.Result{
		StopReason: "completed",
		IsError:    true,
	})

	first := executor.Result{}
	absorb(&first, executor.Event{Kind: executor.KindError, Text: "boom"})
	checkResult(t, "error first", first, executor.Result{
		StopReason: "error",
		IsError:    true,
	})
}

// Compaction status events mark the turn compacted; while compaction is the
// last report the final context size stays unknown, and a later usage event
// reports the post-compaction size as final while the peak is kept.
func TestAOPPathCompactionAndFinalContext(t *testing.T) {
	last := executor.Result{}
	absorb(&last, executor.Event{Kind: executor.KindUsage, ContextTokens: 110000, ContextWindow: 200000})
	absorb(&last, executor.Event{Kind: executor.KindStatus, State: "compact_end"})
	checkResult(t, "compaction last", last, executor.Result{
		LastState:     "compact_end",
		Compacted:     true,
		ContextTokens: 110000, ContextWindow: 200000,
	})

	later := executor.Result{}
	absorb(&later, executor.Event{Kind: executor.KindUsage, ContextTokens: 110000, ContextWindow: 200000})
	absorb(&later, executor.Event{Kind: executor.KindStatus, State: "compact_start"})
	absorb(&later, executor.Event{Kind: executor.KindStatus, State: "compact_end"})
	absorb(&later, executor.Event{Kind: executor.KindUsage, ContextTokens: 20000})
	checkResult(t, "compaction then usage", later, executor.Result{
		LastState:     "compact_end",
		Compacted:     true,
		ContextTokens: 110000, ContextWindow: 200000, FinalContextTokens: 20000,
	})
}

// An assistant message becomes the run's text; a user message or an assistant
// message without text must not.
func TestAOPPathAssistantMessages(t *testing.T) {
	withText := executor.Result{}
	absorb(&withText, executor.Event{Kind: executor.KindMessage, Role: "user", Text: "干活"})
	absorb(&withText, executor.Event{Kind: executor.KindMessage, Role: "assistant", Text: "做完了"})
	checkResult(t, "assistant text", withText, executor.Result{Text: "做完了"})

	empty := executor.Result{}
	absorb(&empty, executor.Event{Kind: executor.KindMessage, Role: "assistant", Text: ""})
	checkResult(t, "assistant without text", empty, executor.Result{})
}

// Judge usage has two honest sources and both must land in the judge buckets
// without touching a single run-level fact (not even the context preamble):
// an event whose Emitter JudgeOf recognises, and a hand-built event carrying
// only kind=judge_usage plus the judge identity.
func TestAOPPathJudgeUsageFromEmitterAndFromKind(t *testing.T) {
	byEmitter := executor.Result{}
	absorb(&byEmitter, executor.Event{
		Kind: executor.KindUsage, Emitter: executor.EmitterGuardrail,
		Tokens: 30, InputTokens: 20, OutputTokens: 10, ContextTokens: 999999,
	})
	checkResult(t, "judge via emitter", byEmitter, executor.Result{
		JudgeJEV: executor.JudgeUsage{Tokens: 30, InputTokens: 20, OutputTokens: 10, Calls: 1},
	})

	byKind := executor.Result{}
	absorb(&byKind, executor.Event{
		Kind: executor.KindJudgeUsage, Judge: executor.JudgeLLM,
		Total: 6, Input: 4, Output: 2,
	})
	checkResult(t, "judge via kind+judge", byKind, executor.Result{
		JudgeLLM: executor.JudgeUsage{Tokens: 6, InputTokens: 4, OutputTokens: 2, Calls: 1},
	})
}
