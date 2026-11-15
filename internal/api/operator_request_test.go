package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/decider"
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
	if _, _, err := s.StartCommander(context.Background(), "## 本轮", false); err != nil {
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
	if _, _, err := s.StartCommander(context.Background(), "## 本轮", false); err != nil {
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
