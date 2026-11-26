package aopexec

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// The decoder must recognise the harness announcement and refuse near misses:
// another code, another emitter, a session it does not belong to, and a
// message without the metadata sidecar.
func TestDecodeSessionContextReset(t *testing.T) {
	hit := contextResetEvent("s1")
	if !isSessionContextReset(hit, "s1") {
		t.Fatal("the reset announcement was not recognised")
	}
	if isSessionContextReset(hit, "s2") {
		t.Fatal("an event for another session matched")
	}
	other := contextResetEvent("s1")
	other.Extensions[0].Value = mustResetCode("session_started")
	if isSessionContextReset(other, "s1") {
		t.Fatal("a different metadata code matched")
	}
	noExt := contextResetEvent("s1")
	noExt.Extensions = nil
	if isSessionContextReset(noExt, "s1") {
		t.Fatal("a system message without the sidecar matched")
	}
	wrongEmitter := contextResetEvent("s1")
	wrongEmitter.Emitter = "cyber.scan"
	if isSessionContextReset(wrongEmitter, "s1") {
		t.Fatal("an event from another emitter matched")
	}
}

// The run-level behaviour (docs/16 §8): the announcement passes through a
// live turn as a journaled warning, and the resume pointer is dropped so the
// next dispatch rebuilds the session from a full brief instead of trusting
// the marker.
func TestRunJournalsContextResetAndDropsTheResumePointer(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{sessions: map[string]bool{}, hostDir: filepath.Dir(work), contextReset: true}
	ex, done := newFake(t, h)
	defer done()

	var events []executor.Event
	res, err := ex.Run(context.Background(), executor.Request{
		IntentID: "i1", Prompt: "枚举攻击面", WorkDir: work,
	}, func(ev executor.Event) { events = append(events, ev) })
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "completed" {
		t.Fatalf("the announcement must not fail the round: %+v", res)
	}
	var warned bool
	for _, ev := range events {
		if ev.Kind == executor.KindSession && strings.Contains(ev.Text, "会话上下文已重置") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no reset warning in the journal: %+v", events)
	}
	if CanResume(work) {
		t.Fatal("the resume pointer survived the reset; the next run would trust an empty session")
	}
}

func mustResetCode(code string) []byte {
	var raw []byte
	raw = protowireAppendTag(raw)
	raw = protowireAppendBytes(raw, []byte(code))
	return raw
}
