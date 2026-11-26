package enroll

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock is the Now seam: tests move time instead of sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestService(t *testing.T) (*Service, *Store, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "remote-nodes.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	clock := &fakeClock{t: time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)}
	svc := New(store, Options{
		PublicURL:   "https://ops.example.com",
		DataDir:     "/srv/zrcin/data",
		ConsoleAddr: "127.0.0.1:8080",
		HubAddr:     "127.0.0.1:18766",
		HubKey:      "hub-key",
		IssueKey:    []byte("0123456789abcdef0123456789abcdef"),
		Now:         clock.now,
	})
	return svc, store, clock
}

func mustCreate(t *testing.T, svc *Service, o CreateOptions) Created {
	t.Helper()
	created, err := svc.Create(o)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return created
}

func mustJoin(t *testing.T, svc *Service, token, installID string) JoinResponse {
	t.Helper()
	resp, err := svc.Join(token, installID)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	return resp
}

// The file is the only persistence: what one process writes, the next reads,
// with 0600 and no plaintext credential in it.
func TestStorePersistsRecordsAndKeepsSecretsHashed(t *testing.T) {
	svc, store, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{Recipe: RecipeCapture, Deploy: DeploySSH})
	joined := mustJoin(t, svc, created.Token, "install-1")

	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if bytes.Contains(raw, []byte(created.Token)) {
		t.Fatal("the enrollment token plaintext is in the file")
	}
	if bytes.Contains(raw, []byte(joined.NodeCredential)) {
		t.Fatal("the node credential plaintext is in the file")
	}
	if !bytes.Contains(raw, []byte(HashCredential(created.Token))) {
		t.Fatal("the token digest is missing from the file")
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", info.Mode().Perm())
	}

	reopened, err := Open(store.path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	svc2 := New(reopened, Options{
		PublicURL: "https://ops.example.com", IssueKey: []byte("0123456789abcdef0123456789abcdef"),
		Now: func() time.Time { return time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC) },
	})
	// The same token + install id after a restart returns the same credential:
	// that is why the platform derives it instead of drawing it.
	resp, err := svc2.Join(created.Token, "install-1")
	if err != nil {
		t.Fatalf("join after restart: %v", err)
	}
	if resp.NodeCredential != joined.NodeCredential {
		t.Fatalf("credential changed across restart: %q vs %q", resp.NodeCredential, joined.NodeCredential)
	}
}

// A file that exists but cannot be parsed must fail startup loudly: silently
// resetting it would drop every node credential an operator handed out.
func TestStoreCorruptedFileFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-nodes.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("Open accepted a corrupted file")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error does not name the file: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatal("the corrupted file was removed behind the operator's back")
	}
}

func TestCreateValidatesInput(t *testing.T) {
	svc, _, _ := newTestService(t)
	for _, o := range []CreateOptions{
		{Recipe: "bogus"},
		{Deploy: "telnet"},
		{Name: "UPPER CASE"},
		{Name: "x"},
	} {
		if _, err := svc.Create(o); err == nil {
			t.Fatalf("Create accepted %+v", o)
		}
	}
	created := mustCreate(t, svc, CreateOptions{TTLMinutes: 1})
	if !strings.Contains(created.InstallCommand, "/join/"+created.Token+"/install.sh") {
		t.Fatalf("install command = %q", created.InstallCommand)
	}
}

func TestEnrollmentExpiryAndRevocation(t *testing.T) {
	svc, _, clock := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{TTLMinutes: 1})
	clock.advance(2 * time.Minute)

	views := svc.ListEnrollments()
	if len(views) != 1 || views[0].State != EnrollExpired {
		t.Fatalf("expired enrollment reads as %+v", views)
	}
	_, err := svc.Join(created.Token, "install-1")
	var je *JoinErr
	if !errors.As(err, &je) || je.Outcome != JoinSpent {
		t.Fatalf("expired token join = %v, want spent", err)
	}
	if err := svc.RevokeEnrollment(created.ID); err == nil {
		t.Fatal("revoking an expired enrollment succeeded")
	}
}

