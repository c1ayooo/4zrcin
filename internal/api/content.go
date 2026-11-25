package api

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/assets"
	"github.com/c1ayooo/4zrcin/internal/bundle"
	"github.com/c1ayooo/4zrcin/internal/pocs"
	"github.com/c1ayooo/4zrcin/internal/store"
	"github.com/c1ayooo/4zrcin/internal/traffic"
)

// ---- traffic ----

// importTraffic brings one HAR capture into the ledger. It is synchronous and
// cheap: no model call, just parse, copy the artifact and derive host facts
// (docs/09-traffic.md).
func (s *Server) importTraffic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeErr(w, 400, "需要 path：HAR 文件在服务器上的位置")
		return
	}
	im := &traffic.Importer{Store: s.Store, CaseRoot: s.CaseRoot}
	res, err := im.Import(req.Path)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// importAssets ingests the harness's own scan results (gogo/neutron JSONL) as
// service and host facts. No separate asset schema: the scanner's output *is*
// the schema (docs/10-assets.md).
func (s *Server) importAssets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeErr(w, 400, "需要 path：扫描结果文件（相对案例目录）")
		return
	}
	im := &assets.Importer{Store: s.Store, CaseRoot: s.CaseRoot}
	res, err := im.Import(req.Path)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// ---- assets view ----

// listAssets groups the observational facts into "host -> ports". The ledger
// keeps facts flat and the report stays a vulnerability snapshot; an inventory
// is a reading of the facts for humans (docs/10-assets.md).
func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	type portView struct {
		Port    string `json:"port"`
		Summary string `json:"summary,omitempty"`
	}
	type hostView struct {
		Host  string     `json:"host"`
		Ports []portView `json:"ports"`
	}
	byHost := map[string]*hostView{}
	var order []string
	ensure := func(name string) *hostView {
		h, ok := byHost[name]
		if !ok {
			h = &hostView{Host: name, Ports: []portView{}}
			byHost[name] = h
			order = append(order, name)
		}
		return h
	}
	for _, f := range s.Store.ListFacts("", 0) {
		// A withdrawn or rejected service is not part of the observed surface.
		if f.Unusable() {
			continue
		}
		switch f.Kind {
		case "host":
			ensure(f.Title)
		case "service":
			host, port, ok := splitService(f.Title)
			if !ok {
				continue
			}
			ensure(host).Ports = append(ensure(host).Ports, portView{Port: port, Summary: f.Summary})
		}
	}
	sort.Strings(order)
	out := make([]hostView, 0, len(order))
	for _, name := range order {
		h := byHost[name]
		sort.Slice(h.Ports, func(i, j int) bool { return h.Ports[i].Port < h.Ports[j].Port })
		out = append(out, *h)
	}
	writeJSON(w, 200, map[string]any{"hosts": out})
}

// splitService turns "10.0.0.5:8080" (or "[::1]:22") into host and port.
func splitService(title string) (string, string, bool) {
	if h, p, err := net.SplitHostPort(title); err == nil && p != "" {
		return h, p, true
	}
	if i := strings.LastIndex(title, ":"); i > 0 && i < len(title)-1 {
		return title[:i], title[i+1:], true
	}
	return "", "", false
}

// ---- pocs ----

// listPOCs is the template library view. Templates are just files in one
// directory: the executor points neutron at it with -t (docs/13-pocs.md).
// q/offset/limit page the library server-side; without them the whole list is
// returned for older callers.
func (s *Server) listPOCs(w http.ResponseWriter, r *http.Request) {
	if s.POCs == nil {
		writeErr(w, 503, "POC 库未配置")
		return
	}
	q := r.URL.Query().Get("q")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit > 500 {
		limit = 500
	}
	all := s.POCs.List()
	page, matched := pocs.Page(all, q, offset, limit)
	writeJSON(w, 200, map[string]any{
		"dir":     s.POCs.Dir,
		"pocs":    page,
		"total":   len(all),
		"matched": matched,
	})
}

// importPOCs copies a nuclei/neutron template file or directory into the
// library. Relative paths resolve against the case root, like every other
// import (docs/13-pocs.md).
func (s *Server) importPOCs(w http.ResponseWriter, r *http.Request) {
	if s.POCs == nil {
		writeErr(w, 503, "POC 库未配置")
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeErr(w, 400, "需要 path：模板文件或目录（相对案例目录）")
		return
	}
	if !filepath.IsAbs(req.Path) {
		req.Path = filepath.Join(s.CaseRoot, filepath.FromSlash(req.Path))
	}
	res, err := s.POCs.Import(req.Path)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// pocDir returns the library path for the dispatch brief, or "" when empty.
func (s *Server) pocDir() string {
	if s.POCs == nil || s.POCs.Dir == "" {
		return ""
	}
	if len(s.POCs.List()) == 0 {
		return ""
	}
	return s.POCs.Dir
}

// ---- bundles ----

// exportBundle writes one archive to data/bundles. Exporting to the server side
// (instead of streaming) keeps a 2 GB case from living in a request body; the
// returned name is downloadable through GET /api/bundles/{name}.
func (s *Server) exportBundle(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.DataDir, "bundles")
	name := "case-" + time.Now().Format("20060102-150405") + ".zip"
	out := filepath.Join(dir, name)
	// The archive carries the instance state that is invisible in project data:
	// a project can be premise_policy=verified_only while this instance runs no
	// verifier at all, and reading only the policy afterwards would claim a
	// stricter run than happened (2026-11-22 export).
	m, err := bundle.ExportLedger(s.CaseRoot, s.Store, out, bundle.RuntimeFacts{
		ReviewEnabled:      !s.ReviewsDisabled(),
		ReviewBootDisabled: s.ReviewBootDisabled(),
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var size int64
	if st, err := os.Stat(out); err == nil {
		size = st.Size()
	}
	writeJSON(w, 200, map[string]any{
		"name": name, "path": out, "size": size,
		"files": len(m.Files), "missing": len(m.Missing), "counts": m.Counts,
	})
}

// importBundle verifies the whole archive before extracting it. The result is a
// self-contained directory: importing never merges into the running ledger,
// because a merge would need conflict rules nobody has written (docs/08, D28).
func (s *Server) importBundle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeErr(w, 400, "需要 path：bundle 文件名或相对数据目录的路径")
		return
	}
	dest := filepath.Join(s.DataDir, "imports", store.NewID("imp"))
	m, err := bundle.Import(s.resolveBundlePath(req.Path), dest)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"dir": dest, "manifest": m})
}

func (s *Server) getBundle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeErr(w, 404, "bundle not found")
		return
	}
	p := filepath.Join(s.DataDir, "bundles", name)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		writeErr(w, 404, "bundle not found")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, p)
}

// autoVerify starts at most one batch of automatic reviews for one intent. The
// candidate set is the enumerable store queue (ReviewCandidates), not "the
// newest 50 facts": a direction with more than 50 observations would otherwise
// push an old vuln/cred out of every automatic pass (F11).
//
// The batch is a per-call bound; the durable cursor plus wrap-around keeps every
// candidate reachable across calls and restarts. The cursor is the last candidate
// this pass handled, so a pause/delete/shutdown leaves it there and a resume
// starts with the first untouched candidate; a busy fact is skipped (the ring
// revisits it), and a review that ends without a usable verdict records a short
// cooldown instead of spending another model run on every wake.
