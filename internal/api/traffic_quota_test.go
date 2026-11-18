package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	native "github.com/kksty/tianwu/internal/aop/traffic"
	"github.com/kksty/tianwu/internal/executor"
	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/traffic"
)

func openRunJournalForTest(t *testing.T, s *Server, runID string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), runID+".jsonl")
	if _, err := s.openJournal(runID, logPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.closeJournal(runID) })
	return logPath
}

func readQuotaJournalLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("journal line is not JSON: %v: %s", err, line)
		}
		rows = append(rows, row)
	}
	return rows
}

func seedHTTPBytes(t *testing.T, caseRoot string, size int) {
	t.Helper()
	dir := filepath.Join(caseRoot, ".http")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.pb"), bytes.Repeat([]byte("x"), size), 0600); err != nil {
		t.Fatal(err)
	}
}

func quotaTestFlow(id string, bodySize int) *native.Flow {
	return &native.Flow{
		Id: id, Complete: true,
		Request:  &native.HttpRequest{Method: "GET", Url: "https://example.test/" + id, Headers: []*native.Header{{Name: "X-Test", Value: "1"}}},
		Response: &native.HttpResponse{StatusCode: 200, Body: bytes.Repeat([]byte("b"), bodySize)},
	}
}

// When the project's .http body budget is already spent, the flow is still
// recorded but without bodies, and the run journal carries a dedicated
// kind=traffic_quota warning with the project, used and limit bytes. Existing
// evidence on disk is untouched.
func TestTrafficQuotaJournalWarnsOnOverflow(t *testing.T) {
	s, p := newBindingServer(t)
	s.MaxProjectHTTPBytes = 1024
	in, err := s.Store.CreateIntent(model.Intent{Title: "配额测试"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Store.AddRun(model.Run{IntentID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	logPath := openRunJournalForTest(t, s, run.ID)

	seed := bytes.Repeat([]byte("x"), 2000) // > 1 KiB budget
	seedHTTPBytes(t, s.CaseRoot, len(seed))
	flow := quotaTestFlow("flow-quota", 512)
	if err := s.appendRunEvent(run.ID, executor.Event{
		Kind: "http", SessionID: "sess-1", NativeEventID: "ev-1", NativeFlow: flow,
	}); err != nil {
		t.Fatalf("appendRunEvent: %v", err)
	}

	var quotaEvent, flowEvent map[string]any
	for _, row := range readQuotaJournalLines(t, logPath) {
		switch row["kind"] {
		case "traffic_quota":
			quotaEvent = row
		case "http":
			flowEvent = row
		}
	}
	if quotaEvent == nil {
		t.Fatal("journal has no kind=traffic_quota warning")
	}
	if got := quotaEvent["project"]; got != p.ID {
		t.Fatalf("quota event project = %v, want %q", got, p.ID)
	}
	if got := quotaEvent["max_bytes"]; got != float64(1024) {
		t.Fatalf("quota event max_bytes = %v, want 1024", got)
	}
	used, _ := quotaEvent["used_bytes"].(float64)
	if used <= 1024 {
		t.Fatalf("quota event used_bytes = %v, want > 1024", quotaEvent["used_bytes"])
	}
	text, _ := quotaEvent["text"].(string)
	if !strings.Contains(text, "项目 HTTP 正文配额已满") || !strings.Contains(text, "已有证据未改动") {
		t.Fatalf("quota event text = %q", text)
	}
	if quotaEvent["run_id"] != run.ID {
		t.Fatalf("quota event run_id = %v, want %q", quotaEvent["run_id"], run.ID)
	}
	if flowEvent == nil || flowEvent["flow_ref"] == "" || flowEvent["flow_ref"] == nil {
		t.Fatalf("http journal event lost its flow ref: %v", flowEvent)
	}
	if quotaEvent["flow_ref"] != flowEvent["flow_ref"] {
		t.Fatalf("quota flow_ref %v != http flow_ref %v", quotaEvent["flow_ref"], flowEvent["flow_ref"])
	}

	ref, _ := flowEvent["flow_ref"].(string)
	capture, stored, err := traffic.ReadCapture(s.CaseRoot, ref)
	if err != nil {
		t.Fatalf("quota capture unreadable: %v", err)
	}
	if !capture.QuotaExceeded || !capture.PreviewOnly || !capture.Truncated {
		t.Fatalf("capture flags = %+v", capture)
	}
	if capture.RequestBytes != 0 || capture.ResponseBytes != 512 {
		t.Fatalf("observed sizes = %d/%d, want 0/512", capture.RequestBytes, capture.ResponseBytes)
	}
	if len(stored.GetResponse().GetBody()) != 0 {
		t.Fatalf("stored body was not stripped: %d bytes", len(stored.GetResponse().GetBody()))
	}
	if len(stored.GetRequest().GetHeaders()) != 1 || stored.GetRequest().GetHeaders()[0].GetName() != "X-Test" {
		t.Fatalf("request headers lost: %v", stored.GetRequest().GetHeaders())
	}
	after, err := os.ReadFile(filepath.Join(s.CaseRoot, ".http", "seed.pb"))
	if err != nil || !bytes.Equal(after, seed) {
		t.Fatalf("existing evidence changed: %v", err)
	}
}

// 0 must mean unlimited at the API layer too: no quota warning, and the body is
// preserved even when the directory is already large.
func TestTrafficQuotaZeroIsUnlimited(t *testing.T) {
	s, _ := newBindingServer(t)
	s.MaxProjectHTTPBytes = 0
	in, err := s.Store.CreateIntent(model.Intent{Title: "无上限"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Store.AddRun(model.Run{IntentID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	logPath := openRunJournalForTest(t, s, run.ID)
	seedHTTPBytes(t, s.CaseRoot, 4096)
	if err := s.appendRunEvent(run.ID, executor.Event{
		Kind: "http", SessionID: "sess-1", NativeEventID: "ev-1", NativeFlow: quotaTestFlow("flow-free", 512),
	}); err != nil {
		t.Fatalf("appendRunEvent: %v", err)
	}
	for _, row := range readQuotaJournalLines(t, logPath) {
		if row["kind"] == "traffic_quota" {
			t.Fatalf("unlimited project wrote a quota warning: %v", row)
		}
	}
	rows, total, err := traffic.ListCaptures(s.CaseRoot, "", run.ID, 0, 10)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("ListCaptures = %+v, %d, %v", rows, total, err)
	}
	if rows[0].QuotaExceeded || rows[0].PreviewOnly {
		t.Fatalf("unlimited capture marked as preview: %+v", rows[0])
	}
	_, stored, err := traffic.ReadCapture(s.CaseRoot, rows[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.GetResponse().GetBody()) != 512 {
		t.Fatalf("unlimited project lost the body: %d", len(stored.GetResponse().GetBody()))
	}
}
