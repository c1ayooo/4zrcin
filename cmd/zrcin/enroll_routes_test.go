package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// The node ingress is a switch: with --public-url unset nothing answers, and
// with it set the operator surface creates invitations while the public
// prefixes dispatch into the enroll shell (docs/15 §3.1, §3.3).

func newEnrollTestHub(t *testing.T, withIngress bool) *hub {
	t.Helper()
	h := &hub{cfg: hubConfig{}}
	if !withIngress {
		return h
	}
	store, err := enroll.Open(filepath.Join(t.TempDir(), "remote-nodes.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	svc := enroll.New(store, enroll.Options{
		PublicURL:   "https://ops.example.com",
		DataDir:     "/srv/zrcin/data",
		ConsoleAddr: "127.0.0.1:8080",
		HubAddr:     "127.0.0.1:18766",
		HubKey:      "hub-key",
		IssueKey:    []byte("0123456789abcdef0123456789abcdef"),
	})
	h.cfg.RemoteNodes = svc
	h.cfg.Enroll = enroll.NewIngress(svc)
	h.cfg.Dist = enroll.NewDist(enroll.DistConfig{DataDir: t.TempDir()})
	return h
}

func doHub(t *testing.T, h *hub, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNodeIngressOffAnswers404Everywhere(t *testing.T) {
	h := newEnrollTestHub(t, false)
	for _, probe := range []struct{ method, path string }{
		{http.MethodPost, "/node/join"},
		{http.MethodGet, "/node/next"},
		{http.MethodPost, "/node/report"},
		{http.MethodGet, "/join/x/install.sh"},
		{http.MethodGet, "/tunnel/x"},
		{http.MethodGet, "/api/enrollments"},
		{http.MethodPost, "/api/enrollments"},
	} {
		if rec := doHub(t, h, probe.method, probe.path, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404 while --public-url is unset", probe.method, probe.path, rec.Code)
		}
	}
	// The hint names the flag: an operator who finds a 404 must learn why.
	rec := doHub(t, h, http.MethodGet, "/api/enrollments", "")
	if !strings.Contains(rec.Body.String(), "--public-url") {
		t.Fatalf("disabled-ingress hint = %q, want it to name --public-url", rec.Body.String())
	}
}

func TestEnrollmentOperatorSurface(t *testing.T) {
	h := newEnrollTestHub(t, true)

	rec := doHub(t, h, http.MethodPost, "/api/enrollments", `{"recipe":"capture","deploy":"command"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created enroll.Created
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create body: %v", err)
	}
	if !strings.HasPrefix(created.Token, "zr1_") || !strings.Contains(created.InstallCommand, created.Token) {
		t.Fatalf("create response = %+v", created)
	}
	// The credential response must never be cached (same rule as the existing
	// node join command).
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("create Cache-Control = %q", got)
	}

	rec = doHub(t, h, http.MethodGet, "/api/enrollments", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), created.ID) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	// The rail needs to know what this platform can hand out; a platform with
	// no launcher yet must still answer, with all three flags false.
	if !strings.Contains(rec.Body.String(), `"dist"`) {
		t.Fatalf("list lacks the artifact status: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), created.Token) {
		t.Fatal("the list leaks the plaintext token")
	}

	rec = doHub(t, h, http.MethodDelete, "/api/enrollments/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body)
	}
	// Revocation is idempotent: revoking an already-revoked invitation is a
	// no-op success, while an unknown id is 404.
	rec = doHub(t, h, http.MethodDelete, "/api/enrollments/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second revoke = %d, want 200 (idempotent)", rec.Code)
	}
	rec = doHub(t, h, http.MethodDelete, "/api/enrollments/does-not-exist", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown revoke = %d, want 404", rec.Code)
	}

	// Invalid input is refused with a readable message, not a 500.
	rec = doHub(t, h, http.MethodPost, "/api/enrollments", `{"recipe":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad recipe = %d", rec.Code)
	}
}

// The public prefixes dispatch into the ingress shell; an unknown sub-route is
// 404 so a half-built surface never looks alive.
func TestPublicPrefixesDispatchToIngress(t *testing.T) {
	h := newEnrollTestHub(t, true)
	rec := doHub(t, h, http.MethodPost, "/node/join", `{"install_id":"x"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("join without a token = %d, want 401 from the ingress", rec.Code)
	}
	rec = doHub(t, h, http.MethodGet, "/node/nothing-here", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown /node route = %d", rec.Code)
	}
}

// The remote-node listing is the ingress's own surface: nodes plus the live
// session facts, and a 404 naming --public-url while the ingress is off.
func TestRemoteNodesOperatorSurface(t *testing.T) {
	h := newEnrollTestHub(t, false)
	if rec := doHub(t, h, http.MethodGet, "/api/remote-nodes", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "--public-url") {
		t.Fatalf("disabled remote-nodes = %d %s", rec.Code, rec.Body)
	}

	h = newEnrollTestHub(t, true)
	rec := doHub(t, h, http.MethodGet, "/api/remote-nodes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("remote-nodes = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"ingress"`) || !strings.Contains(rec.Body.String(), `"nodes"`) {
		t.Fatalf("remote-nodes body = %s", rec.Body)
	}
}
