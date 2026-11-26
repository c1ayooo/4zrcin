package api

// The ledger's asset records (spec #1 工单2): a minimal read of the store's
// raw asset rows. It is deliberately not the asset page's aggregate — the
// domain grouping, traffic/vulnerability joins and drill-down data build on
// this in the asset-page work. The legacy /api/assets (host:port read from
// facts) stays untouched until the page replaces it.

import (
	"net/http"

	"github.com/c1ayooo/4zrcin/internal/model"
)

func (s *Server) listAssetRecords(w http.ResponseWriter, r *http.Request) {
	rows := s.Store.ListAssets()
	if rows == nil {
		rows = []model.Asset{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": rows})
}
