package main

import "net/http"

func (h *hub) serveModelCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, 405, map[string]any{"error": "只支持 POST"})
		return
	}
	writeJSON(w, 200, h.settings.CheckModel(r.Context()))
}
