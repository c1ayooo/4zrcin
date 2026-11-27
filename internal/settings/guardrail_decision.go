package settings

// Guardrail decision configuration (the operator's top-level `guardrail`
// section in cyber.yaml): who judges a tool call and what happens to the
// verdict.
//
// The policy summary (guardrail_policy.go) reports the whole section read-only.
// The *decision* fields are the one writable piece — provider, mode, on_error,
// shadow and the llm judge subsection — because switching a deployment between
// "no judge", "JEV", "a plain OpenAI-compatible model" and "human review" is a
// console-sized decision, not a text-editing exercise. `provider: llm` needs
// no JEV at all: any OpenAI-compatible /chat/completions endpoint judges.
// The remaining keys (scope, allow_prefixes, review_timeout, timeout,
// concurrency, jev subsection) stay hand-edited and are never touched.
//
// Shape (exts/guardrail/extension.go's declared keys):
//
//	guardrail:
//	  provider: none|jev|llm|hybrid   # none (default) = no judge
//	  mode: auto|safe                 # auto: machine verdicts enforce; safe: review
//	  on_error: allow|review|block    # llm/hybrid only (default review)
//	  shadow: false                   # judge and record, always allow
//	  llm: { base_url, model, api_key }
//
// The value is read by scan at process start: a change is restart-gated like
// every product section.

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// GuardrailDecisionInput is the closed write shape for the decision fields.
// The llm judge's key follows the JEV contract: an empty APIKey keeps the
// stored key (the console cannot echo it) and ClearAPIKey removes it.
type GuardrailDecisionInput struct {
	Provider string `json:"provider"`
	Mode     string `json:"mode"`
	OnError  string `json:"on_error"`
	// Shadow is a tri-state: nil leaves the stored value alone.
	Shadow *bool             `json:"shadow"`
	LLM    GuardrailLLMInput `json:"llm"`
}

