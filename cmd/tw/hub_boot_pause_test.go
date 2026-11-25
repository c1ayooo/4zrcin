package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/node"
	"github.com/c1ayooo/4zrcin/internal/project"
)

// A restart is an operator action, not permission to resume spending. Every
// saved project must come up paused even when its manifest said running, and
// the pause has to be persisted so a second restart cannot silently resume the
// engagement either. The runtime gate must agree with the manifest: an
// automatic commander round can start before an operator sees the console.
func TestStartPausesEverySavedProject(t *testing.T) {
	dir := t.TempDir()
	mgr, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("acme", "acme.example")
	if err != nil {
		t.Fatal(err)
	}
	if p.IsPaused() {
		t.Fatal("precondition: a new project starts running")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hub{cfg: hubConfig{Nodes: &node.Registry{}}, mgr: mgr, ctx: ctx, kits: map[string]*kit{}}
	h.start()

	// Boot paused both the persisted state and the live runtime.
	if !p.IsPaused() {
		t.Fatal("boot did not pause the project manifest")
	}
	reopened, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Get(p.ID).IsPaused() {
		t.Fatal("boot pause was not persisted")
	}
	k := h.kits[p.ID]
	if k == nil || k.dec == nil {
		t.Fatal("boot did not attach a decider")
	}
	if !k.dec.Paused() {
		t.Fatal("commander is not paused after restart")
	}
	if reason := k.dec.Status().PausedReason; !strings.Contains(reason, "重启后") {
		t.Fatalf("paused reason = %q, want the restart explanation", reason)
	}
	// The dispatch gate is the second half of the pause: a round that somehow
	// slips past the decider must still be refused.
	rec := httptest.NewRecorder()
	k.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/decide", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("paused project accepted a decision round: %d %s", rec.Code, rec.Body.String())
	}

	// The operator explicitly resumes. A later restart must pause again, so
	// "paused after restart" is the start condition, not a one-time flag.
	if err := p.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	next, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	nextCtx, nextCancel := context.WithCancel(context.Background())
	defer nextCancel()
	h2 := &hub{cfg: hubConfig{Nodes: &node.Registry{}}, mgr: next, ctx: nextCtx, kits: map[string]*kit{}}
	h2.start()
	if !next.Get(p.ID).IsPaused() {
		t.Fatal("a second restart did not pause the project again")
	}
}

