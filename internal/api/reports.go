package api

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/board"
	"github.com/c1ayooo/4zrcin/internal/evidence"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/report"
)

// ---- reports ----

// createReport starts one report run. Generating a report is a real harness run,
// so the caller gets 202 and polls the list; preflight failures are answered
// synchronously because they need a fix in the ledger, not a poll.
func (s *Server) createReport(w http.ResponseWriter, r *http.Request) {
	// A paused project cancels the generation the moment it starts, so an
	// operator who fires the button on a paused (often already closed) project
	// would watch two 202s die in the log. The preflight says so now, with the
	// pause cause and the way out: the report needs the project un-paused, and
	// a closed engagement unlocks through append-goal (measured 2026-11-27,
	// E10: two queued reports died as 取消（pause） before this check). It
	// comes before the harness check because it is about the project, not the
	// report machinery.
	if s.paused.Load() {
		s.controlMu.Lock()
		cause := s.pauseCause
		s.controlMu.Unlock()
		msg := "项目已暂停，报告生成会被立即取消：先恢复项目再触发"
		if cause == "completion_standby" || cause == "commander_standby" {
			msg = "项目已收口暂停（" + cause + "）：报告生成需要项目在跑——通过「追加目标」解锁后（POST /api/project/append-goal）再触发报告"
		} else if cause != "" {
			msg = "项目已暂停（" + cause + "）：恢复项目后再触发报告"
		}
		writeErr(w, 409, msg)
		return
	}
	if s.Reports == nil {
		writeErr(w, 503, "报告未启用（未找到 harness）")
		return
	}
	if err := s.Reports.Preflight(); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	// Generate() refuses to start while another run owns the generator, but that
	// refusal happened inside the goroutine, after this handler had already
	// answered 202. Check the same lock here so "queued" is true.
	if s.Reports.Busy() {
		writeErr(w, 409, "已有报告在生成中")
		return
	}
	go func() {
		if _, err := s.Reports.Generate(s.Ctx); err != nil {
			log.Printf("report: %v", err)
		}
	}()
	writeJSON(w, 202, map[string]any{"queued": true})
}

// listReports returns every report, with the staleness recomputed: a report is
// only as good as the ledger it was written from (docs/08, D27).
func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	cur := s.contentDigest()
	list := report.List(s.CaseRoot)
	for i := range list {
		list[i].Status = effectiveStatus(list[i], cur)
	}
	writeJSON(w, 200, map[string]any{"reports": list})
}

// effectiveStatus is the status a reader must act on, not the one written when
// the report was generated. A report written against a ledger that has since
// moved no longer matches it, and that is stale from then on. The list, the
// detail view and the file handler read this one function, so a report cannot be
// listed as stale and still be served as fresh.
func effectiveStatus(m report.Meta, digest string) string {
	if m.Status == report.StatusReady && m.SourceDigest != "" && m.SourceDigest != digest {
		return report.StatusStale
	}
	return m.Status
}

// contentDigest is the ledger fingerprint reports are compared against.
func (s *Server) contentDigest() string {
	return board.ContentDigest(board.Build(s.Store, 20))
}

// unreportedLimit caps the rows the precheck returns; total is still the whole
// set, so the console can say "N more" without paging.
const unreportedLimit = 20

// unreportedNote states the boundary where the data is produced, because the
// figure is easy to over-read: a report cites a fact id, not a claim version, so
// a conclusion re-declared after the report still counts as cited, and a stale
// report still counts. Only a report that passed its own checks counts: a draft
// or a report whose validation failed is not a delivered citation, and letting
// it vouch for its facts would hide exactly the gap this view exists to show.
// The projection is a set difference, not a coverage claim.
const unreportedNote = "只按已通过校验（status=ready）报告的 findings.json 里的 fact id 算差：命中即算已入，不判断该报告是否已过期，也不判断引用的是否为当前断言版本；没通过校验的报告的引用不算已入，单独列在 uncounted；没出现在名单里不代表结论未复核或报告有误。"

