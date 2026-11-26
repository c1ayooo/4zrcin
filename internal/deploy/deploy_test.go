package deploy

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// ---- in-process SSH server ----

type sshServer struct {
	t    *testing.T
	ln   net.Listener
	addr string
	key  ssh.Signer

	uname string
	uid   string
	node  string

	// failBootstrap makes the bootstrap command exit non-zero.
	failBootstrap bool
	// onBootstrap runs after the script arrives (it can simulate the node
	// joining the platform).
	onBootstrap func(script string)

	mu          sync.Mutex
	commands    []string
	bootstrap   []string
	authKind    string
	authUser    string
	connections int
}

type sshServerOptions struct {
	password  string // empty disables password auth
	authorize ssh.PublicKey
	uid       string
	node      string
}

func startSSHServer(t *testing.T, opts sshServerOptions) *sshServer {
	t.Helper()
	signer := newHostKey(t)
	cfg := &ssh.ServerConfig{}
	if opts.password != "" {
		want := opts.password
		cfg.PasswordCallback = func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == want {
				return nil, nil
			}
			return nil, fmt.Errorf("密码不正确")
		}
	}
	if opts.authorize != nil {
		want := opts.authorize
		cfg.PublicKeyCallback = func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), want.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("公钥不被接受")
		}
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshServer{
		t: t, ln: ln, addr: ln.Addr().String(), key: signer,
		uname: "Linux x86_64", uid: opts.uid, node: opts.node,
	}
	if s.uid == "" {
		s.uid = "0"
	}
	if s.node == "" {
		s.node = "fresh"
	}
	t.Cleanup(func() { _ = ln.Close() })
	go s.accept(cfg)
	return s
}

func newHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func (s *sshServer) accept(cfg *ssh.ServerConfig) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn, cfg)
	}
}

func (s *sshServer) serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close() //nolint:errcheck
		return
	}
	s.mu.Lock()
	s.connections++
	s.authUser = sc.User()
	s.mu.Unlock()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go s.serveSession(ch, chReqs)
	}
}

func (s *sshServer) serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close() //nolint:errcheck
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		s.runCommand(ch, payload.Command)
		return
	}
}

func (s *sshServer) runCommand(ch ssh.Channel, cmd string) {
	s.mu.Lock()
	s.commands = append(s.commands, cmd)
	s.mu.Unlock()

	var out string
	code := 0
	switch {
	case cmd == "uname -s -m":
		out = s.uname + "\n"
	case cmd == "id -u":
		out = s.uid + "\n"
	case strings.Contains(cmd, "/run/systemd/system"):
		out = "systemd\n"
	case strings.Contains(cmd, "node.json"):
		out = s.node + "\n"
	case isBootstrapCommand(cmd):
		stdin, _ := io.ReadAll(ch)
		s.mu.Lock()
		s.bootstrap = append(s.bootstrap, string(stdin))
		hook := s.onBootstrap
		fail := s.failBootstrap
		s.mu.Unlock()
		if fail {
			code = 2
			out = "curl: not found\n"
		} else if hook != nil {
			hook(string(stdin))
		}
	default:
		code = 127
		out = "unsupported command\n"
	}
	if out != "" {
		_, _ = ch.Write([]byte(out))
	}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
}

func isBootstrapCommand(cmd string) bool {
	return cmd == "sh" || cmd == "sudo -n sh" || cmd == "sudo -S sh"
}

func (s *sshServer) recorded() (commands, bootstraps []string, connections int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...), append([]string(nil), s.bootstrap...), s.connections
}

// ---- platform fixture ----

type fixture struct {
	svc  *enroll.Service
	dist *enroll.Dist
	dep  *Service
}

