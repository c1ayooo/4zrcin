package api

// The instance fence rides every turn the project dispatches (spec #1 工单3),
// exactly like the project scope rules it sits above: the marshaled settings
// fence travels as the 4zrcin.GlobalFence fork extension. The hub supplies
// the read callback (the fence is instance state, not project state); a bare
// test server leaves it nil and the turn carries no extension — byte-identical
// to before this existed.

import (
	"encoding/json"
	"log"
)

// globalFenceJSON marshals the instance fence for the wire. The stored shape
// is the normalized shape (internal/settings normalizes on save), so this
// marshals as stored — no second schema to drift. An unset or empty fence
// sends nothing.
func (s *Server) globalFenceJSON() json.RawMessage {
	if s.GlobalFence == nil {
		return nil
	}
	fence := s.GlobalFence()
	if fence.IsEmpty() {
		return nil
	}
	raw, err := json.Marshal(fence)
	if err != nil {
		// A struct of strings and bools cannot fail to marshal; logging keeps
		// the impossible visible instead of silently stripping the red lines.
		log.Printf("instance fence 序列化失败（不应发生）：%v", err)
		return nil
	}
	return raw
}
