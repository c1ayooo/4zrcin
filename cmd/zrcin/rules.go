package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/rules"
)

// The rule library is instance-level: rules are tooling, not an engagement,
// so the global shell talks to /api/rules directly (docs/20 §5.4). Every
// route is the operator's; a harness session looks up what the node runs with
// the cyberhub tool instead.

func (h *hub) ruleLibrary(w http.ResponseWriter) *rules.Store {
	if h.rules == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "规则库未配置"})
		return nil
	}
	return h.rules
}

// serveRules lists the library: both layers and, for the instance files, the
// node's report of whether it runs them.
func (h *hub) serveRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	lib := h.ruleLibrary(w)
	if lib == nil {
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit > 500 {
		limit = 500
	}
	// The console's filters name the whole library "all"; the library takes an
	// empty filter for that, so "all" must never reach it as a value.
	kind, origin := r.URL.Query().Get("kind"), r.URL.Query().Get("source")
	if kind == "all" {
		kind = ""
	}
	if origin == "all" {
		origin = ""
	}
	page, matched, err := lib.List(rules.ListOptions{
		Kind:   kind,
		Origin: origin,
		Query:  r.URL.Query().Get("q"),
		Offset: offset,
		Limit:  limit,
	})
	if err != nil {
		rulesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": lib.Dir, "rules": page, "matched": matched})
}

// serveRuleContent returns one rule's text: an instance file by path, a
// built-in by kind and name. Without a source, a path means the instance
// layer and a name means the built-in one.
func (h *hub) serveRuleContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	lib := h.ruleLibrary(w)
	if lib == nil {
		return
	}
	query := r.URL.Query()
	ref := rules.ContentRef{Kind: query.Get("kind"), Origin: query.Get("source"), Path: query.Get("path"), Name: query.Get("name")}
	if ref.Origin == "" {
		if ref.Path != "" {
			ref.Origin = rules.OriginInstance
		} else {
			ref.Origin = rules.OriginBuiltin
		}
	}
	content, err := lib.Content(ref)
	if err != nil {
		rulesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": ref.Kind, "origin": ref.Origin, "path": ref.Path, "name": ref.Name, "content": content,
	})
}

// serveRuleFile writes and removes an instance file. A pasted rule is posted
// without a path or a revision: its content names the file. A rewrite and a
// deletion carry the revision the console read; a mismatch is the console
// working from a stale list, not a race to win.
func (h *hub) serveRuleFile(w http.ResponseWriter, r *http.Request) {
	lib := h.ruleLibrary(w)
	if lib == nil {
		return
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		var in struct {
			Path     string `json:"path"`
			Category string `json:"category"`
			Content  string `json:"content"`
			Revision string `json:"revision"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON：" + err.Error()})
			return
		}
		if in.Path == "" {
			if r.Method == http.MethodPut {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "改写已有规则需要 path"})
				return
			}
			path, err := rules.DerivePath(in.Content, in.Category)
			if err != nil {
				rulesError(w, err)
				return
			}
			in.Path = path
		}
		rows, err := lib.Put(in.Path, in.Content, in.Revision)
		if err != nil {
			rulesError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rules": rows})
	case http.MethodDelete:
		var in struct {
			Path     string `json:"path"`
			Revision string `json:"revision"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON：" + err.Error()})
			return
		}
		if err := lib.Delete(in.Path, in.Revision); err != nil {
			rulesError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST / PUT / DELETE"})
	}
}

// importRules copies rule files into the library. Relative paths resolve
// against the data directory, like every other import.
func (h *hub) importRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	lib := h.ruleLibrary(w)
	if lib == nil {
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON：" + err.Error()})
		return
	}
	p := strings.TrimSpace(in.Path)
	if p == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要 path：规则文件或目录（相对 <data>）"})
		return
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(h.cfg.DataDir, filepath.FromSlash(p))
	}
	res, err := lib.Import(p)
	if err != nil {
		rulesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// serveRulesStatus returns the node's report as it wrote it, and whether the
// node reported at all.
func (h *hub) serveRulesStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	lib := h.ruleLibrary(w)
	if lib == nil {
		return
	}
	state, reported, err := lib.Status()
	if err != nil {
		rulesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reported": reported, "state": state})
}

// rulesError maps the library's refusals: a stale revision is a conflict, a
// missing rule is a 404, and an unconfigured library is a 503.
func rulesError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, rules.ErrRevision):
		status = http.StatusConflict
	case errors.Is(err, rules.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, rules.ErrNotConfigured):
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
