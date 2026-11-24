package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kksty/tianwu/internal/model"
)

// spawnBlockingWorker creates a peer worker owned by the fixture's source
// participant and leaves its run in flight, so a kill then a restart can be
// observed. Peer publish needs a recorded fact; the helper supplies one.
func spawnBlockingWorker(t *testing.T, f *agentFixture) spawnResp {
	t.Helper()
	f.srv.Exec = &blockingExec{}
	rec := f.call(t, f.srcID, http.MethodPost, "/api/spawn", peerSpawnBody(t, f, "restart-1", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("spawn: %d %s", rec.Code, rec.Body.String())
	}
	var resp spawnResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Started || resp.RunID == "" {
		t.Fatalf("spawn did not start: %+v", resp)
	}
	t.Cleanup(func() {
		f.srv.CancelAll()
		f.srv.WaitIdle(5 * time.Second)
		time.Sleep(150 * time.Millisecond)
	})
	return resp
}

func killAndWaitHeld(t *testing.T, f *agentFixture, intentID string) model.Intent {
	t.Helper()
	rec := f.call(t, f.srcID, http.MethodPost, "/api/workers/"+intentID+"/kill", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("kill: %d %s", rec.Code, rec.Body.String())
	}
	waitFor(t, 5*time.Second, func() bool {
		got, ok := f.p.Store.GetIntent(intentID)
		return ok && got.Status == model.StatusFailed && got.Hold
	})
	got, _ := f.p.Store.GetIntent(intentID)
	return got
}

// A parent may bring back a worker it killed: same identity, same brief, a
// recorded restart note, and the stop actor cleared by the new claim.
func TestAgentRestartWorkerOwnedAfterKill(t *testing.T) {
	f := newSpawnFixture(t)
	spawned := spawnBlockingWorker(t, f)
	held := killAndWaitHeld(t, f, spawned.Intent.ID)
	if held.HeldBy != f.srcID.ParticipantID {
		t.Fatalf("held_by = %q, want the killing parent %q", held.HeldBy, f.srcID.ParticipantID)
	}

	rec := f.call(t, f.srcID, http.MethodPost, "/api/workers/"+spawned.Intent.ID+"/restart",
		map[string]any{"note": "再看一次入口"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Restarted bool       `json:"restarted"`
		IntentID  string     `json:"intent_id"`
		RunID     string     `json:"run_id"`
		Worker    workerView `json:"worker"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Restarted || resp.IntentID != spawned.Intent.ID || resp.RunID == "" || resp.RunID == spawned.RunID {
		t.Fatalf("restart resp = %+v (old run %s)", resp, spawned.RunID)
	}
	waitFor(t, 5*time.Second, func() bool {
		got, ok := f.p.Store.GetIntent(spawned.Intent.ID)
		return ok && got.Status == model.StatusRunning && got.RunID == resp.RunID
	})
	got, _ := f.p.Store.GetIntent(spawned.Intent.ID)
	if got.Hold || got.HeldBy != "" || got.HoldReason != "" {
		t.Fatalf("restart did not clear the hold: %+v", got)
	}
	restartNote := false
	for _, n := range got.Notes {
		if n.Kind == model.IntentNoteRestart && n.Text == "再看一次入口" {
			restartNote = true
		}
	}
	if !restartNote {
		t.Fatalf("restart note missing: %+v", got.Notes)
	}
}

// The restart is ownership-scoped and the stop's actor decides release: an
// operator/commander/unknown stop is not the parent's to clear, and a worker
// that is still running or already settled is not restartable at all.
func TestAgentRestartWorkerRefusesForeignHoldsAndStates(t *testing.T) {
	f := newSpawnFixture(t)
	spawned := spawnBlockingWorker(t, f)

	// Still running: nothing to restart.
	rec := f.call(t, f.srcID, http.MethodPost, "/api/workers/"+spawned.Intent.ID+"/restart", map[string]any{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("running restart = %d %s", rec.Code, rec.Body.String())
	}

	killAndWaitHeld(t, f, spawned.Intent.ID)
	for _, actor := range []string{
		model.HoldActorOperator, model.HoldActorCommander, model.HoldActorUnknown, "",
	} {
		if _, err := f.p.Store.UpdateIntent(spawned.Intent.ID, func(i *model.Intent) {
			i.Status = model.StatusFailed
			i.Hold = true
			i.HeldBy = actor
			i.HoldReason = "previous stop"
		}); err != nil {
			t.Fatal(err)
		}
		rec := f.call(t, f.srcID, http.MethodPost, "/api/workers/"+spawned.Intent.ID+"/restart", map[string]any{})
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "worker_held") {
			t.Fatalf("held by %q: %d %s", actor, rec.Code, rec.Body.String())
		}
	}

	// A watchdog (automatic liveness) stop is releaseable by the owner.
	if _, err := f.p.Store.UpdateIntent(spawned.Intent.ID, func(i *model.Intent) {
		i.Status = model.StatusFailed
		i.Hold = true
		i.HeldBy = model.HoldActorWatchdog
	}); err != nil {
		t.Fatal(err)
	}
	rec = f.call(t, f.srcID, http.MethodPost, "/api/workers/"+spawned.Intent.ID+"/restart", map[string]any{})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("watchdog restart = %d %s", rec.Code, rec.Body.String())
	}

	// Someone else's direction is not this caller's to restart.
	other := f.target
	if _, err := f.p.Store.UpdateIntent(other.ID, func(i *model.Intent) {
		i.Status = model.StatusFailed
		i.Hold = false
	}); err != nil {
		t.Fatal(err)
	}
	rec = f.call(t, f.srcID, http.MethodPost, "/api/workers/"+other.ID+"/restart", map[string]any{})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "worker_forbidden") {
		t.Fatalf("foreign restart = %d %s", rec.Code, rec.Body.String())
	}

	// A direction cannot restart itself; retry/continue is its own path.
	rec = f.call(t, f.srcID, http.MethodPost, "/api/workers/"+f.src.ID+"/restart", map[string]any{})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "worker_self_restart") {
		t.Fatalf("self restart = %d %s", rec.Code, rec.Body.String())
	}
}

// A failed start must restore the exact pre-restart state: a plain failed
// worker must not gain an unknown-holder hold just because capacity or a
// blocked premise refused the new run.
func TestAgentRestartWorkerRollsBackWhenStartFails(t *testing.T) {
	f := newSpawnFixture(t)
	spawned := spawnBlockingWorker(t, f)
	held := killAndWaitHeld(t, f, spawned.Intent.ID)

	// A missing hard premise makes Start refuse the restart after the reserve.
	if _, err := f.p.Store.UpdateIntent(spawned.Intent.ID, func(i *model.Intent) {
		i.FromFactIDs = []string{"f_missing_premise"}
	}); err != nil {
		t.Fatal(err)
	}
	rec := f.call(t, f.srcID, http.MethodPost, "/api/workers/"+spawned.Intent.ID+"/restart", map[string]any{"note": "再试一次"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("blocked restart = %d %s", rec.Code, rec.Body.String())
	}
	got, _ := f.p.Store.GetIntent(spawned.Intent.ID)
	if got.Status != model.StatusFailed || !got.Hold || got.HeldBy != held.HeldBy || got.HoldReason != held.HoldReason {
		t.Fatalf("rollback = %+v, want failed+hold by %q", got, held.HeldBy)
	}
	found := false
	for _, n := range got.Notes {
		if n.Kind == model.IntentNoteRestart && strings.Contains(n.Text, "重启未启动") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed-start note missing: %+v", got.Notes)
	}
}

// A parent cannot seize an operator stop by killing a held pending worker and
// then restarting it: kill must not re-stamp HeldBy, and restart must refuse.
func TestAgentCannotKillOrRestartAnOperatorHeldWorker(t *testing.T) {
	f := newSpawnFixture(t)
	worker, err := f.p.Store.CreateIntent(model.Intent{
		Title: "operator-held worker", Description: "d", CreatedBy: f.srcID.ParticipantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Store.UpdateIntent(worker.ID, func(i *model.Intent) {
		i.Hold = true
		i.HeldBy = model.HoldActorOperator
		i.HoldReason = "操作员叫停"
	}); err != nil {
		t.Fatal(err)
	}
	rec := f.call(t, f.srcID, http.MethodPost, "/api/workers/"+worker.ID+"/kill", nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "worker_held") {
		t.Fatalf("kill of operator-held worker = %d %s", rec.Code, rec.Body.String())
	}
	got, _ := f.p.Store.GetIntent(worker.ID)
	if got.HeldBy != model.HoldActorOperator || got.HoldReason != "操作员叫停" {
		t.Fatalf("kill rewrote the holder: %+v", got)
	}
	rec = f.call(t, f.srcID, http.MethodPost, "/api/workers/"+worker.ID+"/restart", map[string]any{})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "worker_held") {
		t.Fatalf("restart of operator-held worker = %d %s", rec.Code, rec.Body.String())
	}
}
