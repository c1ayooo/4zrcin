package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/store"
)

// documentView is one of the three project documents as the console reads it.
// Revision 0 means "as loaded, never edited through this API" — the text still
// comes from the project (scope, constraint) or the goal object, so the console
// always has something to show and a first save is unambiguous.
type documentView struct {
	Text      string    `json:"text"`
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	GoalID    string    `json:"goal_id,omitempty"`
}

// documentRevision is one row of a document's history. The field names are the
// contract (revision/at/by/text/diff), not documentView's: the console renders
// history rows directly, and diff is omitted until a diff is actually computed.
type documentRevision struct {
	Revision uint64    `json:"revision"`
	At       time.Time `json:"at"`
	By       string    `json:"by"`
	Text     string    `json:"text"`
	Diff     string    `json:"diff,omitempty"`
}

// currentGoalDocument resolves the one editable goal: the earliest open goal by
// creation time. More than one open goal is possible (the commander may append),
// and the console says so without editing a different one here.
func (s *Server) currentGoalDocument() (model.Goal, bool) {
	open := []model.Goal{}
	for _, g := range s.Store.Goals() {
		if !g.Closed {
			open = append(open, g)
		}
	}
	if len(open) == 0 {
		return model.Goal{}, false
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].CreatedAt.Before(open[j].CreatedAt) })
	return open[0], true
}

func (s *Server) documentsView() map[string]documentView {
	out := map[string]documentView{}
	fallback := func(kind, text string) documentView {
		if d, ok := s.Store.Document(kind); ok {
			return documentView{Text: d.Text, Revision: d.Revision, UpdatedAt: d.UpdatedAt, UpdatedBy: d.UpdatedBy}
		}
		return documentView{Text: text}
	}
	out[store.DocumentScope] = fallback(store.DocumentScope, s.scopeText())
	out[store.DocumentConstraints] = fallback(store.DocumentConstraints, s.Constraints())
	if g, ok := s.currentGoalDocument(); ok {
		out[store.DocumentGoal] = documentView{Text: g.Statement, GoalID: g.ID, Revision: s.Store.GoalRevision(g.ID)}
	}
	return out
}

func (s *Server) listDocuments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"documents": s.documentsView()})
}

func (s *Server) documentHistory(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !store.ValidDocumentKind(kind) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知文档：" + kind, "code": "document_unknown"})
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > 200 {
			writeErr(w, http.StatusBadRequest, "limit 必须是 1–200")
			return
		}
		limit = n
	}
	revisions := []documentRevision{}
	if kind == store.DocumentGoal {
		// The goal document's history is the goal row's own version journal; the
		// document kind has no rows of its own (the statement is the only copy).
		if g, ok := s.currentGoalDocument(); ok {
			for _, d := range s.Store.GoalDocumentHistory(g.ID) {
				revisions = append(revisions, documentRevision{Text: d.Text, Revision: d.Revision, At: d.UpdatedAt})
			}
		}
		sort.SliceStable(revisions, func(i, j int) bool { return revisions[i].Revision > revisions[j].Revision })
		if len(revisions) > limit {
			revisions = revisions[:limit]
		}
		writeJSON(w, http.StatusOK, map[string]any{"revisions": revisions})
		return
	}
	for _, c := range s.Store.History("document", kind) {
		var d model.Document
		if err := json.Unmarshal(c.Value, &d); err != nil {
			continue
		}
		revisions = append(revisions, documentRevision{Text: d.Text, Revision: d.Revision, At: d.UpdatedAt, By: d.UpdatedBy})
	}
	sort.SliceStable(revisions, func(i, j int) bool { return revisions[i].Revision > revisions[j].Revision })
	if len(revisions) > limit {
		revisions = revisions[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revisions})
}

// saveDocument is the single write entry for all three documents. It compares
// the caller's revision before anything else, so a console that read an older
// version cannot overwrite a newer edit — including an edit made through the
// goal editor.
func (s *Server) saveDocument(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !store.ValidDocumentKind(kind) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知文档：" + kind, "code": "document_unknown"})
		return
	}
	var req struct {
		Text     string `json:"text"`
		Revision uint64 `json:"revision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	goalID := ""
	if kind == store.DocumentGoal {
		g, ok := s.currentGoalDocument()
		if !ok {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "没有未收口的目标可写", "code": "document_no_open_goal"})
			return
		}
		goalID = g.ID
	}
	var doc model.Document
	var err error
	if kind == store.DocumentGoal {
		doc, err = s.Store.SaveGoalDocument(req.Revision, goalID, req.Text, "", "", "", collab.OperatorID)
	} else {
		doc, err = s.Store.SaveDocument(req.Revision, kind, req.Text, collab.OperatorID)
	}
	if errors.Is(err, store.ErrDocumentConflict) {
		current := s.documentsView()[kind]
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "文档已被改过：刷新看当前版再改",
			"code":    "document_conflict",
			"current": map[string]any{"revision": current.Revision, "updated_at": current.UpdatedAt},
		})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyDocument(kind, goalID, req.Text); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAction("document_save", kind, req.Text)
	// A save whose text did not change writes nothing; the revision it reports is
	// the one that is really stored, and changed=false says why it did not move.
	writeJSON(w, http.StatusOK, map[string]any{
		"revision": doc.Revision, "updated_at": doc.UpdatedAt,
		"changed": doc.Revision != req.Revision,
	})
}

// applyDocument makes a saved revision effective: the in-memory prompt snapshot,
// the project manifest (scope/constraints) and, for the goal, the goal object
// itself. The document row already holds the same text; these are the readers
// that existed before it.
func (s *Server) applyDocument(kind, goalID, text string) error {
	switch kind {
	case store.DocumentScope:
		s.SetProjectSettings(text, s.Constraints())
		if s.PersistDoc != nil {
			return s.PersistDoc(kind, text)
		}
	case store.DocumentConstraints:
		s.SetProjectSettings(s.scopeText(), text)
		if s.PersistDoc != nil {
			return s.PersistDoc(kind, text)
		}
	case store.DocumentGoal:
		// SaveGoalDocument already edited the statement in the same transaction;
		// there is nothing left to apply.
		return nil
	}
	return nil
}
