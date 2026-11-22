package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/bench"
	"github.com/kksty/tianwu/internal/store"
)

// The goal supply polls the platform repeatedly; the same challenge must find the
// goal it already created instead of adding another.
func TestAppendBenchGoalIsIdempotentPerChallenge(t *testing.T) {
	s, _ := newBindingServer(t)
	first, created, err := s.AppendBenchGoal("web_sql_injection_01", "拿到 web_sql_injection_01 的全部 flag", "起点：平台容器")
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.ID == "" || first.UpdatedBy != BenchAuthor {
		t.Fatalf("first append = %+v (created=%v)", first, created)
	}
	second, created, err := s.AppendBenchGoal("web_sql_injection_01", "拿到 web_sql_injection_01 的全部 flag", "起点：平台容器")
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("second append created a new goal %q (created=%v), want %q", second.ID, created, first.ID)
	}
	if got := len(s.Store.Goals()); got != 1 {
		t.Fatalf("goals = %d, want 1", got)
	}
	// A different challenge is a different goal: one goal per challenge is what
	// keeps "there is always an open target" true for every started container.
	if _, created, err := s.AppendBenchGoal("pwn_stack_03", "拿到 pwn_stack_03 的全部 flag", ""); err != nil || !created {
		t.Fatalf("second challenge append: created=%v err=%v", created, err)
	}
	if got := len(s.Store.Goals()); got != 2 {
		t.Fatalf("goals = %d, want 2", got)
	}
	if _, _, err := s.AppendBenchGoal("  ", "空", ""); err == nil {
		t.Fatal("a goal without a challenge code was accepted")
	}
	if strings.TrimSpace(first.IdempotencyKey) == "" {
		t.Fatal("goal was written without an idempotency key")
	}
}

// A bench-authored scope save must be attributable in both places the run is
// audited: the document row (updated_by) and operator-actions.jsonl. Without the
// header it stays the operator's write, which is exactly the kind of miscredit
// this project has already fixed once.
func TestBenchScopeSaveIsSignedAndAudited(t *testing.T) {
	s, p := newBindingServer(t)
	body, _ := json.Marshal(map[string]any{
		"text":        "本任务当前允许触及的地址：\n- 10.0.1.5:8080\n",
		"revision":    0,
		"evidence_id": "challenges-20261122T080000.000Z-001.json",
		"note":        "地址 1 个",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/documents/scope", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(BenchActorHeader, BenchAuthor)
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	s.Register(mux)
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", w.Code, w.Body.String())
	}

	stored, ok := s.Store.Document(store.DocumentScope)
	if !ok || stored.UpdatedBy != BenchAuthor {
		t.Fatalf("document = %+v, want updated_by %q", stored, BenchAuthor)
	}

	raw, err := os.ReadFile(filepath.Join(p.Dir, "operator-actions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var line operatorAction
	if err := json.Unmarshal(bytes.TrimSpace(raw), &line); err != nil {
		t.Fatalf("audit line is not JSON: %v (%s)", err, raw)
	}
	if line.Action != "document_scope_write" || line.By != BenchAuthor ||
		line.EvidenceID != "challenges-20261122T080000.000Z-001.json" ||
		!strings.Contains(line.Note, "地址 1 个") || !strings.Contains(line.Note, "起点 rev 1") {
		t.Fatalf("audit line = %+v, want action/by/evidence/revision", line)
	}
}

// Any other header value is not a signature: it must fall back to the operator,
// so a stray or copied header cannot credit a machine for a human's edit. The
// exact literal is checked here too, so this test fails on its own if the
// signature path breaks — the positive case is not borrowed from another test.
func TestBenchActorHeaderOnlyHonoursTheLiteralValue(t *testing.T) {
	save := func(headerValue string) string {
		s, _ := newBindingServer(t)
		body, _ := json.Marshal(map[string]any{"text": "起点正文", "revision": 0})
		req := httptest.NewRequest(http.MethodPut, "/api/documents/scope", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(BenchActorHeader, headerValue)
		w := httptest.NewRecorder()
		mux := http.NewServeMux()
		s.Register(mux)
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("save with %q = %d: %s", headerValue, w.Code, w.Body.String())
		}
		stored, _ := s.Store.Document(store.DocumentScope)
		return stored.UpdatedBy
	}
	if got := save(BenchAuthor); got != BenchAuthor {
		t.Fatalf("exact literal signed as %q, want %q", got, BenchAuthor)
	}
	if got := save("bench "); got == BenchAuthor {
		t.Fatalf("a non-literal header value was accepted as the bench signature (%q)", got)
	}
}

// The runner's signature literals live in two packages because the control plane
// may not import the bench package. This is the one place that keeps them equal.
func TestBenchSignatureConstantsMatchTheRunner(t *testing.T) {
	if bench.ActorHeader != BenchActorHeader || bench.ActorBench != BenchAuthor {
		t.Fatalf("bench.ActorHeader/ActorBench = %q/%q, api has %q/%q",
			bench.ActorHeader, bench.ActorBench, BenchActorHeader, BenchAuthor)
	}
}
