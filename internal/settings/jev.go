package settings

// JEV credentials (the guardrail's optional second-stage judge, the operator's
// `extensions.jev` in cyber.yaml).
//
// The guardrail policy summary (guardrail_policy.go) is deliberately
// read-only: the policy's shape is the operator's judgement call, edited in
// the file directly. The JEV *credentials* are different: a key is a secret
// the console can hold but never echo, and retyping it into an editor on every
// rotation is exactly the friction that leaves judges unconfigured. So this
// file owns three keys of the jev section — base_url, api_key, model — with
// the same comment-preserving discipline as the capture and proxy sections.
// Everything else in the section (mode, learning, directory, timeout,
// declaration_effort, compilation_timeout — exts/jev/config.go) stays
// hand-edited and is never touched.
//
// Shape:
//
//	jev:                    # top-level: the layout every harness binary accepts
//	  base_url: https://... # optional; the engine defaults to TypeSafe
//	  api_key: ...          # or the TYPESAFE_API_KEY environment variable
//	  model: ...            # optional judge model override
//
// Like every product section, the file is the storage, not the delivery: a
// scan node replaces its file sections with the Hub's distribution, which
// carries none of them, so the credentials are read by scan at process start.
// A change is restart-gated like the capture section.

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// JEVView is the credentials summary for the guardrail page. The key is never
// copied into the view — Configured plus the routing metadata is everything
// the console may say.
type JEVView struct {
	Configured bool `json:"configured"`
	// BaseURL and Model are the stored routing values as written; empty means
	// the engine default applies (TypeSafe's endpoint, its default model).
	BaseURL string `json:"base_url,omitempty"`
	Model   string `json:"model,omitempty"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// JEV reads the operator's jev credentials. An unreadable file is reported,
// not an error: the settings save reports the real parse error when it tries
// to persist.
func (s *Store) JEV() JEVView {
	view := JEVView{Source: "默认（cyber.yaml 未设置 extensions.jev）"}
	if s == nil || s.harness == "" {
		view.Reason = "没有 harness 配置路径（ZRCIN_HARNESS_CONFIG），状态未知"
		return view
	}
	_, doc, err := s.readDocument()
	if err != nil {
		view.Reason = "无法读取 harness 配置，裁判凭据状态未知"
		return view
	}
	section, err := productMapping(doc, "jev")
	if err != nil {
		view.Reason = err.Error()
		return view
	}
	if section == nil {
		return view
	}
	if key := mapGet(section, "api_key"); key != nil && strings.TrimSpace(scalar(key)) != "" {
		view.Configured = true
		view.Source = "操作员 cyber.yaml 的 jev.api_key（本页可改，密钥本身不回显）"
	}
	if raw := mapGet(section, "base_url"); raw != nil {
		if value := strings.TrimSpace(scalar(raw)); value != "" {
			view.BaseURL = value
		}
	}
	if raw := mapGet(section, "model"); raw != nil {
		if value := strings.TrimSpace(scalar(raw)); value != "" {
			view.Model = value
		}
	}
	if reason := checkJEVBaseURL(view.BaseURL); reason != "" {
		view.Reason = reason
	}
	return view
}

// checkJEVBaseURL reports the first problem with a stored base URL as one
// sentence ("" means fine). The engine's own client is the final authority.
func checkJEVBaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Sprintf("jev.base_url 不是可解析的 URL：%v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Sprintf("jev.base_url 协议 %q 不是 http/https", u.Scheme)
	}
	if u.Host == "" {
		return "jev.base_url 缺少主机地址"
	}
	return ""
}

// JEVInput is the closed write shape for extensions.jev. The console cannot
// echo the stored key, so an empty APIKey keeps it and an explicit ClearAPIKey
// removes it — a save that did not touch the credential can never wipe it.
// BaseURL and Model are not secrets: an empty value clears the key and returns
// the field to the engine default.
type JEVInput struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key"`
	// ClearAPIKey removes the stored key. Combined with a non-empty APIKey it
	// is an ambiguous request and is rejected.
	ClearAPIKey bool `json:"clear_api_key"`
}

// ValidateJEV rejects impossible values before anything is written.
func (s *Store) ValidateJEV(in JEVInput) error {
	if in.ClearAPIKey && strings.TrimSpace(in.APIKey) != "" {
		return fmt.Errorf("clear_api_key 与 api_key 不能同时给出")
	}
	if reason := checkJEVBaseURL(strings.TrimSpace(in.BaseURL)); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

// JEVDiffers reports whether writing in would change the stored jev section
// (raw document comparison, not projection text). The key comparison uses the
// plaintext input against the stored scalar, so an unchanged retyped key is
// "no change" while an untouched empty one is too.
func (s *Store) JEVDiffers(in JEVInput) (bool, error) {
	_, doc, err := s.readDocument()
	if err != nil {
		return false, err
	}
	section, err := productMapping(doc, "jev")
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
	if strings.TrimSpace(in.BaseURL) != stored("base_url") {
		return true, nil
	}
	if strings.TrimSpace(in.Model) != stored("model") {
		return true, nil
	}
	if in.ClearAPIKey {
		return stored("api_key") != "", nil
	}
	if key := strings.TrimSpace(in.APIKey); key != "" {
		return key != stored("api_key"), nil
	}
	return false, nil
}

// SetJEV writes the operator's extensions.jev keys under the write lock,
// comment- and section-preserving, validated by the same rules the read path
// applies. A jev section (and an extensions mapping) that would be left empty
// is removed, so no empty husk survives in the file.
func (s *Store) SetJEV(in JEVInput) error {
	if s.harness == "" {
		return fmt.Errorf("没有 harness 配置路径：设置 ZRCIN_HARNESS_CONFIG")
	}
	if err := s.ValidateJEV(in); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, doc, err := s.readDocument()
	if err != nil {
		return err
	}
	// A legacy nested extensions.jev block moves up first: the nested layout
	// is rejected by every harness binary, and its other keys survive the move.
	migrateProductSections(doc)
	section := mapGet(doc, "jev")
	if section != nil && section.Kind != yaml.MappingNode {
		return fmt.Errorf("harness 配置的 jev 段必须是映射")
	}
	if section == nil {
		section = ensureMapping(doc, "jev")
	}
	if value := strings.TrimSpace(in.BaseURL); value == "" {
		mapDelete(section, "base_url")
	} else {
		mapSet(section, "base_url", stringScalar(value))
	}
	if value := strings.TrimSpace(in.Model); value == "" {
		mapDelete(section, "model")
	} else {
		mapSet(section, "model", stringScalar(value))
	}
	switch {
	case in.ClearAPIKey:
		mapDelete(section, "api_key")
	case strings.TrimSpace(in.APIKey) != "":
		mapSet(section, "api_key", stringScalar(strings.TrimSpace(in.APIKey)))
	}
	if len(section.Content) == 0 {
		mapDelete(doc, "jev")
	}
	return s.writeHarnessDocument(root)
}
