package api

import (
	"net/http"
)

// phase1Summary is the Phase 1 read-only projection. It is deliberately not a
// ledger endpoint: the sidecar records live outside objects/versions so reading
// them cannot bump a delivery cursor or change a run.
func (s *Server) phase1Summary(w http.ResponseWriter, r *http.Request) {
	if s.Metrics == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"reason":  "sidecar 不可用（项目目录为空或 metrics/ 不可写）",
		})
		return
	}
	out := s.Metrics.Summary()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"summary": out,
	})
}