// unreportedFact is one verified conclusion no report cites.
type unreportedFact struct {
	ID    string `json:"fact"`
	Title string `json:"title"`
}

// reportPrecheck answers "what would the report contain right now" without
// spending a model run: how many findings have been verified, how many are still
// unverified, and which verified ones no report cites yet. The console asks
// before writing a report that would carry candidates — the report is still
// legal, but the operator should choose that knowingly (D100).
func (s *Server) reportPrecheck(w http.ResponseWriter, r *http.Request) {
	db := s.Store.Snapshot()
	reportable, unverified, rejected, withdrawn := 0, 0, 0, 0
	verified := make([]model.Fact, 0, len(db.Facts))
	for _, f := range db.Facts {
		switch f.Kind {
		case "vuln", "cred":
		default:
			continue
		}
		switch f.Status {
		case "withdrawn":
			withdrawn++
			continue
		case "rejected":
			rejected++
			continue
		}
		reportable++
		switch f.Status {
		case "unverified":
			unverified++
		case "verified":
			verified = append(verified, f)
		}
	}
	cited, unreadable, uncounted := s.reportFactCitations()
	sort.Slice(verified, func(i, j int) bool { return verified[i].ID < verified[j].ID })
	unreported := make([]unreportedFact, 0, len(verified))
	for _, f := range verified {
		if cited[f.ID] {
			continue
		}
		unreported = append(unreported, unreportedFact{ID: f.ID, Title: f.Title})
	}
	total := len(unreported)
	if len(unreported) > unreportedLimit {
		unreported = unreported[:unreportedLimit]
	}
	writeJSON(w, 200, map[string]any{
		"reportable": reportable,
		"unverified": unverified,
		"rejected":   rejected,
		"withdrawn":  withdrawn,
		"running":    s.Reports != nil && s.Reports.Busy(),
		"unreported": map[string]any{
			"total":      total,
			"limit":      unreportedLimit,
			"items":      unreported,
			"note":       unreportedNote,
			"unreadable": unreadable,
			"uncounted":  uncounted,
		},
	})
}

// reportFactCitations is the read-only half of the precheck: the fact ids a
// delivered report's findings.json cites, the ids of reports whose rows could not
// be read, and the ids of reports whose rows were readable but do not count.
//
// A report that cannot be read is not treated as citing nothing — that would
// report its facts as unreported when the opposite may be true — so it is named.
// A report that did not pass its own checks is the harder case: its directory is
// not a deliverable (serveReportFile refuses it), so its rows cannot prove a
// fact reached a report. Counting them would let a failed check vouch for the
// very facts it failed on, so they are listed separately instead of silently
// dropped. A draft without rows yet is normal and not an error.
func (s *Server) reportFactCitations() (cited map[string]bool, unreadable, uncounted []string) {
	cited = map[string]bool{}
	unreadable = make([]string, 0)
	uncounted = make([]string, 0)
	for _, m := range report.List(s.CaseRoot) {
		dir, ok := s.reportDir(m.ID)
		if !ok {
			unreadable = append(unreadable, m.ID)
			continue
		}
		rows, err := report.ReadFindings(dir)
		if err != nil {
			if m.Status != report.StatusDraft {
				unreadable = append(unreadable, m.ID)
			}
			continue
		}
		if m.Status != report.StatusReady {
			uncounted = append(uncounted, m.ID)
			continue
		}
		for _, row := range rows {
			if row.Fact != "" {
				cited[row.Fact] = true
			}
		}
	}
	sort.Strings(unreadable)
	sort.Strings(uncounted)
	return cited, unreadable, uncounted
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.reportDir(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "report not found")
		return
	}
	m, err := report.ReadMeta(dir)
	if err != nil {
		writeErr(w, 404, "report not found")
		return
	}
	m.Status = effectiveStatus(m, s.contentDigest())
	writeJSON(w, 200, m)
}

