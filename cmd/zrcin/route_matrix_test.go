package main

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Four route tables describe the console's surfaces and none of them can see
// each other: Register (internal/api/api.go, the project face) and registerAgent
// (internal/api/agent.go and its sub-registrations, the participant face) are
// plain ServeMux registrations, while sessionWriteRoutes and agentRouteHidden
// (auth.go) are gate-side maps over the same paths. Audit item N7: a route added
// to one table without updating the others drifts silently. These tests make the
// drift loud by deriving the registered set from the source and cross-checking
// it against both gate tables, with every divergence declared on purpose.

var routePatternRe = regexp.MustCompile(`mux\.HandleFunc\(\s*"([A-Z]+ /api/[^"]+)"`)

// repoRoot walks up from the test's working directory until it finds go.mod,
// so the source-relative reads below work regardless of how go test set the
// working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(dir + string(os.PathSeparator) + "go.mod"); err == nil {
			return dir
		}
		parent := dir[:strings.LastIndexByte(dir, os.PathSeparator)]
		if parent == "" || parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// registeredPatterns extracts the route patterns the given source files
// register. The files are the authoritative declaration — no duplicate matrix
// to maintain — and the tests below pin the cross-table contracts that source
// alone cannot express.
func registeredPatterns(t *testing.T, files ...string) map[string]bool {
	t.Helper()
	root := repoRoot(t)
	out := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(root + string(os.PathSeparator) + f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range routePatternRe.FindAllStringSubmatch(string(src), -1) {
			out[m[1]] = true
		}
	}
	return out
}

// operatorOnlyWrites are the non-GET routes of the project face that no harness
// session may write, each grouped by why. The reverse half of
// TestSessionWriteRoutesMatchesRegisteredWrites walks this list: a new write
// route on the project face must land in sessionWriteRoutes (sessions may call
// it) or here (operator only) — one of the two, explicitly.
var operatorOnlyWrites = map[string]string{
	// Operator console interactions and curation.
	"POST /api/chat":                                   "operator text entry",
	"POST /api/directives":                             "operator directive",
	"POST /api/intents/{id}/reopen":                    "lifecycle control",
	"POST /api/intents/{id}/wait":                      "lifecycle control (participants wait via their own face)",
	"POST /api/intents/{id}/operator/complete":         "operator answers the help request",
	"POST /api/facts/verify-pending":                   "operator review trigger",
	"POST /api/facts/{id}/verify":                      "operator review trigger",
	"POST /api/facts/{id}/review":                      "operator review trigger",
	"POST /api/facts/{id}/withdraw":                    "operator override (participants use their own face)",
	"POST /api/goals/{id}/met":                         "goal stamping",
	"POST /api/guardrail/resolve":                      "tool approval answer",
	"POST /api/operator-requests/{id}/resolve":         "operator answers",
	"POST /api/operator-requests/{id}/request-details": "operator answers",
	"POST /api/operator-messages/seen":                 "operator inbox",
	"POST /api/operator-scope":                         "operator scope switch",
	"PUT /api/documents/{kind}":                        "operator document editor",
	"PUT /api/instructions/{id}":                       "operator instruction editor",
	"POST /api/instructions":                           "operator instruction editor",
	"POST /api/project/pause":                          "project switch",
	"POST /api/project/resume":                         "project switch",
	"POST /api/project/closeout":                       "project switch",
	"POST /api/project/append-goal":                    "project switch",
	"POST /api/decide":                                 "decider trigger",
	"POST /api/decider/pause":                          "decider switch",
	"POST /api/decider/resume":                         "decider switch",
	"POST /api/verifier/pause":                         "review switch",
	"POST /api/verifier/resume":                        "review switch",
	"POST /api/verifier/disable":                       "review switch",
	"POST /api/verifier/enable":                        "review switch",
	"POST /api/reports":                                "report generation",
	"POST /api/traffic/import":                         "instance content import",
	"POST /api/assets/import":                          "instance content import",
	"POST /api/traffic/import/preview":                 "read-only import preview (no ledger write); operator content import",
	"POST /api/assets/import/preview":                  "read-only import preview (no ledger write); operator content import",
	"POST /api/assets/{id}/fence":                      "operator fence membership decision",
	"POST /api/fence-pending/resolve":                  "operator fence decision queue",
	"POST /api/pocs/import":                            "instance content import",
	"POST /api/bundles/export":                         "instance content bundle",
	"POST /api/bundles/import":                         "instance content bundle",
	"PUT /api/nodes":                                   "instance node administration",
	"PUT /api/peers":                                   "instance node administration",
	"POST /api/runs/{id}/cancel":                       "operator run control",
	"POST /api/runs/{id}/warnings/dismiss":             "operator run control",
	"POST /api/access":                                 "operator access credentials",
	"POST /api/access/{id}/browser":                    "operator access credentials",
	"POST /api/access/{id}/lease":                      "operator access credentials",
	"DELETE /api/access/{id}":                          "operator access credentials",
	"DELETE /api/playbooks/{id}":                       "operator curation",
	"POST /api/playbooks/{id}/archive":                 "operator curation",
	"POST /api/playbooks/{id}/retire":                  "operator curation",
	"POST /api/playbooks/{id}/kb-candidate":            "operator curation",
	"POST /api/kb/candidates":                          "operator nomination (participants nominate via their own face)",
	"POST /api/spawn":                                  "participant-face only (agent.go)",
	"POST /api/workers/{id}/kill":                      "participant-face only (agent.go)",
}

