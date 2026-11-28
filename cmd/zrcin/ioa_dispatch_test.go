package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/api"
	"github.com/c1ayooo/4zrcin/internal/decider"
	"github.com/c1ayooo/4zrcin/internal/ioa"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
)

// Characterization: the native dispatch path onNativeIOA only schedules on a
// protocol message addressed to a known commander session. Lifecycle events
// with a target, messages without a target session and rejected records are
// observations only. Pinned before the envelope swap; the guard keeps its
// meaning after the swap (only the type names change).
func TestNativeDispatchOnlySchedulesAddressedMessages(t *testing.T) {
	root := t.TempDir()
	mgr, err := project.Open(filepath.Join(root, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("dispatch", "")
	if err != nil {
		t.Fatal(err)
	}
	// The commander round only runs when the board has open work; an idle
	// project sleeps through every wake. Same setup as the scheduling test.
	if _, err := p.Store.CreateIntent(model.Intent{Title: "open"}); err != nil {
		t.Fatal(err)
	}
	srv := api.New(p.Store, nil, root, p.CaseDir)
	dir := filepath.Join(srv.CommanderDir(), ".zrcin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aop-session.json"), []byte(`{"session":"commander-guard"}`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := make(chan struct{}, 4)
	r := decider.NewRunner(p.Store, nil, "")
	r.ModelName, r.Debounce, r.Heartbeat = "fixture", time.Millisecond, time.Hour
	r.Commander = func(context.Context, string, bool) (decider.RoundResult, error) {
		calls <- struct{}{}
		return decider.RoundResult{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	h := &hub{kits: map[string]*kit{p.ID: {proj: p, srv: srv, dec: r}}}

	quiet := func(t *testing.T, why string) {
		t.Helper()
		select {
		case <-calls:
			t.Fatalf("%s scheduled a model turn", why)
		case <-time.After(20 * time.Millisecond):
		}
	}

	// Control: an addressed message does schedule. Proves the harness is wired.
	addressed := ioa.Record{Seq: 1, IOAMessageID: "control", Envelope: &ioa.Envelope{
		Version: 1, Event: ioa.EventMessage, ID: "e0", Target: ioa.Address{SessionID: "commander-guard"}}}
	if err := h.onNativeIOA(addressed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("addressed message did not schedule")
	}

	// A lifecycle event aimed at the same session is not a dispatch trigger.
	delegate := ioa.Record{Seq: 2, IOAMessageID: "delegate", Envelope: &ioa.Envelope{
		Version: 1, Event: ioa.EventSubagentDelegate, ID: "e1", CallID: "call-1",
		Target: ioa.Address{SessionID: "commander-guard"}, Agent: &ioa.Agent{Name: "scout"}}}
	if err := h.onNativeIOA(delegate); err != nil {
		t.Fatal(err)
	}
	quiet(t, "delegate with target")

	// A message without a target session is not a dispatch trigger.
	unaddressed := ioa.Record{Seq: 3, IOAMessageID: "unaddressed", Envelope: &ioa.Envelope{
		Version: 1, Event: ioa.EventMessage, ID: "e2", Source: ioa.Address{SessionID: "someone"}}}
	if err := h.onNativeIOA(unaddressed); err != nil {
		t.Fatal(err)
	}
	quiet(t, "message without target")

	// A rejected record is never a dispatch trigger.
	rejected := ioa.Record{Seq: 4, IOAMessageID: "rejected", Error: "protocol field chain cannot be null"}
	if err := h.onNativeIOA(rejected); err != nil {
		t.Fatal(err)
	}
	quiet(t, "rejected record")
}