// GuardrailLLMInput is the llm judge subsection's write shape.
type GuardrailLLMInput struct {
	BaseURL     string `json:"base_url"`
	Model       string `json:"model"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
}

var guardrailDecisionProviders = map[string]bool{"": true, "none": true, "jev": true, "llm": true, "hybrid": true}
var guardrailDecisionModes = map[string]bool{"": true, "auto": true, "safe": true}
var guardrailDecisionOnError = map[string]bool{"": true, "allow": true, "review": true, "block": true}

// ValidateGuardrailDecision rejects impossible values before anything is
// written: unknown enum values, and a judge selected without its connection —
// provider llm judges through the llm subsection, hybrid still screens with
// it, so both require base_url and model (an already-stored connection counts:
// the keep-on-empty key rule means a rotation save carries only the new key).
func (s *Store) ValidateGuardrailDecision(in GuardrailDecisionInput) error {
	if !guardrailDecisionProviders[strings.ToLower(strings.TrimSpace(in.Provider))] {
		return fmt.Errorf("provider 只允许 none/jev/llm/hybrid（收到 %q）", in.Provider)
	}
	if !guardrailDecisionModes[strings.ToLower(strings.TrimSpace(in.Mode))] {
		return fmt.Errorf("mode 只允许 auto/safe（收到 %q）", in.Mode)
	}
	if !guardrailDecisionOnError[strings.ToLower(strings.TrimSpace(in.OnError))] {
		return fmt.Errorf("on_error 只允许 allow/review/block（收到 %q）", in.OnError)
	}
	if in.LLM.ClearAPIKey && strings.TrimSpace(in.LLM.APIKey) != "" {
		return fmt.Errorf("llm.clear_api_key 与 llm.api_key 不能同时给出")
	}
	if reason := checkJEVBaseURL(strings.TrimSpace(in.LLM.BaseURL)); reason != "" {
		return fmt.Errorf("%s", strings.Replace(reason, "jev.base_url", "guardrail.llm.base_url", 1))
	}
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider == "llm" || provider == "hybrid" {
		if err := s.llmJudgeUsable(provider, in.LLM); err != nil {
			return err
		}
	}
	return nil
}

// llmJudgeUsable reports whether the llm subsection would hold a working
// judge connection after this write. An existing stored connection counts:
// the console cannot echo the key, so "keep what is stored" is the normal
// rotation save.
func (s *Store) llmJudgeUsable(provider string, in GuardrailLLMInput) error {
	_, doc, err := s.readDocument()
	if err != nil {
		return err
	}
	section, err := productMapping(doc, "guardrail")
	if err != nil {
		return err
	}
	stored := func(key string) string {
		if section == nil {
			return ""
		}
		llm := mapGet(section, "llm")
		if llm == nil {
			return ""
		}
		raw := mapGet(llm, key)
		if raw == nil {
			return ""
		}
		return strings.TrimSpace(scalar(raw))
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = stored("base_url")
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		model = stored("model")
	}
	if baseURL == "" || model == "" {
		return fmt.Errorf("provider %s 需要 llm.base_url 与 llm.model（裁判必须有一个可调用的连接）", provider)
	}
	return nil
}

// GuardrailDecisionDiffers reports whether writing in would change the stored
// decision fields.
func (s *Store) GuardrailDecisionDiffers(in GuardrailDecisionInput) (bool, error) {
	_, doc, err := s.readDocument()
	if err != nil {
		return false, err
	}
	section, err := productMapping(doc, "guardrail")
	if err != nil {
		return false, err
	}
	stored := func(key string) string {
		if section == nil {
			return ""
		}
		raw := mapGet(section, key)
		if raw == nil {
			return ""
		}
		return strings.TrimSpace(scalar(raw))
	}
	if strings.TrimSpace(in.Provider) != stored("provider") {
		return true, nil
	}
	if strings.TrimSpace(in.Mode) != stored("mode") {
		return true, nil
	}
	if strings.TrimSpace(in.OnError) != stored("on_error") {
		return true, nil
	}
	if in.Shadow != nil {
		storedShadow := stored("shadow") == "true"
		if *in.Shadow != storedShadow {
			return true, nil
		}
	}
	llm := mapGet(section, "llm")
	llmStored := func(key string) string {
		if llm == nil {
			return ""
		}
		raw := mapGet(llm, key)
		if raw == nil {
			return ""
		}
		return strings.TrimSpace(scalar(raw))
	}
	if strings.TrimSpace(in.LLM.BaseURL) != llmStored("base_url") {
		return true, nil
	}
	if strings.TrimSpace(in.LLM.Model) != llmStored("model") {
		return true, nil
	}
	if in.LLM.ClearAPIKey {
		return llmStored("api_key") != "", nil
	}
	if key := strings.TrimSpace(in.LLM.APIKey); key != "" {
		return key != llmStored("api_key"), nil
	}
	return false, nil
}

// SetGuardrailDecision writes the decision fields into the operator's
// top-level guardrail section under the write lock, comment-preserving, with
// the legacy nested block migrated up first. Scope, allow_prefixes,
// review_timeout, the llm timing keys and the jev subsection stay hand-edited
// and are never touched. An on_error or shadow value is cleared by writing the
// empty string / nil is left alone — only explicit values change keys.
func (s *Store) SetGuardrailDecision(in GuardrailDecisionInput) error {
	if s.harness == "" {
		return fmt.Errorf("没有 harness 配置路径：设置 ZRCIN_HARNESS_CONFIG")
	}
	if err := s.ValidateGuardrailDecision(in); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, doc, err := s.readDocument()
	if err != nil {
		return err
	}
	migrateProductSections(doc)
	section := mapGet(doc, "guardrail")
	if section != nil && section.Kind != yaml.MappingNode {
		return fmt.Errorf("harness 配置的 guardrail 段必须是映射")
	}
	if section == nil {
		section = ensureMapping(doc, "guardrail")
	}
	setOrDelete := func(key, value string) {
		if value == "" {
			mapDelete(section, key)
		} else {
			mapSet(section, key, stringScalar(value))
		}
	}
	setOrDelete("provider", strings.TrimSpace(in.Provider))
	setOrDelete("mode", strings.TrimSpace(in.Mode))
	setOrDelete("on_error", strings.TrimSpace(in.OnError))
	if in.Shadow != nil {
		if *in.Shadow {
			mapSet(section, "shadow", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		} else {
			mapDelete(section, "shadow")
		}
	}
	llm := mapGet(section, "llm")
	if in.LLM.ClearAPIKey || strings.TrimSpace(in.LLM.APIKey) != "" || strings.TrimSpace(in.LLM.BaseURL) != "" || strings.TrimSpace(in.LLM.Model) != "" {
		if llm == nil {
			llm = ensureMapping(section, "llm")
		}
		if llm.Kind != yaml.MappingNode {
			return fmt.Errorf("harness 配置的 guardrail.llm 段必须是映射")
		}
		setOrDeleteLLM := func(key, value string) {
			if value == "" {
				mapDelete(llm, key)
			} else {
				mapSet(llm, key, stringScalar(value))
			}
		}
		setOrDeleteLLM("base_url", strings.TrimSpace(in.LLM.BaseURL))
		setOrDeleteLLM("model", strings.TrimSpace(in.LLM.Model))
		switch {
		case in.LLM.ClearAPIKey:
			mapDelete(llm, "api_key")
		case strings.TrimSpace(in.LLM.APIKey) != "":
			mapSet(llm, "api_key", stringScalar(strings.TrimSpace(in.LLM.APIKey)))
		}
		if len(llm.Content) == 0 {
			mapDelete(section, "llm")
		}
	}
	if len(section.Content) == 0 {
		mapDelete(doc, "guardrail")
	}
	return s.writeHarnessDocument(root)
}
