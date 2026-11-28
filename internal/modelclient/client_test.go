package modelclient

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/c1ayooo/4zrcin/internal/testutil"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNormaliseBase(t *testing.T) {
	cases := map[string]string{
		"http://h:8080":                     "http://h:8080/v1",
		"http://h:8080/":                    "http://h:8080/v1",
		"http://h:8080/v1":                  "http://h:8080/v1",
		"http://h:8080/v1/":                 "http://h:8080/v1",
		"http://h:8080/v1/chat/completions": "http://h:8080/v1",
		"http://h:8080/api/v1":              "http://h:8080/api/v1",
	}
	for in, want := range cases {
		if got := normaliseBase(in); got != want {
			t.Errorf("normaliseBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// Endpoint is the shared normalisation: the settings page and the model list
// must resolve the same base and protocol the connectivity probe does.
func TestEndpointResolvesProtocolAndBase(t *testing.T) {
	cases := []struct {
		name         string
		base, prov   string
		wantBase     string
		wantProvider string
	}{
		{"bare openai host gets /v1", "http://h:8080", "", "http://h:8080/v1", ProviderOpenAI},
		{"explicit openai keeps /v1", "http://h:8080/v1/", "openai", "http://h:8080/v1", ProviderOpenAI},
		{"chat-completions suffix stripped", "http://h:8080/v1/chat/completions", "", "http://h:8080/v1", ProviderOpenAI},
		{"anthropic by name gets /v1", "https://api.anthropic.com", "anthropic", "https://api.anthropic.com/v1", ProviderAnthropic},
		{"messages suffix implies anthropic", "https://gw.test/v1/messages", "", "https://gw.test/v1", ProviderAnthropic},
		{"anthropic custom path kept", "https://gw.test/anthropic", "anthropic", "https://gw.test/anthropic", ProviderAnthropic},
	}
	for _, tc := range cases {
		gotBase, gotProvider := Endpoint(tc.base, tc.prov)
		if gotBase != tc.wantBase || gotProvider != tc.wantProvider {
			t.Errorf("%s: Endpoint(%q,%q) = (%q,%q), want (%q,%q)",
				tc.name, tc.base, tc.prov, gotBase, gotProvider, tc.wantBase, tc.wantProvider)
		}
	}
}

// Check is the settings page's fallback probe (no running host). It must stay a
// plain-text round trip: no JSON mode, no invented caps — those belonged to the
// JSON-mode path.
func TestCheckSendsPlainTextProbe(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = map[string]any{}
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "OK"}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	c, err := NewWithProvider(srv.URL, "sk-test", "fixture-model", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", gotBody["messages"])
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "Connectivity check. Reply only OK." {
		t.Fatalf("probe message = %v", msg)
	}
	if gotBody["model"] != "fixture-model" {
		t.Fatalf("model = %v", gotBody["model"])
	}
	for _, key := range []string{"response_format", "temperature", "max_tokens"} {
		if _, ok := gotBody[key]; ok {
			t.Fatalf("plain probe sent %s=%v", key, gotBody[key])
		}
	}
	if res.Content != "OK" {
		t.Fatalf("content = %q", res.Content)
	}
}

// The probe's reply goes through the same parsing as a real answer: think tags
// are stripped, the provider's own model id is reported, usage is kept.
func TestCheckParsesOpenAIReplyStripsThink(t *testing.T) {
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "returned-name",
			"choices": []map[string]any{{
				"message":       map[string]any{"content": "<think>算一下</think>{\"ok\":true}"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer srv.Close()

	c, err := NewWithProvider(srv.URL, "k", "fallback-model", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != `{"ok":true}` || strings.Contains(res.Content, "think") {
		t.Fatalf("content = %q", res.Content)
	}
	if res.ReturnedModel != "returned-name" || res.Model != "returned-name" {
		t.Fatalf("returned=%q model=%q", res.ReturnedModel, res.Model)
	}
	if res.Usage.PromptTokens != 10 || res.Usage.CompletionTokens != 5 || res.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

// The Anthropic shape of the same probe: the Messages endpoint, key and version
// headers, and the conservative default max_tokens that API requires.
func TestCheckParsesAnthropicReply(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	var gotBody map[string]any
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		body, _ := io.ReadAll(r.Body)
		gotBody = map[string]any{}
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{
			"model":"claude-returned","stop_reason":"end_turn",
			"content":[{"type":"text","text":"<thinking>skip</thinking>OK"}],
			"usage":{"input_tokens":7,"output_tokens":3}
		}`))
	}))
	defer srv.Close()

	c, err := NewWithProvider(srv.URL, "secret", "claude-x", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotKey != "secret" || gotVersion != "2023-06-01" {
		t.Fatalf("key=%q version=%q", gotKey, gotVersion)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 || gotBody["model"] != "claude-x" || gotBody["max_tokens"] != float64(anthropicDefaultMaxToken) {
		t.Fatalf("request = %v", gotBody)
	}
	if res.Content != "OK" {
		t.Fatalf("content = %q", res.Content)
	}
	if res.Usage.PromptTokens != 7 || res.Usage.CompletionTokens != 3 || res.Usage.TotalTokens != 10 {
		t.Fatalf("usage = %+v", res.Usage)
	}
	if res.ReturnedModel != "claude-returned" || res.Model != "claude-returned" {
		t.Fatalf("returned=%q model=%q", res.ReturnedModel, res.Model)
	}
}

// A probe that hits the token cap is an answer-shaped failure: the error must
// name the cap, not read as a parse error.
func TestCheckReportsATruncatedReply(t *testing.T) {
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": `{"partial"`},
				"finish_reason": "length",
			}},
		})
	}))
	defer srv.Close()

	c, err := NewWithProvider(srv.URL, "k", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("truncated reply error = %v", err)
	}
}

// A 401 from the probe must stay a typed APIError the settings page can
// classify as needs-a-human, not a transient failure.
func TestCheckSurfacesNeedsHumanOn401(t *testing.T) {
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	c, err := NewWithProvider(srv.URL, "k", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Check(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != 401 || !apiErr.NeedsHuman() {
		t.Fatalf("status=%d needsHuman=%v", apiErr.StatusCode, apiErr.NeedsHuman())
	}
}

// cmd/zrcin bridges its --model* flags into the process environment through
// these constants and settings reads the same names back; renaming one would
// silently detach the flags. The strings themselves are the contract.
func TestEnvVariableNames(t *testing.T) {
	if EnvBase != "CYBER_BASE_URL" {
		t.Fatalf("EnvBase = %q", EnvBase)
	}
	if EnvKey != "CYBER_API_KEY" {
		t.Fatalf("EnvKey = %q", EnvKey)
	}
	if EnvModel != "CYBER_MODEL" {
		t.Fatalf("EnvModel = %q", EnvModel)
	}
}

func TestCompleteParsesResponseAndStripsThink(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "glm-5",
			"choices": []map[string]any{{
				"message":       map[string]any{"content": "<think>算一下</think>{\"noop\":{\"reason\":\"等\"}}"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "sk-test", "glm-5")
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Complete(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" || gotModel != "glm-5" {
		t.Fatalf("auth=%q model=%q", gotAuth, gotModel)
	}
	if strings.Contains(res.Content, "think") || !strings.Contains(res.Content, "noop") {
		t.Fatalf("content = %q", res.Content)
	}
	if res.Usage.TotalTokens != 15 || res.Model != "glm-5" {
		t.Fatalf("usage=%+v model=%q", res.Usage, res.Model)
	}
}

func TestChatSendsTranscriptInOrder(t *testing.T) {
	var got []Message
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		got = req.Messages
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": `{"noop":{"reason":"x"}}`},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "k", "m")
	if err != nil {
		t.Fatal(err)
	}
	in := []Message{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: `{"tool":"facts"}`},
		{Role: "user", Content: "结果"},
	}
	if _, err := c.Chat(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(in) {
		t.Fatalf("messages = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Fatalf("message[%d] = %+v, want %+v", i, got[i], in[i])
		}
	}
}

func TestCompleteReportsHTTPError(t *testing.T) {
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "k", "m")
	if _, err := c.Complete(context.Background(), "s", "u"); err == nil {
		t.Fatal("want error on 401")
	}
}

func TestNewRequiresAllParts(t *testing.T) {
	if _, err := New("", "k", "m"); err == nil {
		t.Fatal("missing base must fail")
	}
	if _, err := New("http://h", "", "m"); err == nil {
		t.Fatal("missing key must fail")
	}
	if _, err := New("http://h", "k", ""); err == nil {
		t.Fatal("missing model must fail")
	}
}

// A length-capped completion is a prefix, not an answer. Reporting it as a
// generic parse failure sent the decider into a retry loop against the same cap;
// the error has to name the cap.
func TestChatReportsATruncatedCompletion(t *testing.T) {
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": `{"intents":[{"title":"x"`},
				"finish_reason": "length",
			}},
			"usage": map[string]any{"total_tokens": 12},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "sk-test", "m")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxTokens != 0 {
		t.Fatalf("default max_tokens = %d, want 0 (omit; provider/harness default)", c.MaxTokens)
	}
	if _, err := c.Complete(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("truncated completion error = %v", err)
	}
}

// Zero max_tokens is "not set": the request must omit the field so the
// provider's model limit applies, exactly like a harness run without
// --max-tokens. A hardcoded cap here is what made the two disagree.
func TestChatOmitsMaxTokensWhenUnset(t *testing.T) {
	var raw map[string]any
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"noop":{"reason":"x"}}`}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "k", "m")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d", c.MaxTokens)
	}
	if _, err := c.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["max_tokens"]; ok {
		t.Fatalf("request sent max_tokens=%v although it was unset", raw["max_tokens"])
	}
}