// A commander-requested stop is a pause with a cause. A control-plane restart
// must not relabel it as an operator pause: the console's badge and the
// append-and-continue contract both switch on the persisted machine code, and
// "who stopped the engagement" is exactly the question the operator asks first.
func TestCommanderStandbyPauseKindSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	mgr, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("acme", "acme.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetPausedWithKind(true, project.PauseKindCommanderStandby); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hub{cfg: hubConfig{Nodes: &node.Registry{}}, mgr: mgr, ctx: ctx, kits: map[string]*kit{}}
	h.start()

	k := h.kits[p.ID]
	if k == nil || k.dec == nil {
		t.Fatal("boot did not attach the paused project")
	}
	if !k.dec.Paused() {
		t.Fatal("commander is not paused after restart")
	}
	// The runtime cause must be restored from the manifest, not guessed from prose.
	rec := httptest.NewRecorder()
	k.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/board", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("board = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"project_pause_kind":"commander_standby"`) {
		t.Fatalf("board lost the commander pause kind: %s", rec.Body.String())
	}
	// The boot pause write must not overwrite the persisted cause either.
	reopened, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get(p.ID).PauseKindOf(); got != project.PauseKindCommanderStandby {
		t.Fatalf("persisted pause kind = %q after boot, want %q", got, project.PauseKindCommanderStandby)
	}
}

// A restart is its own pause cause: the operator did not touch the switch, and
// the console must not say they did. The cause has to be persisted (so the next
// boot keeps it) and reported by the board (so the badge and the resume button
// agree with the manifest).
func TestBootPauseRecordsTheRestartCause(t *testing.T) {
	dir := t.TempDir()
	mgr, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("acme", "acme.example")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hub{cfg: hubConfig{Nodes: &node.Registry{}}, mgr: mgr, ctx: ctx, kits: map[string]*kit{}}
	h.start()

	reopened, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get(p.ID).PauseKindOf(); got != project.PauseKindRestart {
		t.Fatalf("persisted pause kind = %q after a boot pause, want %q", got, project.PauseKindRestart)
	}
	k := h.kits[p.ID]
	if k == nil || k.dec == nil {
		t.Fatal("boot did not attach a decider")
	}
	body := boardBody(t, k)
	if !strings.Contains(body, `"project_pause_kind":"restart_pause"`) {
		t.Fatalf("board does not report the restart cause: %s", body)
	}
	if strings.Contains(body, `"project_pause_kind":"manual_pause"`) {
		t.Fatalf("a restart was reported as the operator's pause: %s", body)
	}
	if !strings.Contains(body, `"reason_code":"paused_restart"`) {
		t.Fatalf("resume action does not name the restart cause: %s", body)
	}
	// The console's first screen shows this prose, not the machine code, so it has
	// to be the sentence for the cause that actually applies.
	if !strings.Contains(body, `"reason":"控制面重启后项目默认暂停：确认现场后点「继续项目」"`) {
		t.Fatalf("board does not say why the restart paused it: %s", body)
	}

	// A second restart keeps the cause instead of falling back to the default.
	nextCtx, nextCancel := context.WithCancel(context.Background())
	defer nextCancel()
	h2 := &hub{cfg: hubConfig{Nodes: &node.Registry{}}, mgr: reopened, ctx: nextCtx, kits: map[string]*kit{}}
	h2.start()
	again, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get(p.ID).PauseKindOf(); got != project.PauseKindRestart {
		t.Fatalf("pause kind after a second restart = %q, want %q", got, project.PauseKindRestart)
	}
}

// A manifest paused before the cause codes existed carries no cause, and code
// cannot recover one. It must say "unrecorded" rather than claim the operator
// paused the project: an invented author is the one answer that can be false.
func TestLegacyPauseWithoutCauseIsNotReportedAsManual(t *testing.T) {
	dir := t.TempDir()
	mgr, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("acme", "acme.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetPaused(true); err != nil { // keeps the empty cause
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hub{cfg: hubConfig{Nodes: &node.Registry{}}, mgr: mgr, ctx: ctx, kits: map[string]*kit{}}
	h.start()

	reopened, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get(p.ID).PauseKindOf(); got != "" {
		t.Fatalf("boot rewrote a legacy pause cause to %q", got)
	}
	body := boardBody(t, h.kits[p.ID])
	if !strings.Contains(body, `"project_pause_kind":"unrecorded"`) {
		t.Fatalf("a cause-less pause was not reported as unrecorded: %s", body)
	}
	if strings.Contains(body, `"project_pause_kind":"manual_pause"`) {
		t.Fatalf("a cause-less pause was attributed to the operator: %s", body)
	}
	// Same rule for the prose the first screen shows: an unknown cause must not be
	// explained as a restart or as an operator action.
	if strings.Contains(body, "重启后项目默认暂停") {
		t.Fatalf("a cause-less pause was explained as a restart: %s", body)
	}
	if !strings.Contains(body, `"reason":"暂停原因未记录：确认现场后点「继续项目」"`) {
		t.Fatalf("the first screen does not say the cause is unrecorded: %s", body)
	}
}

func boardBody(t *testing.T, k *kit) string {
	t.Helper()
	rec := httptest.NewRecorder()
	k.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/board", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("board = %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// Runtime attachment (creating a project, re-opening it after edits) keeps the
// manifest state: only the boot path forces a pause. Otherwise a newly created
// project could never start without a confusing resume step.
func TestRuntimeAttachKeepsManifestPauseState(t *testing.T) {
	mgr, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("acme", "acme.example")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hub{cfg: hubConfig{Nodes: &node.Registry{}}, mgr: mgr, ctx: ctx, kits: map[string]*kit{}}

	k := h.attach(p)
	if k == nil || k.dec == nil {
		t.Fatal("runtime attach did not build a decider")
	}
	if k.dec.Paused() {
		t.Fatal("runtime attach paused a running manifest")
	}
}
