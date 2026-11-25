package api

import (
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/c1ayooo/4zrcin/internal/evidence"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/retrieval"
)

// ---- reads ----

func (s *Server) listFacts(w http.ResponseWriter, r *http.Request) {
	out, err := s.searchFacts(r)
	if err != nil {
		retrievalError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// getFact is the depth path for one fact: the board carries only title and
// summary, so reading doc + evidence is an explicit act.
func (s *Server) getFact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, version, ok := s.Store.FactVersion(id)
	if !ok {
		writeErr(w, 404, "fact not found")
		return
	}
	if harnessSession(r) {
		facts := []model.Fact{f}
		if s.ReviewsDisabled() {
			// Reviews are closed at runtime (--no-verify at boot, or the operator's
			// switch): an unverified claim keeps its text. Withdrawn, rejected,
			// missing and re-stated rows are still redacted.
			s.Store.ProjectSessionFactsVisible(facts)
		} else {
			s.Store.ProjectSessionFacts(facts)
		}
		writeJSON(w, 200, map[string]any{"fact": facts[0], "version": version, "evidence": []model.Evidence{}, "reviews": []model.Review{}})
		return
	}
	want := make(map[string]bool, len(f.EvidenceIDs))
	for _, eid := range f.EvidenceIDs {
		want[eid] = true
	}
	evs := make([]model.Evidence, 0, len(want))
	for _, e := range s.Store.Evidence() {
		if want[e.ID] {
			evs = append(evs, e)
		}
	}
	// A reviewer reads originals to recompute, not the verdicts others wrote:
	// seeing another reviewer's reasoning turns independent recomputation into
	// agreement (D102). The reviews of even its own target fact stay absent.
	reviews := s.Store.Reviews(id)
	if IsReviewerSurface(r.Context()) {
		reviews = []model.Review{}
	}
	writeJSON(w, 200, map[string]any{"fact": f, "version": version, "evidence": evs, "reviews": reviews})
}

// listPlaybooks is the console's read view of the project's knowledge table.
// It is separate from /api/facts on purpose: a playbook is knowledge, not a
// finding, so it has its own endpoint and its own vocabulary (D70).
func (s *Server) listPlaybooks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := retrieval.Playbooks(s.Store, retrieval.PlaybookQuery{Q: q.Get("q"), Tag: q.Get("tag"), Status: q.Get("status"), Archived: q.Get("archived"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		retrievalError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// retirePlaybook is operator/commander curation: the text stays, recall stops
// using it. A wrong tactic is a lesson, so it is retired rather than deleted.
func (s *Server) retirePlaybook(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.RetirePlaybook(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) listEvidence(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"evidence": s.Store.Evidence()})
}

// evidenceDocumentType is the evidence package's answer for one ledger row: the
// row's declared Mime plus the extension of the file it resolves to. The rule
// lives there because the vulnerability library serves the same bytes from the
// same origin and must not grow a second copy of it.
func evidenceDocumentType(e model.Evidence, abs string) (string, bool) {
	return evidence.DocumentType(e.Mime, abs)
}

// evidenceDownloadName is the operator-facing name for a saved artifact. URI is
// the only guaranteed field (slash-separated, relative to the case root), so it
// backs up a row whose display name was never filled in.
func evidenceDownloadName(e model.Evidence) string {
	if e.Name != "" {
		return e.Name
	}
	return path.Base(e.URI)
}

// serveEvidence streams the original artifact. The URI is stored relative to
// the case root; anything that resolves outside it is refused (evidence.Path).
//
// Evidence bytes are attacker-influenced and served from the console's own
// origin, so the default is a preview, not a download: inline, never sniffed,
// and CSP sandbox for anything the browser would render as a document. Saving
// the artifact is a separate, explicit intent (?download=1) — with a browser
// that can no longer infer a type (777 of 874 real rows are octet-stream),
// "inline" alone would still make the click download; explicit preview headers
// put the operator back in control.
func (s *Server) serveEvidence(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	e, ok := s.Store.EvidenceByID(id)
	if !ok {
		writeErr(w, 404, "evidence not found")
		return
	}
	abs, err := evidence.Path(s.CaseRoot, e)
	switch {
	case errors.Is(err, evidence.ErrOutside):
		writeErr(w, 403, "证据路径越界")
		return
	case err != nil:
		writeErr(w, 404, "证据文件已不存在："+e.URI)
		return
	}
	// D150: the row records the digest of the bytes it admitted. A preserved
	// copy that no longer hashes to it is not this proof — serving it would let
	// a later write masquerade as the original evidence. Refuse with a distinct
	// status instead of a 404 (the file is there; its content is wrong).
	if err := evidence.Verify(s.CaseRoot, e); err != nil {
		switch {
		case errors.Is(err, evidence.ErrDigestMismatch), errors.Is(err, evidence.ErrCorrupt):
			writeErr(w, http.StatusConflict, "证据内容与登记摘要不符，已拒绝提供："+err.Error())
		case errors.Is(err, evidence.ErrMissing):
			writeErr(w, 404, "证据文件已不存在："+e.URI)
		case errors.Is(err, evidence.ErrOutside):
			writeErr(w, 403, "证据路径越界")
		default:
			writeErr(w, 500, "读取证据失败："+err.Error())
		}
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		// FormatMediaType is the single writer of this header: it emits
		// filename* (RFC 2231/5987) for non-ASCII names and quotes specials,
		// so no bare filename may be added next to it.
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": evidenceDownloadName(e)}))
	} else {
		w.Header().Set("Content-Disposition", "inline")
		// The console's preview sheet frames PDFs. The instance-wide
		// X-Frame-Options: DENY (cmd/tw withSecurityHeaders) would block that
		// same-origin frame; this handler writes into the same response header
		// map, so this endpoint — and only this one — relaxes it to SAMEORIGIN.
		// Console pages keep DENY, and document types are CSP-sandboxed below.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		// Pin the document type instead of letting ServeFile re-derive it from
		// the extension, and sandbox the preview: same-origin evidence must
		// never run script in the console's origin (nosniff alone only stops
		// the blob from being misinterpreted, not an actual HTML/SVG document).
		if ct, ok := evidenceDocumentType(e, abs); ok {
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Security-Policy", "sandbox")
		}
	}
	http.ServeFile(w, r, abs)
}