func TestJoinOutcomes(t *testing.T) {
	svc, _, clock := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{})

	first := mustJoin(t, svc, created.Token, "install-1")
	if first.NodeID == "" || first.NodeCredential == "" {
		t.Fatalf("first join lacked identity: %+v", first)
	}
	if first.Tunnel.Path != "/tunnel" || first.Hub.Addr != "127.0.0.1:18766" || first.ConsoleAddr != "127.0.0.1:8080" {
		t.Fatalf("join response mirrors the wrong addresses: %+v", first)
	}
	if first.Dist == nil {
		t.Fatal("dist must be an empty array, not null")
	}
	if _, err := ParseCredential(first.NodeCredential); err != nil {
		t.Fatalf("issued credential does not parse: %v", err)
	}
	if v, ok := svc.AuthenticateNode(first.NodeCredential); !ok || v.ID != first.NodeID {
		t.Fatalf("issued credential does not authenticate: ok=%v view=%+v", ok, v)
	}

	// Idempotent retry: same token, same install id.
	again := mustJoin(t, svc, created.Token, "install-1")
	if again.NodeCredential != first.NodeCredential || again.NodeID != first.NodeID {
		t.Fatal("retry with the same install id issued a different identity")
	}
	// Different host with the same token: refused, not silently shared.
	if _, err := svc.Join(created.Token, "install-2"); err == nil {
		t.Fatal("a second host joined with the same token")
	} else {
		var je *JoinErr
		if !errors.As(err, &je) || je.Outcome != JoinHostMismatch {
			t.Fatalf("second host outcome = %v, want host mismatch", err)
		}
	}
	// Unknown token is spent, not a leak of "which part was wrong".
	if _, err := svc.Join("zr1_"+strings.Repeat("A", 43), "install-1"); err == nil {
		t.Fatal("a made-up token joined")
	}

	// The tunnel coming up spends the enrollment.
	svc.MarkJoined(first.NodeID)
	if got := svc.ListEnrollments()[0].State; got != EnrollJoined {
		t.Fatalf("enrollment state after connect = %q, want joined", got)
	}
	_, err := svc.Join(created.Token, "install-1")
	var je *JoinErr
	if !errors.As(err, &je) || je.Outcome != JoinSpent {
		t.Fatalf("join after the token was spent = %v, want spent", err)
	}

	// Revoking a joined enrollment is refused: the node holds a credential and
	// the operator action for that is destroy.
	if err := svc.RevokeEnrollment(created.ID); err == nil {
		t.Fatal("revoking a joined enrollment succeeded")
	}
	_ = clock
}

func TestAuthenticateRejectsForeignAndDestroyed(t *testing.T) {
	svc, store, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1")

	if _, ok := svc.AuthenticateNode("zn1_" + strings.Repeat("0", 32) + "_" + strings.Repeat("A", 43)); ok {
		t.Fatal("a made-up node credential authenticated")
	}
	// Destroyed nodes answer with their state so the HTTP layer can send 410.
	store.mu.Lock()
	store.mu.f.Nodes[0].State = NodeDestroyed
	store.mu.Unlock()
	view, ok := svc.AuthenticateNode(joined.NodeCredential)
	if ok {
		t.Fatal("a destroyed node's credential authenticated")
	}
	if view.State != NodeDestroyed {
		t.Fatalf("destroyed node view state = %q", view.State)
	}
}

func TestHeartbeatDerivesOnlineFromFreshness(t *testing.T) {
	svc, _, clock := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{Name: "edge-01"})
	joined := mustJoin(t, svc, created.Token, "install-1")

	view, err := svc.Heartbeat(joined.NodeID, Report{Hostname: "wsl", OS: "linux", Arch: "amd64", NodeBinPath: "/opt/zrcin-node/bin/zrcin"})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !view.Online || view.State != NodeOnline {
		t.Fatalf("fresh heartbeat reads %+v, want online", view)
	}
	if view.Name != "edge-01" || view.Hostname != "wsl" || view.NodeBinPath == "" {
		t.Fatalf("heartbeat did not record the reported fields: %+v", view)
	}
	if !view.DispatchesOK {
		t.Fatal("a live tunnel node must be dispatch-ready now that the file contract landed")
	}

	clock.advance(OnlineWindow + time.Second)
	gone := svc.Nodes()[0]
	if gone.Online || gone.State != NodeOffline {
		t.Fatalf("stale node reads %+v, want offline", gone)
	}
	if _, err := svc.Heartbeat("00000000000000000000000000000000", Report{}); err == nil {
		t.Fatal("heartbeat for an unknown node succeeded")
	}
}

