package api

import (
	"encoding/json"
	"log"
)

// scopeRulesJSON reads the project's normalized test-range rule set for the
// wire (executor.Request.ScopeRules → 4zrcin.ScopeRules extension). The
// manifest JSON tags are the wire contract, so the struct marshals as stored —
// no translation, no second shape to drift.
//
// A configured set rides every turn regardless of Enabled: the flag is inside
// the payload, and the fork must see a disabled set too, or a session that
// outlives the save would keep enforcing the last enabled rules. Only a
// project that never configured rules sends nothing (byte-identical wire).
func (s *Server) scopeRulesJSON() json.RawMessage {
	if s.Project == nil {
		return nil
	}
	rules, ok := s.Project.ScopeRulesOf()
	if !ok {
		return nil
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		// A struct of strings and bools cannot fail to marshal; logging keeps
		// the impossible visible instead of silently stripping the audit range.
		log.Printf("project %s: scope_rules 序列化失败（不应发生）：%v", s.Project.ID, err)
		return nil
	}
	return raw
}
