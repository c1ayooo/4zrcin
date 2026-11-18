package aop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// connectRPC is one fake Connect unary route: the method path and its JSON
// answer. The tests assert the request document too, because the harness
// validates proto field names strictly.
type connectRPC struct {
	method string
	in     any
	status int
	body   string
	called bool
}

func fakeConnectServer(t *testing.T, routes []*connectRPC) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer host-token" {
			t.Errorf("Authorization = %q", got)
		}
		for _, route := range routes {
			if !strings.HasSuffix(r.URL.Path, route.method) {
				continue
			}
			route.called = true
			if route.in != nil {
				if err := json.NewDecoder(r.Body).Decode(route.in); err != nil {
					t.Errorf("%s request: %v", route.method, err)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(route.status)
			_, _ = w.Write([]byte(route.body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, New(srv.URL, "host-token")
}

func TestConnectEventsReadsCanonicalHistory(t *testing.T) {
	var request map[string]any
	body := `{"events":[{"cursor":"c1","event":{"id":"ev-1","sessionId":"sess-1","payload":{"message":{"role":"assistant"}}}},{"cursor":"c2","event":{"id":"ev-2"}}],"nextCursor":"c2"}`
	srv, client := fakeConnectServer(t, []*connectRPC{{method: "ListEvents", in: &request, status: 200, body: body}})
	defer srv.Close()

	page, err := client.Events(context.Background(), "sess-1", "c0", 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := request["sessionId"]; got != "sess-1" {
		t.Fatalf("sessionId = %v", got)
	}
	if got := request["afterCursor"]; got != "c0" {
		t.Fatalf("afterCursor = %v", got)
	}
	if got := request["limit"]; got != float64(50) {
		t.Fatalf("limit = %v", got)
	}
	if len(page.Events) != 2 || page.Events[0].Cursor != "c1" || !strings.Contains(string(page.Events[0].Event), `"ev-1"`) {
		t.Fatalf("events = %+v", page.Events)
	}
	if page.NextCursor != "c2" {
		t.Fatalf("next cursor = %q", page.NextCursor)
	}
}

func TestConnectEventsReturnsHostRejection(t *testing.T) {
	_, client := fakeConnectServer(t, []*connectRPC{{
		method: "ListEvents", status: http.StatusNotFound,
		body: `{"code":"not_found","message":"session not found"}`,
	}})
	_, err := client.Events(context.Background(), "gone", "", 10)
	if err == nil || !IsConnectNotFound(err) {
		t.Fatalf("want NOT_FOUND, got %v", err)
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("rejection lost the host message: %v", err)
	}
}

func TestConnectCommands(t *testing.T) {
	var request map[string]any
	_, client := fakeConnectServer(t, []*connectRPC{{
		method: "ListCommands", in: &request, status: 200,
		body: `{"commands":[{"name":"help","usage":"/help","description":"menu","aliases":["?"]}]}`,
	}})
	commands, err := client.Commands(context.Background(), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if request["sessionId"] != "sess-1" {
		t.Fatalf("request = %v", request)
	}
	if len(commands) != 1 || commands[0].Name != "help" || commands[0].Usage != "/help" || len(commands[0].Aliases) != 1 {
		t.Fatalf("commands = %+v", commands)
	}
}

func TestConnectResetSessionAcceptedAndRejected(t *testing.T) {
	var request map[string]any
	_, client := fakeConnectServer(t, []*connectRPC{{
		method: "ResetSession", in: &request, status: 200,
		body: `{"requestId":"req-1","accepted":{"previous":{"id":"old"},"current":{"session":{"id":"new"}}}}`,
	}})
	out, err := client.ResetSession(context.Background(), "req-1", "old", "", "title")
	if err != nil {
		t.Fatal(err)
	}
	if request["requestId"] != "req-1" || request["sessionId"] != "old" || request["title"] != "title" {
		t.Fatalf("request = %v", request)
	}
	if !out.Accepted || out.Previous != "old" || out.Current != "new" {
		t.Fatalf("outcome = %+v", out)
	}

	_, rejected := fakeConnectServer(t, []*connectRPC{{
		method: "ResetSession", status: 200,
		body: `{"requestId":"req-2","rejected":{"code":"NOT_FOUND","message":"session not found"}}`,
	}})
	out, err = rejected.ResetSession(context.Background(), "req-2", "gone", "", "")
	if err == nil || !IsConnectNotFound(err) {
		t.Fatalf("want NOT_FOUND rejection, got %v", err)
	}
	if out.Accepted || out.Code != "NOT_FOUND" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestConnectProbeAndModels(t *testing.T) {
	var probe map[string]any
	_, client := fakeConnectServer(t, []*connectRPC{
		{method: "TestLLM", in: &probe, status: 200, body: `{"ok":true,"provider":"openai","model":"real-model","latencyMs":42,"reply":"pong"}`},
		{method: "ListModels", status: 200, body: `{"ok":true,"supported":true,"models":["a","b"]}`},
	})
	result, err := client.ProbeLLM(context.Background(), LLMProbe{Provider: "openai", BaseURL: "http://gw", APIKey: "k", Model: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Model != "real-model" || result.LatencyMS != 42 {
		t.Fatalf("probe = %+v", result)
	}
	if probe["baseUrl"] != "http://gw" || probe["apiKey"] != "k" {
		t.Fatalf("probe request = %v", probe)
	}
	models, err := client.ListModels(context.Background(), LLMProbe{})
	if err != nil {
		t.Fatal(err)
	}
	if len(models.Models) != 2 || !models.Supported {
		t.Fatalf("models = %+v", models)
	}
}

func TestConnectProbeReadsProtoJSONIntegers(t *testing.T) {
	// protojson (the codec behind the host's Connect JSON) writes int64 as a
	// decimal string. A hand-written answer may use a number. Both must decode,
	// and an unreadable latency must not discard the rest of the answer.
	cases := []struct {
		name string
		body string
		want LLMProbeResult
	}{
		{
			name: "protojson string latency",
			body: `{"ok":true,"provider":"openai","model":"served","latencyMs":"1608","reply":"pong"}`,
			want: LLMProbeResult{OK: true, Provider: "openai", Model: "served", LatencyMS: 1608, Reply: "pong"},
		},
		{
			name: "number latency",
			body: `{"ok":true,"model":"served","latencyMs":42}`,
			want: LLMProbeResult{OK: true, Model: "served", LatencyMS: 42},
		},
		{
			name: "unit suffix is ignored, answer is kept",
			body: `{"ok":false,"model":"served","latencyMs":"1608ms","error":"API error (401): invalid key"}`,
			want: LLMProbeResult{OK: false, Model: "served", LatencyMS: 1608, Error: "API error (401): invalid key"},
		},
		{
			name: "unreadable latency is zero, not an error",
			body: `{"ok":true,"model":"served","latencyMs":"unknown","reply":"pong"}`,
			want: LLMProbeResult{OK: true, Model: "served", Reply: "pong"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, client := fakeConnectServer(t, []*connectRPC{{method: "TestLLM", status: 200, body: tc.body}})
			got, err := client.ProbeLLM(context.Background(), LLMProbe{Model: "draft"})
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if got != tc.want {
				t.Fatalf("probe = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestConnectUpdateConfigSendsSharedDocument(t *testing.T) {
	var request map[string]any
	_, client := fakeConnectServer(t, []*connectRPC{{
		method: "UpdateConfig", in: &request, status: 200,
		body: `{"config":{"path":"/tmp/runtime.yaml","loaded":true}}`,
	}})
	raw, err := client.UpdateConfig(context.Background(), map[string]any{
		"llm": map[string]any{"activeProfile": "default"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := request["config"].(map[string]any)
	llm, _ := config["llm"].(map[string]any)
	if llm["activeProfile"] != "default" {
		t.Fatalf("request = %v", request)
	}
	if !strings.Contains(string(raw), "runtime.yaml") {
		t.Fatalf("view = %s", raw)
	}
}

func TestConnectRejectionKeepsCode(t *testing.T) {
	_, client := fakeConnectServer(t, []*connectRPC{{
		method: "UpdateConfig", status: http.StatusBadRequest,
		body: `{"code":"invalid_argument","message":"model is required"}`,
	}})
	_, err := client.UpdateConfig(context.Background(), map[string]any{})
	var ce *ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConnectError, got %v", err)
	}
	if ce.Code != "invalid_argument" || ce.Status != http.StatusBadRequest {
		t.Fatalf("error = %+v", ce)
	}
	if IsConnectNotFound(err) {
		t.Fatal("invalid_argument read as NOT_FOUND")
	}
}