// Next is the launcher's only control channel: it must return the state even
// when nothing is queued, and wake the moment a directive arrives.
func TestNextLongPollWakesOnDirective(t *testing.T) {
	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1")

	// Nothing queued, no wait: the state comes back immediately.
	res := svc.Next(joined.NodeID, 0)
	if res.State.ID != joined.NodeID || len(res.Directives) != 0 {
		t.Fatalf("empty long-poll = %+v", res)
	}

	type outcome struct{ res NextResult }
	done := make(chan outcome, 1)
	go func() { done <- outcome{res: svc.Next(joined.NodeID, 10*time.Second)} }()

	time.Sleep(50 * time.Millisecond) // let the poll park
	d, err := svc.Enqueue(joined.NodeID, "uninstall")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if d.Seq == 0 || d.Op != "uninstall" {
		t.Fatalf("directive = %+v", d)
	}
	select {
	case got := <-done:
		if len(got.res.Directives) != 1 || got.res.Directives[0] != d {
			t.Fatalf("long-poll woke with %+v, want %+v", got.res, d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake on a directive")
	}
	// Delivered directives are not redelivered; the sequence keeps rising.
	if res := svc.Next(joined.NodeID, 0); len(res.Directives) != 0 {
		t.Fatalf("delivered directive redelivered: %+v", res.Directives)
	}
	d2, _ := svc.Enqueue(joined.NodeID, "uninstall")
	if d2.Seq != d.Seq+1 {
		t.Fatalf("sequence %d, want %d", d2.Seq, d.Seq+1)
	}
}

func TestValidatePublicURL(t *testing.T) {
	ok := []string{
		"https://ops.example.com",
		"https://ops.example.com/",
		"http://127.0.0.1:8080",
		"http://10.0.0.5:8080",
		"http://localhost:8080",
	}
	for _, raw := range ok {
		if _, err := ValidatePublicURL(raw); err != nil {
			t.Fatalf("ValidatePublicURL(%q) = %v, want ok", raw, err)
		}
	}
	bad := map[string]string{
		"http://ops.example.com":       "TLS",          // public host over plain http
		"https://ops.example.com/base": "路径",           // a path
		"https://ops.example.com?x=1":  "查询",           // a query
		"ops.example.com":              "http 或 https", // no scheme
		"ftp://ops.example.com":        "http 或 https",
	}
	for raw, want := range bad {
		_, err := ValidatePublicURL(raw)
		if err == nil {
			t.Fatalf("ValidatePublicURL(%q) accepted", raw)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ValidatePublicURL(%q) error = %q, want it to mention %q", raw, err, want)
		}
	}
	got, err := ValidatePublicURL("https://ops.example.com/")
	if err != nil || got != "https://ops.example.com" {
		t.Fatalf("trailing slash: got %q err %v", got, err)
	}
}

func TestEnsureTunnelKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.key")
	key, err := EnsureTunnelKey(path)
	if err != nil {
		t.Fatalf("EnsureTunnelKey: %v", err)
	}
	if len(key) != TunnelKeySize {
		t.Fatalf("key size = %d, want %d", len(key), TunnelKeySize)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v %v", info.Mode().Perm(), err)
	}
	again, err := EnsureTunnelKey(path)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatalf("reload changed the key: %v", err)
	}

	// A wrong-length file is a startup error, never silently regenerated
	// (nodes hold credentials derived from it).
	badPath := filepath.Join(t.TempDir(), "tunnel.key")
	if err := os.WriteFile(badPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureTunnelKey(badPath); err == nil {
		t.Fatal("a short tunnel key was accepted")
	}
}

// ── ingress ────────────────────────────────────────────────────────────────

func newTestIngress(t *testing.T) (*Ingress, *Service, *fakeClock) {
	t.Helper()
	svc, _, clock := newTestService(t)
	return NewIngress(svc), svc, clock
}

func doJSON(t *testing.T, h http.Handler, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = "192.0.2.10:5555"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIngressJoinReportNext(t *testing.T) {
	in, svc, _ := newTestIngress(t)
	created := mustCreate(t, svc, CreateOptions{})

	rec := doJSON(t, in, http.MethodPost, "/node/join", created.Token, JoinRequest{InstallID: "install-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("join status = %d body %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("join Cache-Control = %q", got)
	}
	var joined JoinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &joined); err != nil {
		t.Fatalf("join body: %v", err)
	}

	rec = doJSON(t, in, http.MethodPost, "/node/report", joined.NodeCredential, Report{Hostname: "wsl", OS: "linux", Arch: "amd64"})
	if rec.Code != http.StatusOK {
		t.Fatalf("report status = %d body %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, in, http.MethodGet, "/node/next?wait=0", joined.NodeCredential, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("next status = %d body %s", rec.Code, rec.Body)
	}
	var next NextResult
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
		t.Fatalf("next body: %v", err)
	}
	if next.State.ID != joined.NodeID || !next.State.Online {
		t.Fatalf("next state = %+v", next.State)
	}

	// Status codes: no credential 401, made-up 401, spent token 410.
	if rec := doJSON(t, in, http.MethodGet, "/node/next", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous next = %d", rec.Code)
	}
	if rec := doJSON(t, in, http.MethodGet, "/node/next", "zn1_"+strings.Repeat("0", 32)+"_"+strings.Repeat("A", 43), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("foreign credential next = %d", rec.Code)
	}
	svc.MarkJoined(joined.NodeID)
	if rec := doJSON(t, in, http.MethodPost, "/node/join", created.Token, JoinRequest{InstallID: "install-1"}); rec.Code != http.StatusGone {
		t.Fatalf("spent token join = %d", rec.Code)
	}
	// Wrong methods and unknown sub-routes never look alive.
	if rec := doJSON(t, in, http.MethodGet, "/node/join", created.Token, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET join = %d", rec.Code)
	}
	if rec := doJSON(t, in, http.MethodGet, "/node/whatever", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown sub-route = %d", rec.Code)
	}
}

func TestIngressRateLimitsFailures(t *testing.T) {
	in, _, _ := newTestIngress(t)
	last := http.StatusUnauthorized
	for i := 0; i < 40; i++ {
		rec := doJSON(t, in, http.MethodPost, "/node/join", "zr1_"+strings.Repeat("A", 43), JoinRequest{InstallID: "x"})
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after 40 failed joins the status is %d, want 429", last)
	}
}

// Later tickets register their handlers at exact paths; the shell dispatches
// them and nothing else.
func TestIngressDispatchesRegisteredRoutes(t *testing.T) {
	in, _, _ := newTestIngress(t)
	hit := false
	in.Register("/join/abc/install.sh", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte("script"))
	}))
	rec := doJSON(t, in, http.MethodGet, "/join/abc/install.sh", "", nil)
	if !hit || rec.Code != http.StatusOK || rec.Body.String() != "script" {
		t.Fatalf("registered route: hit=%v code=%d body=%q", hit, rec.Code, rec.Body)
	}
}

