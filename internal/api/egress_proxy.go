package api

import (
	"encoding/json"
	"log"
)

// egressProxyJSON reads the project's normalized egress proxy for the wire
// (executor.Request.EgressProxy → 4zrcin.EgressProxy extension). The manifest
// JSON tags are the wire contract, so the struct marshals as stored — no
// translation, no second shape to drift.
//
// A configured proxy rides every turn regardless of Enabled: the flag is
// inside the payload, and the fork must see a disabled one too, or a session
// that outlives the save would keep routing through the old endpoint. Only a
// project that never configured a proxy sends nothing (byte-identical wire).
func (s *Server) egressProxyJSON() json.RawMessage {
	if s.Project == nil {
		return nil
	}
	proxy, ok := s.Project.ProxyOf()
	if !ok {
		return nil
	}
	raw, err := json.Marshal(proxy)
	if err != nil {
		// A struct of strings, ints and bools cannot fail to marshal; logging
		// keeps the impossible visible instead of silently dropping the route.
		log.Printf("project %s: proxy 序列化失败（不应发生）：%v", s.Project.ID, err)
		return nil
	}
	return raw
}