func newFixture(t *testing.T, opts ...func(*Config)) *fixture {
	t.Helper()
	dir := t.TempDir()
	store, err := enroll.Open(filepath.Join(dir, "remote-nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := enroll.New(store, enroll.Options{
		PublicURL:   "https://ops.example.com",
		DataDir:     "/srv/zrcin/data",
		HubAddr:     "127.0.0.1:18777",
		ConsoleAddr: "127.0.0.1:18080",
		HubKey:      "hub-key",
		IssueKey:    []byte("0123456789abcdef0123456789abcdef"),
	})
	launcher := filepath.Join(dir, "zrcin-node")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dist := enroll.NewDist(enroll.DistConfig{
		DataDir: filepath.Join(dir, "dist"),
		Paths:   map[string]string{enroll.ArtLauncher: launcher},
	})
	cfg := Config{Enroll: svc, Dist: dist, DataDir: filepath.Join(dir, "keys"), Wait: 10 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}
	dep, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{svc: svc, dist: dist, dep: dep}
}

// joinFromScript parses the enrollment token out of the bootstrap script and
// performs what the launcher would do: join, then report in (online).
func joinFromScript(t *testing.T, svc *enroll.Service) func(script string) {
	t.Helper()
	return func(script string) {
		token := tokenFromScript(t, script)
		joined, err := svc.Join(token, "install-test")
		if err != nil {
			t.Errorf("join: %v", err)
			return
		}
		if _, err := svc.Heartbeat(joined.NodeID, enroll.Report{Hostname: "edge-test"}); err != nil {
			t.Errorf("heartbeat: %v", err)
		}
		// The tunnel ingress does this in production once the handshake binds
		// the ticket to the node.
		svc.MarkJoined(joined.NodeID)
	}
}

func tokenFromScript(t *testing.T, script string) string {
	t.Helper()
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, "TOKEN=") {
			continue
		}
		token, err := strconv.Unquote(strings.TrimPrefix(line, "TOKEN="))
		if err != nil || token == "" {
			t.Fatalf("token line=%q err=%v", line, err)
		}
		return token
	}
	t.Fatalf("script has no TOKEN line:\n%s", script)
	return ""
}

func waitJob(t *testing.T, dep *Service, id string, timeout time.Duration) JobView {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		view, ok := dep.Get(id)
		if !ok {
			t.Fatalf("job %s disappeared", id)
		}
		if view.State != StateRunning {
			return view
		}
		time.Sleep(20 * time.Millisecond)
	}
	view, _ := dep.Get(id)
	t.Fatalf("job did not finish: %+v", view)
	return view
}

func stepStateOf(view JobView, key string) string {
	for _, s := range view.Steps {
		if s.Key == key {
			return s.State
		}
	}
	return ""
}

// ---- tests ----