// Destroy is the teardown of docs/14 §7.3: the record goes destroying, the
// uninstall directive lands on the node's control channel, and the receipt
// finalises it. Forget does the same for a node that will never report.
func TestDestroyQueuesUninstallAndFinalises(t *testing.T) {
	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1")

	view, err := svc.Destroy(joined.NodeID)
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if view.State != NodeDestroying {
		t.Fatalf("state after destroy = %q, want destroying", view.State)
	}
	next := svc.Next(joined.NodeID, 0)
	if len(next.Directives) != 1 || next.Directives[0].Op != "uninstall" {
		t.Fatalf("directives after destroy = %+v", next.Directives)
	}
	if next.State.ID != joined.NodeID {
		t.Fatal("wrong node in long-poll state")
	}
	// A destroying node still authenticates (the directive channel must work)
	// but reports its state so the ticket endpoint refuses new tunnels.
	if v, ok := svc.AuthenticateNode(joined.NodeCredential); !ok || v.State != NodeDestroying {
		t.Fatalf("authenticate while destroying = %+v ok=%v", v, ok)
	}
	// Destroy is idempotent.
	if again, err := svc.Destroy(joined.NodeID); err != nil || again.State != NodeDestroying {
		t.Fatalf("second Destroy = %+v %v", again, err)
	}

	// The launcher's receipt.
	view, err = svc.Heartbeat(joined.NodeID, Report{Event: "uninstalled"})
	if err != nil {
		t.Fatalf("Heartbeat(uninstalled): %v", err)
	}
	if view.State != NodeDestroyed {
		t.Fatalf("state after receipt = %q, want destroyed", view.State)
	}
	if _, ok := svc.AuthenticateNode(joined.NodeCredential); ok {
		t.Fatal("a destroyed node's credential authenticated")
	}
	if _, err := svc.Destroy("00000000000000000000000000000000"); !errors.Is(err, ErrNoSuchNode) {
		t.Fatalf("Destroy(unknown) = %v", err)
	}
}

func TestForgetFinalisesADestroyingNode(t *testing.T) {
	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1")
	if _, err := svc.Destroy(joined.NodeID); err != nil {
		t.Fatal(err)
	}
	view, err := svc.MarkDestroyed(joined.NodeID)
	if err != nil || view.State != NodeDestroyed {
		t.Fatalf("MarkDestroyed = %+v %v", view, err)
	}
}