// serveReportFile serves files inside one report dir. Containment is checked by
// the report package, the same rule that validates evidence links.
//
// The directory exists while a report is still failing its own checks, so the
// status decides what may leave: prose, rows and evidence copies are the
// deliverable, and a report that did not pass validation is not one. checks.json
// is the exception in every state — it is how the operator sees which rule
// failed, and refusing it would hide the reason behind the refusal.
func (s *Server) serveReportFile(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.reportDir(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "report not found")
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		rel = report.IndexFile
	}
	abs, ok := report.SafePath(dir, rel)
	if !ok {
		writeErr(w, 403, "路径越界")
		return
	}
	meta, err := report.ReadMeta(dir)
	if err != nil {
		writeErr(w, 409, "报告尚未就绪：report.json 缺失或不可读")
		return
	}
	status := meta.Status
	if meta.Status == report.StatusReady && meta.SourceDigest != "" {
		status = effectiveStatus(meta, s.contentDigest())
	}
	if status != report.StatusReady && filepath.Clean(rel) != report.ChecksFile {
		writeErr(w, 409, reportNotDeliverable(meta, status))
		return
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		writeErr(w, 404, "文件不存在："+rel)
		return
	}
	// The report directory is written by a model run and may contain copies of
	// attacker-influenced evidence (captured HTML, SVG, XML). It is served from
	// the console's own origin, so it needs the same treatment as an evidence
	// artifact: markdown keeps its text type, a browser-rendered document is
	// pinned and CSP-sandboxed, and nothing is ever sniffed into a document.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.EqualFold(filepath.Ext(rel), ".md") {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	} else if ct, ok := evidence.DocumentType("", abs); ok {
		w.Header().Set("Content-Type", ct)
		// sandbox gives the document an opaque origin: a captured page cannot
		// run script with the operator's console cookie.
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	http.ServeFile(w, r, abs)
}

// reportNotDeliverable explains why a report's body is not served. A bare 409
// would leave the operator guessing; the failed rule names are the useful part,
// and checks.json carries them in full.
func reportNotDeliverable(m report.Meta, status string) string {
	switch status {
	case report.StatusDraft:
		// checks.json is written only after the run stops and the checks run, so
		// naming it here would send the operator to a 404. A placeholder file
		// would be worse: an empty check list reads as "nothing failed".
		return "报告还在生成中（draft），正文与证据尚未产出；校验结果要等生成结束才写出，现在没有可查看的内容"
	case report.StatusStale:
		return "报告写完之后账本已变化（stale），正文与证据不再对应当前账本，只能查看 checks.json；重新生成报告后再交付"
	}
	failed := make([]string, 0, len(m.Checks))
	for _, c := range m.Checks {
		if !c.OK {
			failed = append(failed, c.Rule)
		}
	}
	msg := "报告校验未通过（" + status + "），正文与证据不对外提供，只能查看 checks.json"
	if len(failed) > 0 {
		if len(failed) > 4 {
			failed = failed[:4]
		}
		msg += "；未通过的规则：" + strings.Join(failed, "、")
	}
	return msg
}

func (s *Server) reportDir(id string) (string, bool) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", false
	}
	return filepath.Join(s.CaseRoot, report.Dir, id), true
}

// resolveBundlePath accepts a bare file name (under data/bundles) or a path
// relative to the data directory. Absolute paths still work, but the docs ask
// for relative ones so a command can be pasted on another machine.
func (s *Server) resolveBundlePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	named := filepath.Join(s.DataDir, "bundles", filepath.FromSlash(p))
	if !strings.ContainsAny(p, `/\`) {
		if _, err := os.Stat(named); err == nil {
			return named
		}
	}
	return filepath.Join(s.DataDir, filepath.FromSlash(p))
}
