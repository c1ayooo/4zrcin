package settings

// Configuration hot update.
//
// Saving a model connection used to restart the local host unconditionally,
// which kills every session on that node. The running harness already owns a
// configuration service: ConfigService/UpdateConfig commits a shared
// (llm/agent/traffic) document and broadcasts a reload to its scans, so a
// model-only save can take effect without a restart. The control plane falls
// back to the restart when the host is offline, refuses the update, or does not
// report the new provider/model within the bounded wait.
//
// Product sections (mcp/search/guardrail/jev) are NOT shared configuration:
// they are read by the scan node from its own runtime file. A save that changes
// one of them therefore needs the restart and never takes the hot path.

// ConnectConfig is the cyber.config.DistributeConfig payload for one model
// connection (shared llm section). The host normalises the single entry and
// picks it as the active profile, matching the flat llm fields the generated
// runtime yaml carries; the values are the same either way, so the hot-applied
// document and the next restart's file cannot diverge.
func (m Model) ConnectConfig() map[string]any {
	entry := map[string]any{
		"provider": m.Provider,
		"base_url": m.BaseURL,
		"api_key":  m.APIKey,
		"model":    m.Model,
	}
	if m.ContextWindow > 0 {
		entry["context_window"] = m.ContextWindow
	}
	if m.MaxTokens > 0 {
		entry["max_tokens"] = m.MaxTokens
	}
	return map[string]any{"llm": map[string]any{"providers": []any{entry}}}
}
