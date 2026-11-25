package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/decider"
	"github.com/c1ayooo/4zrcin/internal/model"
)

// The inbox is the one operator read: the fallback authorization request is
// structured and visible even when the commander wrote nothing to state.md.
func TestOperatorInboxAndRequestLifecycle(t *testing.T) {
	s, _, st := newCommanderServer(t)
	req, created, err := st.EnsureIdleOperatorRequest()
	if err != nil || !created {
		t.Fatalf("ensure: created=%v err=%v", created, err)
	}

	rec := call(t, s, "GET", "/api/operator-inbox", nil)
	if rec.Code != 200 {
		t.Fatalf("inbox = %d %s", rec.Code, rec.Body.String())
	}
	var inbox struct {
		Authorization []struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		} `json:"authorization"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatal(err)
	}
	if inbox.Total != 1 || len(inbox.Authorization) != 1 || inbox.Authorization[0].ID != req.ID {
		t.Fatalf("inbox = %+v", inbox)
	}

	rec = call(t, s, "PUT", "/api/operator-requests/"+req.ID, map[string]any{
		"action": "对计费接口做受控写入", "asset": "36.103.205.127",
		"impact": "可能生成一条测试账单", "authorization": "允许写业务库并回滚",
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "对计费接口做受控写入") {
		t.Fatalf("detail = %d %s", rec.Code, rec.Body.String())
	}
	got, _ := st.GetOperatorRequest(req.ID)
	if got.Generic || got.Asset != "36.103.205.127" {
		t.Fatalf("stored request = %+v", got)
	}

	rec = call(t, s, "POST", "/api/operator-requests/"+req.ID+"/resolve", map[string]any{"resolution": "已授权，按最小验证做"})
	if rec.Code != 200 {
		t.Fatalf("resolve = %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetOperatorRequest(req.ID); got.Status != "resolved" || got.Resolution == "" {
		t.Fatalf("resolved request = %+v", got)
	}
}

func TestOperatorRequestDetailsRideOneCommanderPrompt(t *testing.T) {
	s, ex, st := newCommanderServer(t)
	req, _, err := st.EnsureIdleOperatorRequest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestOperatorDetails(req.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StartCommander(context.Background(), withBoardAnchor("## 本轮"), false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## 操作员请求补齐授权明细", req.ID, "PUT /api/operator-requests/" + req.ID} {
		if !strings.Contains(ex.req.Prompt, want) {
			t.Fatalf("detail prompt missing %q:\n%s", want, ex.req.Prompt)
		}
	}
	if got, _ := st.GetOperatorRequest(req.ID); got.DetailRequested {
		t.Fatal("detail flag was not consumed by the prompt")
	}
	if _, _, err := s.StartCommander(context.Background(), withBoardAnchor("## 本轮"), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ex.req.Prompt, "## 操作员请求补齐授权明细") {
		t.Fatalf("detail section leaked into the next round: %q", ex.req.Prompt)
	}
}

// The detail request is the operator's one-shot click: it sets the flag and
// wakes the commander; a harness session cannot trigger it.
func TestRequestOperatorDetailsIsOperatorOnly(t *testing.T) {
	s, _, st := newCommanderServer(t)
	s.Decider = decider.NewRunner(st, nil, "")
	req, _, err := st.EnsureIdleOperatorRequest()
	if err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, "POST", "/api/operator-requests/"+req.ID+"/request-details", map[string]any{})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request-details = %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetOperatorRequest(req.ID); !got.DetailRequested {
		t.Fatal("detail flag was not set")
	}

	mux := requestMux(s)
	hreq := harnessRequest("POST", "/api/operator-requests/"+req.ID+"/request-details")
	hrec := record(mux, hreq)
	if hrec.Code != http.StatusForbidden {
		t.Fatalf("harness request-details = %d %s", hrec.Code, hrec.Body.String())
	}
}

// The commander may fill details but cannot resolve the operator's request.
func TestOperatorRequestResolveIsOperatorOnly(t *testing.T) {
	s, _, st := newCommanderServer(t)
	req, _, err := st.EnsureIdleOperatorRequest()
	if err != nil {
		t.Fatal(err)
	}
	mux := requestMux(s)
	w := harnessRequest("POST", "/api/operator-requests/"+req.ID+"/resolve")
	w.Body = jsonBody(`{"resolution":"commander says done"}`)
	rec := record(mux, w)
	if rec.Code != 403 {
		t.Fatalf("commander resolve = %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetOperatorRequest(req.ID); got.Status != "pending" {
		t.Fatalf("commander changed request status: %+v", got)
	}
}

func requestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.Register(mux)
	return mux
}

func jsonBody(body string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(body))
}

func record(mux *http.ServeMux, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The commander can file a concrete request directly (no "列明细" detour). It
// reuses the generic fallback, rejects a duplicate concrete request, refuses an
// executor, and records the right provenance.
func TestCommanderCreatesConcreteOperatorRequest(t *testing.T) {
	s, _, st := newCommanderServer(t)
	mux := requestMux(s)
	body := `{"action":"对 225.219 的上传接口做受控写入","asset":"36.103.225.219","impact":"可能新增一条测试文件","authorization":"允许写并清理"}`

	req := harnessRequest("POST", "/api/operator-requests")
	req.Body = jsonBody(body)
	rec := record(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Request model.OperatorRequest `json:"request"`
		Created bool                  `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Created || out.Request.Generic || out.Request.RequestedBy != model.OperatorRequestFromCommander ||
		out.Request.Action == "" || out.Request.Asset == "" {
		t.Fatalf("created request = %+v created=%v", out.Request, out.Created)
	}

	// A second concrete request would duplicate the operator's decision.
	req = harnessRequest("POST", "/api/operator-requests")
	req.Body = jsonBody(body)
	if rec := record(mux, req); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d %s", rec.Code, rec.Body.String())
	}

	// An executor may not file a request for its own scope.
	exec := harnessRequest("POST", "/api/operator-requests")
	exec = exec.WithContext(WithIdentity(exec.Context(), collab.Identity{
		ProjectID: "default", ParticipantID: collab.ParticipantID("in_x"), IntentID: "in_x", RunID: "r_1",
	}))
	exec.Body = jsonBody(body)
	if rec := record(mux, exec); rec.Code != http.StatusForbidden {
		t.Fatalf("executor create = %d %s", rec.Code, rec.Body.String())
	}

	if _, err := st.ResolveOperatorRequest(out.Request.ID, "已处理"); err != nil {
		t.Fatal(err)
	}
	if rec := call(t, s, "POST", "/api/operator-requests", map[string]any{"asset": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing action = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, s, "POST", "/api/operator-requests", map[string]any{"action": "操作员自己的请求", "authorization": "x"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"requested_by":"operator"`) {
		t.Fatalf("operator create = %d %s", rec.Code, rec.Body.String())
	}

	// Reusing a generic fallback keeps the same row.
	pending := st.PendingOperatorRequests()
	if len(pending) != 1 {
		t.Fatalf("pending before fallback = %+v", pending)
	}
	if _, err := st.ResolveOperatorRequest(pending[0].ID, "已处理"); err != nil {
		t.Fatal(err)
	}
	fallback, created, err := st.EnsureIdleOperatorRequest()
	if err != nil || !created {
		t.Fatalf("ensure fallback: created=%v err=%v", created, err)
	}
	req = harnessRequest("POST", "/api/operator-requests")
	req.Body = jsonBody(`{"action":"补成具体请求","asset":"A","impact":"I","authorization":"允许"}`)
	rec = record(mux, req)
	var reused struct {
		Request model.OperatorRequest `json:"request"`
		Created bool                  `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reused); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || reused.Created || reused.Request.ID != fallback.ID || reused.Request.Generic {
		t.Fatalf("reuse fallback = %d %+v created=%v", rec.Code, reused.Request, reused.Created)
	}
}
