package api

import (
	"context"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"sync"
	"testing"
	"time"
)

type recordExec struct {
	mu  sync.Mutex
	req executor.Request
	run bool
}

func (r *recordExec) Name() string { return "record" }

func (r *recordExec) Run(_ context.Context, req executor.Request, emit func(executor.Event)) (executor.Result, error) {
	r.mu.Lock()
	r.req = req
	r.run = true
	r.mu.Unlock()
	emit(executor.Event{Kind: executor.KindSession, Text: "actual-model"})
	emit(executor.Event{Kind: executor.KindTurnEnd, StopReason: "completed", OK: executor.BoolPtr(true)})
	return executor.Result{ExitCode: 0, StopReason: "completed"}, nil
}

func (r *recordExec) Request() executor.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.req
}

func waitRun(t *testing.T, p *project.Project, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := p.Store.GetRun(runID); ok && r.Status != model.RunRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not settle")
}
