package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeTrail records what the runner told each side, so the test can assert the
// contract instead of trusting the runner's own log lines.
type fakeTrail struct {
	mu           sync.Mutex
	goals        []map[string]any
	scopes       []string
	subs         []struct{ Code, Flag string }
	started      []string
	closed       []string
	benchHeaders int
}

func (f *fakeTrail) addGoal(b map[string]any) {
	f.mu.Lock()
	f.goals = append(f.goals, b)
	f.mu.Unlock()
}
func (f *fakeTrail) addScope(s string) { f.mu.Lock(); f.scopes = append(f.scopes, s); f.mu.Unlock() }
func (f *fakeTrail) addSub(code, flag string) {
	f.mu.Lock()
	f.subs = append(f.subs, struct{ Code, Flag string }{code, flag})
	f.mu.Unlock()
}

// oneBenchRound drives exactly one full round (poll → plan → goal/start/scope →
// submit) plus the shutdown rewrite, against fake platform and fake console
// servers. It is the "能跑一轮" claim, executed rather than asserted by prose.
func TestBenchOnceRunsOneRoundAgainstFakeSides(t *testing.T) {
	trail := &fakeTrail{}
	addr := "10.0.1.5:8080"

	// Platform: one challenge, stopped until start, then available.
	startedUp := false
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("BENCHMARK_TOKEN") != "bench-token" {
			t.Errorf("platform call without the token header")
		}
		switch r.URL.Path {
		case "/openapi/v1/challenges":
			status, addrs := "stopped", "[]"
			if startedUp {
				status, addrs = "available", `["`+addr+`"]`
			}
			io.WriteString(w, `[{"unique_code":"web_sql_injection_01","description":"SQL 注入","difficulty":"easy","level":1,"total_score":100,"flag_count":1,"correct_flag_count":0,"is_completed":false,"container_status":"`+status+`","container_addr":`+addrs+`}]`)
		case "/openapi/v1/challenges/start":
			startedUp = true
			trail.mu.Lock()
			trail.started = append(trail.started, r.URL.Query().Get("unique_code"))
			trail.mu.Unlock()
			io.WriteString(w, `{"unique_code":"web_sql_injection_01","container_addr":["`+addr+`"]}`)
		case "/openapi/v1/challenges/close":
			startedUp = false
			trail.mu.Lock()
			trail.closed = append(trail.closed, r.URL.Query().Get("unique_code"))
			trail.mu.Unlock()
			io.WriteString(w, `{"unique_code":"web_sql_injection_01","closed":true}`)
		case "/openapi/v1/challenges/submit":
			var req struct {
				UniqueCode string `json:"unique_code"`
				Flag       string `json:"flag"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			trail.addSub(req.UniqueCode, req.Flag)
			io.WriteString(w, `{"correct":true,"awarded":100,"cumulative_score":100,"correct_flag_count":1,"total_flag_count":1,"matched_flag_index":0}`)
		default:
			t.Errorf("unexpected platform path %s", r.URL.Path)
		}
	}))
	defer platform.Close()

	// Console: documents, append-goal, facts.
	rev := uint64(0)
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer console-token" {
			t.Errorf("console call without the bearer token: %s %s", r.Method, r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/p/default/api/documents":
			io.WriteString(w, `{"documents":{"scope":{"text":"old","revision":`+itoa(rev)+`}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/p/default/api/documents/scope":
			if r.Header.Get("X-Tw-Actor") != "bench" {
				t.Errorf("scope write without the bench signature")
			} else {
				trail.benchHeaders++
			}
			var req struct {
				Text       string `json:"text"`
				Revision   uint64 `json:"revision"`
				EvidenceID string `json:"evidence_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Revision != rev {
				t.Errorf("scope revision = %d, want %d", req.Revision, rev)
			}
			if !strings.Contains(req.EvidenceID, "challenges-") {
				t.Errorf("scope evidence = %q, want the dump file name", req.EvidenceID)
			}
			if strings.Contains(strings.ToLower(req.Text), "old") {
				t.Errorf("scope kept the previous text: %q", req.Text)
			}
			rev++
			trail.addScope(req.Text)
			io.WriteString(w, `{"revision":`+itoa(rev)+`,"changed":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/p/default/api/project/append-goal":
			if r.Header.Get("X-Tw-Actor") != "bench" {
				t.Errorf("goal write without the bench signature")
			} else {
				trail.benchHeaders++
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			trail.addGoal(body)
			io.WriteString(w, `{"goal":{"id":"g_1"},"created":true,"resumed":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/p/default/api/facts":
			// The flag is declared by an executor as a fact with the container as
			// its entity — that binding is what selects the challenge.
			io.WriteString(w, `{"facts":[{"id":"f_1","title":"拿到 flag{abc123}","summary":"来自 `+addr+`","entity_ref":"`+addr+`"}]}`)
		default:
			t.Errorf("unexpected console call %s %s", r.Method, r.URL.Path)
		}
	}))
	defer console.Close()

	dir := t.TempDir()
	env := map[string]string{
		"BENCHMARK_BASE_URL": platform.URL,
		"BENCHMARK_TOKEN":    "bench-token",
	}
	var stderrBuf bytes.Buffer
	code := cliBench([]string{
		"-console", console.URL, "-token", "console-token", "-project", "default",
		"-dump-dir", filepath.Join(dir, "challenges"),
		"-submit-log", filepath.Join(dir, "submissions.jsonl"),
		"-attempt-log", filepath.Join(dir, "attempts.jsonl"),
		"-interval", "1ms", "-once",
	}, io.Discard, &stderrBuf, func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("bench -once = %d, want 0; stderr:\n%s", code, stderrBuf.String())
	}

	trail.mu.Lock()
	defer trail.mu.Unlock()
	if len(trail.goals) != 1 {
		t.Fatalf("goals posted = %d, want 1: %+v", len(trail.goals), trail.goals)
	}
	if got := trail.goals[0]["idempotency_key"]; got != "bench-goal:web_sql_injection_01" {
		t.Fatalf("goal key = %v", got)
	}
	if stmt, _ := trail.goals[0]["statement"].(string); !strings.Contains(stmt, "web_sql_injection_01") {
		t.Fatalf("goal statement does not name the challenge: %q", stmt)
	}
	if len(trail.started) != 1 || len(trail.subs) != 1 {
		t.Fatalf("started=%v submitted=%v, want one each", trail.started, trail.subs)
	}
	if trail.subs[0].Flag != "flag{abc123}" || trail.subs[0].Code != "web_sql_injection_01" {
		t.Fatalf("submission = %+v", trail.subs[0])
	}
	if len(trail.scopes) < 2 {
		t.Fatalf("scope writes = %d, want the round write and the shutdown write", len(trail.scopes))
	}
	if !strings.Contains(trail.scopes[0], addr) {
		t.Fatalf("first scope does not carry the live address: %q", trail.scopes[0])
	}
	last := trail.scopes[len(trail.scopes)-1]
	if strings.Contains(last, addr) {
		t.Fatalf("shutdown scope kept a released address: %q", last)
	}
	if !strings.Contains(last, "没有进行中的题") {
		t.Fatalf("shutdown scope does not state why it is empty: %q", last)
	}
	if trail.benchHeaders < 2 {
		t.Fatalf("bench signature used %d times, want goal and scope writes", trail.benchHeaders)
	}

	// The submitter log is the artifact that leaves the machine: one line, digest
	// not the raw flag, and marked correct.
	raw, err := os.ReadFile(filepath.Join(dir, "submissions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "flag{abc123}") {
		t.Fatalf("submission log leaked the raw flag: %s", raw)
	}
	var sub struct {
		UniqueCode string `json:"unique_code"`
		Correct    bool   `json:"correct"`
		Awarded    int    `json:"awarded"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &sub); err != nil {
		t.Fatalf("submission log line is not JSON: %v (%s)", err, raw)
	}
	if sub.UniqueCode != "web_sql_injection_01" || !sub.Correct || sub.Awarded != 100 {
		t.Fatalf("submission log = %+v", sub)
	}

	// Closing the containers is part of the shutdown: the platform must see it
	// before the final scope is written.
	if len(trail.closed) != 1 {
		t.Fatalf("containers closed = %v, want 1", trail.closed)
	}
}

func itoa(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
