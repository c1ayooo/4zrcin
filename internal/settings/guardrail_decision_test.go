package settings

import (
	"log"
	"os"
	"strings"
	"testing"
)

// The decision write round-trips through the read projection; the llm key
// follows the keep-on-empty contract; scope/allow_prefixes/jev stay untouched.
func TestSetGuardrailDecisionRoundTrip(t *testing.T) {
	seed := "guardrail:\n    provider: jev\n    mode: safe\n    scope: [bash]\n    allow_prefixes: [\"ls \"]\njev:\n    api_key: tsk-1\n"
	st := guardrailStore(t, seed)

	// An llm-only judge: no JEV needed, any OpenAI-compatible endpoint judges.
	in := GuardrailDecisionInput{
		Provider: "llm", Mode: "auto", OnError: "review",
		LLM: GuardrailLLMInput{BaseURL: "https://relay.example/v1", Model: "judge-1", APIKey: "sk-1"},
	}
	if err := st.SetGuardrailDecision(in); err != nil {
		t.Fatalf("llm write: %v", err)
	}
	view := st.GuardrailPolicy()
	if view.Provider != "llm" || view.Mode != "auto" || !view.Configured {
		t.Fatalf("view after write = %+v", view)
	}
	if !view.LLM.APIKeySet || view.LLM.Model != "judge-1" {
		t.Fatalf("llm view = %+v", view.LLM)
	}
	// scope / allow_prefixes / the jev key survive a decision write.
	data, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"scope:", "allow_prefixes:", "tsk-1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("decision write dropped %q:\n%s", want, text)
		}
	}

	// A rotation save with an empty key keeps the stored one.
	if err := st.SetGuardrailDecision(GuardrailDecisionInput{
		Provider: "llm", Mode: "auto",
		LLM: GuardrailLLMInput{BaseURL: "https://relay.example/v1", Model: "judge-1"},
	}); err != nil {
		t.Fatalf("rotation save: %v", err)
	}
	if !st.GuardrailPolicy().LLM.APIKeySet {
		t.Fatal("rotation save wiped the stored key")
	}

	// Hybrid requires an llm connection: on a store that never had one,
	// validation refuses instead of writing a judge that cannot run.
	fresh := guardrailStore(t, "")
	if err := fresh.ValidateGuardrailDecision(GuardrailDecisionInput{
		Provider: "hybrid", Mode: "auto",
	}); err == nil {
		t.Fatal("hybrid without any llm connection must be refused")
	}
	if err := fresh.ValidateGuardrailDecision(GuardrailDecisionInput{
		Provider: "llm", Mode: "auto",
	}); err == nil {
		t.Fatal("llm without any llm connection must be refused")
	}

	// An unknown provider is refused before anything persists.
	if err := st.ValidateGuardrailDecision(GuardrailDecisionInput{Provider: "gpt"}); err == nil {
		t.Fatal("unknown provider must be refused")
	}

	// A legacy nested block is migrated up by the decision write.
	st = guardrailStore(t, "extensions:\n  guardrail:\n    provider: jev\n")
	if err := st.SetGuardrailDecision(GuardrailDecisionInput{
		Provider: "llm", Mode: "auto",
		LLM: GuardrailLLMInput{BaseURL: "https://relay.example/v1", Model: "judge-1"},
	}); err != nil {
		t.Fatalf("migrating write: %v", err)
	}
	data, err = os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "extensions:") {
		t.Fatalf("decision write left the rejected nested layout:\n%s", data)
	}
	if view := st.GuardrailPolicy(); view.Provider != "llm" || !view.LLM.Configured {
		t.Fatalf("view after migrating write = %+v / %+v", view, view.LLM)
	}
}

// 已决 19: a save that carries the legacy spelling to its new home records
// one operator-facing diagnostic. Reads migrate in memory silently.
func TestSetGuardrailDecisionMigrationDiagnostic(t *testing.T) {
	seed := "guardrail:\n    provider: llm\n    mode: auto\n    on_error: review\n    scope: [bash]\n"
	st := guardrailStore(t, seed)

	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	log.SetFlags(0)

	in := GuardrailDecisionInput{
		Provider: "llm", Mode: "auto", OnError: "review",
		LLM: GuardrailLLMInput{BaseURL: "https://relay.example/v1", Model: "judge-1", APIKey: "sk-1"},
	}
	if err := st.SetGuardrailDecision(in); err != nil {
		t.Fatalf("write: %v", err)
	}
	diag := buf.String()
	if !strings.Contains(diag, "判定旋钮已迁入 fence 节") {
		t.Fatalf("fence migration was not diagnosed:\n%s", diag)
	}

	// A second save with nothing left to migrate stays silent.
	buf.Reset()
	if err := st.SetGuardrailDecision(in); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if strings.Contains(buf.String(), "配置迁移") {
		t.Fatalf("steady-state save diagnosed a migration:\n%s", buf.String())
	}
}