func TestDeployPasswordStdinAndOnline(t *testing.T) {
	f := newFixture(t)
	srv := startSSHServer(t, sshServerOptions{password: "hunter2", uid: "0"})
	srv.onBootstrap = joinFromScript(t, f.svc)

	view, err := f.dep.Start(Options{
		Host: "127.0.0.1", Port: portOf(t, srv.addr), User: "root",
		Password: "hunter2", Name: "edge-ssh",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJob(t, f.dep, view.ID, 10*time.Second)
	if done.State != StateSucceeded {
		t.Fatalf("job state=%s err=%s log=%v", done.State, done.Error, done.Log)
	}
	for _, key := range []string{stepConnect, stepProbe, stepBootstrap, stepWait, stepDone} {
		if got := stepStateOf(done, key); got != StepDone {
			t.Fatalf("step %s = %s, want done (%+v)", key, got, done.Steps)
		}
	}
	if done.NodeID == "" {
		t.Fatal("job did not record the node id")
	}

	commands, bootstraps, _ := srv.recorded()
	if len(bootstraps) != 1 {
		t.Fatalf("bootstrap ran %d times", len(bootstraps))
	}
	// The script goes over stdin; the command line carries neither the token
	// nor the script.
	for _, cmd := range commands {
		if strings.Contains(cmd, "zr1_") || strings.Contains(cmd, "install.sh") {
			t.Fatalf("secret leaked into argv: %q", cmd)
		}
	}
	if !strings.Contains(bootstraps[0], `TOKEN="zr1_`) || !strings.Contains(bootstraps[0], "install --join") {
		t.Fatalf("stdin is not the bootstrap script: %q", bootstraps[0])
	}
	// Root logins do not go through sudo.
	if !contains(commands, "sh") {
		t.Fatalf("root bootstrap command = %v", commands)
	}
	// The enrollment is spent by the join.
	if got := f.svc.ListEnrollments()[0].State; got != enroll.EnrollJoined {
		t.Fatalf("enrollment state = %q", got)
	}
	// Credentials are gone once the job ends.
	f.dep.mu.Lock()
	j := f.dep.jobs[view.ID]
	f.dep.mu.Unlock()
	for name, secret := range map[string][]byte{
		"password": j.password, "key": j.privateKey, "passphrase": j.passphrase,
		"sudo": j.sudoPassword, "script": j.script,
	} {
		if len(secret) > 0 && !allZero(secret) {
			t.Fatalf("%s was not wiped", name)
		}
	}
	// A second connection to the same host reuses the pinned key.
	if !strings.Contains(logText(done), "首次连接") {
		t.Fatalf("first connection did not note the pinned host key: %v", logText(done))
	}
}

func TestDeployKeyAuthSudoPassword(t *testing.T) {
	f := newFixture(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	srv := startSSHServer(t, sshServerOptions{authorize: signer.PublicKey(), uid: "1000"})
	srv.onBootstrap = joinFromScript(t, f.svc)

	pemBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test key", []byte("key-pass"))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(pemBlock))
	if _, err := ssh.ParsePrivateKey([]byte(keyPEM)); err == nil {
		t.Fatal("test key is not encrypted")
	}
	if len(pub) == 0 {
		t.Fatal("no public key")
	}

	view, err := f.dep.Start(Options{
		Host: "127.0.0.1", Port: portOf(t, srv.addr), User: "deployer",
		PrivateKey: keyPEM, Passphrase: "key-pass", SudoPassword: "sudo-pass",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJob(t, f.dep, view.ID, 10*time.Second)
	if done.State != StateSucceeded {
		t.Fatalf("job state=%s err=%s log=%v", done.State, done.Error, logText(done))
	}
	commands, bootstraps, _ := srv.recorded()
	if !contains(commands, "sudo -S sh") {
		t.Fatalf("non-root bootstrap command = %v", commands)
	}
	if len(bootstraps) != 1 {
		t.Fatalf("bootstrap ran %d times", len(bootstraps))
	}
	// The sudo password answers the prompt as the first stdin line; the rest
	// is the script.
	firstLine, rest, _ := strings.Cut(bootstraps[0], "\n")
	if firstLine != "sudo-pass" {
		t.Fatalf("first stdin line = %q, want the sudo password", firstLine)
	}
	if !strings.HasPrefix(rest, "#!/bin/sh") {
		t.Fatalf("stdin did not continue with the script: %q", rest[:min(40, len(rest))])
	}
}

func TestDeployBootstrapFailureStops(t *testing.T) {
	f := newFixture(t)
	srv := startSSHServer(t, sshServerOptions{password: "pw", uid: "0"})
	srv.failBootstrap = true

	view, err := f.dep.Start(Options{Host: "127.0.0.1", Port: portOf(t, srv.addr), User: "root", Password: "pw"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJob(t, f.dep, view.ID, 10*time.Second)
	if done.State != StateFailed {
		t.Fatalf("state = %s, want failed", done.State)
	}
	if stepStateOf(done, stepBootstrap) != StepFailed {
		t.Fatalf("bootstrap step = %s", stepStateOf(done, stepBootstrap))
	}
	if stepStateOf(done, stepWait) != StepPending {
		t.Fatalf("wait step ran after a failed bootstrap: %s", stepStateOf(done, stepWait))
	}
	if !strings.Contains(done.Error, "引导脚本失败") {
		t.Fatalf("error = %q", done.Error)
	}
	if !strings.Contains(logText(done), "curl: not found") {
		t.Fatalf("remote stderr missing from the log: %v", logText(done))
	}
	// The enrollment stays pending (nothing joined), which is what lets the
	// operator retry or revoke it.
	if got := f.svc.ListEnrollments()[0].State; got != enroll.EnrollPending {
		t.Fatalf("enrollment state = %q", got)
	}
}

func TestDeployWaitTimeout(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Wait = 300 * time.Millisecond })
	srv := startSSHServer(t, sshServerOptions{password: "pw", uid: "0"})
	// The bootstrap succeeds but no node ever reports in.
	view, err := f.dep.Start(Options{Host: "127.0.0.1", Port: portOf(t, srv.addr), User: "root", Password: "pw"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJob(t, f.dep, view.ID, 10*time.Second)
	if done.State != StateFailed || !strings.Contains(done.Error, "等待回连超时") {
		t.Fatalf("state=%s err=%q", done.State, done.Error)
	}
}

func TestDeployHostKeyPinning(t *testing.T) {
	f := newFixture(t)
	srv := startSSHServer(t, sshServerOptions{password: "pw", uid: "0"})
	srv.onBootstrap = joinFromScript(t, f.svc)
	addr := srv.addr

	view, err := f.dep.Start(Options{Host: "127.0.0.1", Port: portOf(t, addr), User: "root", Password: "pw"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if done := waitJob(t, f.dep, view.ID, 10*time.Second); done.State != StateSucceeded {
		t.Fatalf("first job: %s %s", done.State, done.Error)
	}
	pin, ok := f.dep.pinFor(addr)
	if !ok || !strings.HasPrefix(pin, "SHA256:") {
		t.Fatalf("host key not pinned: %q ok=%v", pin, ok)
	}
	// A different key for the same host is a hard failure, before any
	// credential is offered.
	if err := f.dep.rememberPin(addr, "SHA256:someone-else"); err == nil {
		t.Fatal("rememberPin accepted a changed key")
	}
	// The pin survives a service restart (it is the same file the process
	// would load next time).
	reloaded, err := New(Config{Enroll: f.svc, Dist: f.dist, DataDir: f.dep.cfg.DataDir, Wait: time.Second})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again, ok := reloaded.pinFor(addr); !ok || again != pin {
		t.Fatalf("pin did not survive reload: %q ok=%v", again, ok)
	}
}

func TestDeployValidationCreatesNoEnrollment(t *testing.T) {
	f := newFixture(t)
	cases := []Options{
		{Host: "", User: "root", Password: "pw"},
		{Host: "127.0.0.1", User: "root"},                              // no auth
		{Host: "127.0.0.1", Port: 70000, User: "root", Password: "pw"}, // bad port
		{Host: "bad host", User: "root", Password: "pw"},
		{Host: "127.0.0.1", User: "root; rm -rf /", Password: "pw"},
		{Host: "127.0.0.1", User: "root", PrivateKey: "not a key"},
		{Host: "127.0.0.1", User: "root", Password: "pw", SudoPassword: "a\nb"},
	}
	for i, o := range cases {
		if _, err := f.dep.Start(o); err == nil {
			t.Fatalf("case %d accepted: %+v", i, o)
		}
	}
	if got := len(f.svc.ListEnrollments()); got != 0 {
		t.Fatalf("validation failures created %d enrollment(s)", got)
	}
}

// TestDeployViewHasNoSecrets guards the projection: the view is what reaches
// the console, and a credential must never be part of it.
func TestDeployViewHasNoSecrets(t *testing.T) {
	f := newFixture(t)
	srv := startSSHServer(t, sshServerOptions{password: "pw-9f2c-uniq", uid: "0"})
	srv.onBootstrap = joinFromScript(t, f.svc)
	view, err := f.dep.Start(Options{Host: "127.0.0.1", Port: portOf(t, srv.addr), User: "root", Password: "pw-9f2c-uniq"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, f.dep, view.ID, 10*time.Second)
	raw := fmt.Sprintf("%+v", done)
	for _, needle := range []string{"pw-9f2c-uniq", "zr1_", "curl -fsSL"} {
		if strings.Contains(raw, needle) {
			t.Fatalf("view leaks %q: %s", needle, raw)
		}
	}
}

func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func logText(view JobView) string {
	var b strings.Builder
	for _, line := range view.Log {
		b.WriteString(line.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
