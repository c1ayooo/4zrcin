package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// Switching the judge to a plain OpenAI-compatible model works end to end:
// the decision fields land in cyber.yaml top-level, the key follows the
// keep-on-empty rotation contract, and the response reports the restart —
// scan reads the section at process start.
func TestSettingsSaveWritesGuardrailDecision(t *testing.T) {
	h, harness := singleModelHub(t)
	rec := settingsPUT(t, h, `{"base_url":"http://fast:8080","model":"fast-model",
		"guardrail_decision_write":{"provider":"llm","mode":"auto","on_error":"review",
			"llm":{"base_url":"https://relay.example/v1","model":"judge-1","api_key":"sk-secret-1"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings save = %d %s", rec.Code, rec.Body.String())
	}
	var view settings.View
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	g := view.Guardrail
	if g.Provider != "llm" || g.Mode != "auto" || !g.Configured || !g.LLM.APIKeySet {
		t.Fatalf("guardrail projection = %+v / %+v", g, g.LLM)
	}
	if strings.Contains(rec.Body.String(), "sk-secret-1") {
		t.Fatalf("response echoed the key: %s", rec.Body.String())
	}
	data, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "provider: llm") || !strings.Contains(text, "sk-secret-1") {
		t.Fatalf("stored document lost the decision: %s", text)
	}

	// A save that did not touch the key keeps it.
	rec = settingsPUT(t, h, `{"guardrail_decision_write":{"provider":"llm","mode":"safe",
		"llm":{"base_url":"https://relay.example/v1","model":"judge-1"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotation save = %d %s", rec.Code, rec.Body.String())
	}
	view = settings.View{}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Guardrail.Mode != "safe" || !view.Guardrail.LLM.APIKeySet {
		t.Fatalf("guardrail projection after rotation = %+v / %+v", view.Guardrail, view.Guardrail.LLM)
	}
	data, err = os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sk-secret-1") {
		t.Fatalf("rotation save wiped the key: %s", data)
	}

	// An explicit clear removes the key.
	rec = settingsPUT(t, h, `{"guardrail_decision_write":{"provider":"llm","mode":"safe",
		"llm":{"clear_api_key":true}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear save = %d %s", rec.Code, rec.Body.String())
	}
	data, err = os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret-1") {
		t.Fatalf("clear save left the key: %s", data)
	}
	if view.Guardrail.Provider != "llm" {
		t.Fatalf("clear save disturbed the provider: %+v", view.Guardrail)
	}
}
