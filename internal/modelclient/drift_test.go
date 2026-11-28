package modelclient

// Drift tests. This package and the engine's agent/provider deliberately
// disagree on three observable points, and the shell keeps its looser side ON
// PURPOSE. This client only runs as the settings page's connectivity probe when
// no harness host is online (the probe answers with Source "self"); where
// guessing beats refusing, the probe tries the extra path. When a host IS
// online, its own probe (ConfigService/TestLLM, Source "host") is the
// authoritative answer and the frontend tells the two apart by `source`. Each
// test names the engine code it differs from, so a change on either side shows
// up here instead of silently converging or diverging further.

import (
	"context"
	"encoding/json"
	"github.com/c1ayooo/4zrcin/internal/testutil"
	"net/http"
	"testing"
)

// Drift 1: a bare address. The engine (agent/provider/openai.go,
// completionEndpoint) posts a bare http://host:8080 straight to
// /chat/completions and never appends /v1. This package appends /v1: settings
// bases are commonly written without it, and a fallback probe that guesses the
// conventional path beats one that fails.
func TestDriftBareHostGetsV1Suffix(t *testing.T) {
	if gotBase, gotProvider := Endpoint("http://h:8080", ""); gotBase != "http://h:8080/v1" || gotProvider != ProviderOpenAI {
		t.Fatalf("Endpoint = (%q,%q), want the bare host with /v1 appended", gotBase, gotProvider)
	}
	var gotPath string
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "OK"}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()
	c, err := NewWithProvider(srv.URL, "k", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("probe path = %q, want /v1/chat/completions (the engine would hit /chat/completions)", gotPath)
	}
}

// Drift 2: an unknown provider name. The engine (agent/provider/provider.go,
// Resolve) rejects one with "unsupported provider ...". This package reads the
// name as a protocol hint and falls back to the OpenAI wire format: the probe
// should try, not refuse — an OpenAI-compatible gateway is the common case and
// provider is a protocol, not a brand.
func TestDriftUnknownProviderFallsBackToOpenAI(t *testing.T) {
	gotBase, gotProvider := Endpoint("http://h:8080", "not-a-known-vendor")
	if gotProvider != ProviderOpenAI {
		t.Fatalf("provider = %q, want the openai fallback (the engine would reject the name)", gotProvider)
	}
	if gotBase != "http://h:8080/v1" {
		t.Fatalf("base = %q", gotBase)
	}
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "OK"}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()
	c, err := NewWithProvider(srv.URL, "k", "m", "not-a-known-vendor")
	if err != nil {
		t.Fatalf("an unknown provider name must build a client, not error: %v", err)
	}
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatalf("probe with an unknown provider name failed: %v", err)
	}
}

// Drift 3: an empty provider with a base ending in /messages. The engine
// (agent/provider/protocol.go, InferFromBaseURL) sniffs the host only
// (api.anthropic.com, anthropic.com, deepseek's /anthropic path) and resolves
// this shape to openai. This package reads a /messages suffix as unmistakably
// Anthropic and switches protocol.
func TestDriftMessagesSuffixImpliesAnthropic(t *testing.T) {
	gotBase, gotProvider := Endpoint("https://gw.test/v1/messages", "")
	if gotProvider != ProviderAnthropic {
		t.Fatalf("provider = %q, want anthropic (the engine would infer openai from the host)", gotProvider)
	}
	if gotBase != "https://gw.test/v1" {
		t.Fatalf("base = %q, want the /messages suffix stripped", gotBase)
	}
}
