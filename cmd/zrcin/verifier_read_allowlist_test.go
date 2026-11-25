package main

import (
	"net/http"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/collab"
)

// The review credential's read surface is an allowlist twice over (review F1):
// the route must be named here, and the id in the path must be in the
// credential's own sets. This table pins both layers, including the denials
// that carry the reason from the doc comment: enumeration of the ledger,
// another reviewer's verdict, the view of the party under review, and every
// write.
func TestVerifierReadAllowed(t *testing.T) {
	cred := collab.ReadOnlyCredential{
		ProjectID:   "p1",
		FactIDs:     []string{"f_target", "f_premise"},
		EvidenceIDs: []string{"ev_cited"},
	}
	p := "/p/p1/api"
	cases := []struct {
		method, path string
		want         bool
	}{
		// The surface: the named fact, its history, the named evidence, what a
		// run did.
		{http.MethodGet, p + "/facts/f_target", true},
		{http.MethodGet, p + "/facts/f_premise", true},
		{http.MethodGet, p + "/facts/f_target/history", true},
		{http.MethodGet, p + "/evidence/ev_cited", true},
		{http.MethodGet, p + "/runs/r_1/events", true},

		// The bare lists are enumeration now: reading by id is possible because
		// the credential names the ids, not because a list reveals them.
		{http.MethodGet, p + "/facts", false},
		{http.MethodGet, p + "/evidence", false},

		// Ids outside the credential's sets are other directions' records.
		{http.MethodGet, p + "/facts/f_other", false},
		{http.MethodGet, p + "/facts/f_other/history", false},
		{http.MethodGet, p + "/evidence/ev_other", false},

		// Absent on purpose.
		{http.MethodGet, p + "/reviews", false},
		{http.MethodGet, p + "/board", false},
		{http.MethodGet, p + "/intents", false},
		{http.MethodGet, p + "/goals", false},
		{http.MethodGet, p + "/messages", false},
		{http.MethodGet, p + "/runs", false},
		{http.MethodGet, p + "/runs/r_1/output", false},
		{http.MethodGet, p + "/runs/r_1/context", false},
		{http.MethodGet, p + "/runs/r_1/events/tail", false},
		{http.MethodGet, p + "/facts/verify-pending", false},
		{http.MethodPost, p + "/facts/f_target/verify", false},
		{http.MethodPut, p + "/state", false},

		// Only the project base is on the surface; instance and static paths are
		// the operator's.
		{http.MethodGet, "/api/facts", false},
		{http.MethodGet, "/p/p1/static/index.js", false},
	}
	for _, c := range cases {
		if got := verifierReadAllowed(c.method, c.path, cred); got != c.want {
			t.Errorf("verifierReadAllowed(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
	// A credential with an empty set admits nothing id-shaped: the gate fails
	// closed when a registry handed out a credential without a scope.
	empty := collab.ReadOnlyCredential{ProjectID: "p1"}
	for _, path := range []string{p + "/facts/f_target", p + "/evidence/ev_cited"} {
		if verifierReadAllowed(http.MethodGet, path, empty) {
			t.Errorf("empty-scope credential admitted %s", path)
		}
	}
}
