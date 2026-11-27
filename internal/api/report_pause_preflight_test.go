package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// E10 measured behavior (2026-11-27): a paused — often already closed —
// project cancels the report generation the moment it starts, after the
// handler already answered 202. The preflight answers 409 synchronously with
// the pause cause and the unlock path, before anything queues.
func TestCreateReportPreflightsThePausedProject(t *testing.T) {
	s, _, mux := newBatchServer(t, &batchExec{})
	s.paused.Store(true)
	s.controlMu.Lock()
	s.pauseCause = "completion_standby"
	s.controlMu.Unlock()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reports", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("paused project: status = %d, want 409", rec.Code)
	}
	for _, want := range []string{"completion_standby", "append-goal"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("409 body missing %q: %s", want, rec.Body.String())
		}
	}

	// A manual pause names its own way out.
	s.controlMu.Lock()
	s.pauseCause = "manual_pause"
	s.controlMu.Unlock()
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reports", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "恢复项目") {
		t.Fatalf("manual pause: status = %d body = %s", rec.Code, rec.Body.String())
	}

	// Unpaused keeps the pre-existing behavior: without harness wiring the
	// request reaches the harness check (503), not the pause gate.
	s.paused.Store(false)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reports", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unpaused without harness: status = %d, want 503", rec.Code)
	}
}