// TestSessionWriteRoutesMatchesRegisteredWrites pins the contract between the
// project face's route table (Register) and the session write gate
// (sessionWriteRoutes):
//
//  1. every allowlist entry names a route that is actually registered, and its
//     normalized form is what normalizeSessionPath produces for a concrete path;
//  2. every registered non-GET route is classified: either a session may write
//     it (sessionWriteRoutes) or it is operator-only (operatorOnlyWrites). A new
//     write route without a classification fails here instead of silently
//     returning 404 to the sessions that need it — or worse, silently working.
func TestSessionWriteRoutesMatchesRegisteredWrites(t *testing.T) {
	registered := registeredPatterns(t, "internal/api/api.go")

	for key := range sessionWriteRoutes {
		method, normPath, _ := strings.Cut(key, " ")
		// The normalized entry must round-trip from a concrete path and name a
		// route that is actually registered on the project face.
		concrete := strings.Replace(normPath, "*", "x", 1)
		if got := normalizeSessionPath(concrete); got != normPath {
			t.Errorf("sessionWriteRoutes key %q: normalizeSessionPath(%q) = %q, want %q", key, concrete, got, normPath)
		}
		pattern := method + " " + strings.Replace(normPath, "*", "{id}", 1)
		if !registered[pattern] {
			t.Errorf("sessionWriteRoutes names %q but no such route is registered on the project face (pattern %q)", key, pattern)
		}
	}

	for pattern := range registered {
		method, path, _ := strings.Cut(pattern, " ")
		if method == http.MethodGet {
			continue
		}
		concrete := strings.Replace(path, "{id}", "x", 1)
		if sessionWriteRoutes[method+" "+normalizeSessionPath(concrete)] {
			continue
		}
		if _, ok := operatorOnlyWrites[pattern]; ok {
			continue
		}
		t.Errorf("write route %q is neither in sessionWriteRoutes (normalized %q) nor declared operator-only: classify it", pattern, normalizeSessionPath(concrete))
	}
}

// participantFaceOnly lists the segments that exist on the participant face but
// stay hidden on the project face, with the reason each divergence is deliberate
// (the agent.go doc comment states the same two examples: spawn exists only
// there, board is allowed only on the project face).
var participantFaceOnly = []string{
	"/api/platform-boundary", // executor isolation notice lives on the participant face
	"/api/spawn",             // spawning a worker is a participant act
	"/api/workers",           // worker control likewise
	// Nomination: a session nominates through its own authenticated face; the
	// project face's route is the operator's (agentRouteHidden hides /api/kb/).
	"/api/kb/candidates",
}

// TestParticipantFaceRoutesAreVisibleOnTheProjectFace cross-checks registerAgent
// against agentRouteHidden: a route a participant can call must also be visible
// to a session on the project face unless it is declared participant-face only.
// The reverse (hidden segments without a participant route) is fine — board is
// the documented example — so only this direction is pinned.
func TestParticipantFaceRoutesAreVisibleOnTheProjectFace(t *testing.T) {
	registered := registeredPatterns(t,
		"internal/api/agent.go",
		"internal/api/external_actions.go",
		"internal/api/resources.go",
		"internal/api/focus.go",
		"internal/api/communication.go",
		"internal/api/platform_boundary.go",
	)
	only := map[string]bool{}
	for _, seg := range participantFaceOnly {
		only[seg] = true
	}
	patterns := make([]string, 0, len(registered))
	for p := range registered {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		_, path, _ := strings.Cut(pattern, " ")
		concrete := strings.ReplaceAll(path, "{id}", "x")
		concrete = strings.ReplaceAll(concrete, "{name}", "x")
		hidden := false
		for _, seg := range participantFaceOnly {
			if concrete == seg || strings.HasPrefix(concrete, seg+"/") {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}
		if agentRouteHidden(concrete) {
			t.Errorf("participant route %q is hidden on the project face: declare it participantFaceOnly or make it visible", pattern)
		}
	}
}
