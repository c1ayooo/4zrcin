//go:build remtunnel

package tunnel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	remrunner "github.com/chainreactors/rem/runner"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// Tunnel end-to-end: a node holding a valid ticket reaches exactly the two
// loopback services it is allowed to reach, and nothing else (docs/15 §3.4).
//
// The rem client runs in a subprocess: rem agents register in one package-
// global registry keyed by the login alias, so a client and the platform's
// server-side agent cannot both live in this process.

// TestTunnelClientHelper runs one rem client console when the parent test
// asks for it (the same pattern the rem e2e suite uses).
func TestTunnelClientHelper(t *testing.T) {
	cmdline := os.Getenv("ZRCIN_TUNNEL_CLIENT_CMD")
	if cmdline == "" {
		t.Skip("subprocess helper")
	}
	con, err := remrunner.NewConsoleWithCMD(cmdline)
	if err != nil {
		t.Fatalf("client console: %v", err)
	}
	if err := con.Run(); err != nil {
		t.Fatalf("client run: %v", err)
	}
}

func startTunnelClient(t *testing.T, cmdline string) {
	t.Helper()
	proc := exec.Command(os.Args[0], "-test.run=^TestTunnelClientHelper$")
	proc.Env = append(os.Environ(), "ZRCIN_TUNNEL_CLIENT_CMD="+cmdline)
	var out bytes.Buffer
	proc.Stdout, proc.Stderr = &out, &out
	if err := proc.Start(); err != nil {
		t.Fatalf("start client: %v", err)
	}
	t.Cleanup(func() {
		_ = proc.Process.Kill()
		_ = proc.Wait()
		if t.Failed() {
			t.Logf("client output:\n%s", out.String())
		}
	})
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	return ln.Addr().(*net.TCPAddr).Port
}

// echoListener stands in for one of the platform's loopback services (the Hub
// and the console API): the tunnel may reach it, and only it.
func echoListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listener: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(c, c) //nolint:errcheck // echo
				c.Close()     //nolint:errcheck
			}(conn)
		}
	}()
	return ln
}

// roundTripThrough reports whether a payload sent to the local listener comes
// back. A refused serve is a listener that accepts and then closes: the read
// fails instead of echoing.
func roundTripThrough(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			time.Sleep(150 * time.Millisecond)
			continue
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Write([]byte("ping"))
		buf := make([]byte, 4)
		_, rerr := io.ReadFull(conn, buf)
		_ = conn.Close()
		if rerr == nil {
			return true
		}
		return false
	}
	return false
}

type tunnelFixture struct {
	svc      *enroll.Service
	ingress  Ingress
	server   *httptest.Server
	nodeID   string
	nodeCred string
	hubAddr  string
	conAddr  string
}

func newTunnelFixture(t *testing.T) *tunnelFixture {
	t.Helper()
	hubEcho := echoListener(t)
	conEcho := echoListener(t)
	dir := t.TempDir()
	store, err := enroll.Open(filepath.Join(dir, "remote-nodes.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	issueKey := []byte("0123456789abcdef0123456789abcdef")
	svc := enroll.New(store, enroll.Options{
		PublicURL:   "https://ops.example.com",
		DataDir:     "/srv/zrcin/data",
		ConsoleAddr: conEcho.Addr().String(),
		HubAddr:     hubEcho.Addr().String(),
		HubKey:      "hub-key",
		IssueKey:    issueKey,
	})
	created, err := svc.Create(enroll.CreateOptions{})
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	joined, err := svc.Join(created.Token, "install-1")
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	ing, err := Start(svc, Config{
		PublicURL:   "https://ops.example.com",
		Key:         "test-tunnel-key",
		HubAddr:     hubEcho.Addr().String(),
		ConsoleAddr: conEcho.Addr().String(),
	})
	if err != nil {
		t.Fatalf("tunnel start: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close(joined.NodeID) })

	nodeIngress := enroll.NewIngress(svc)
	nodeIngress.Register("/node/ticket", ing.TicketHandler())
	nodeIngress.RegisterPrefix("/tunnel/", ing.TunnelHandler())
	srv := httptest.NewServer(nodeIngress)
	t.Cleanup(srv.Close)

	return &tunnelFixture{
		svc: svc, ingress: ing, server: srv,
		nodeID: joined.NodeID, nodeCred: joined.NodeCredential,
		hubAddr: hubEcho.Addr().String(), conAddr: conEcho.Addr().String(),
	}
}

// ticket asks the platform for a dial URL, exactly as the launcher does.
func (f *tunnelFixture) ticket(t *testing.T, credential string) (dialURL string, status int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/node/ticket", nil)
	if err != nil {
		t.Fatal(err)
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ticket request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode
	}
	var body struct {
		Ticket string `json:"ticket"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("ticket body: %v", err)
	}
	// The httptest server is plain http; the launcher-facing URL is wss. For
	// this test dial the test server itself.
	u := strings.TrimPrefix(body.URL, "wss://")
	u = strings.TrimPrefix(u, "ws://")
	// The URL already carries ?wrapper=raw; only the scheme and host change
	// (the ticket response renders wss for the launcher, the test server is
	// plain http).
	if i := strings.Index(u, "/tunnel/"); i >= 0 {
		return "ws://" + f.server.Listener.Addr().String() + u[i:], resp.StatusCode
	}
	return "", resp.StatusCode
}

func TestTunnelEndToEnd(t *testing.T) {
	f := newTunnelFixture(t)
	dialURL, status := f.ticket(t, f.nodeCred)
	if status != http.StatusOK || dialURL == "" {
		t.Fatalf("ticket status=%d url=%q", status, dialURL)
	}

	hubPort, conPort := freeTCPPort(t), freeTCPPort(t)
	startTunnelClient(t, fmt.Sprintf(
		"-c %s -k test-tunnel-key -q -a %s -l port://127.0.0.1:%d/%s -l port://127.0.0.1:%d/%s",
		dialURL, f.nodeID, hubPort, f.hubAddr, conPort, f.conAddr))

	if !roundTripThrough(hubPort, 25*time.Second) {
		t.Fatal("the Hub forward never echoed")
	}
	if !roundTripThrough(conPort, 10*time.Second) {
		t.Fatal("the console forward never echoed")
	}
	if got := f.svc.ListEnrollments()[0].State; got != enroll.EnrollJoined {
		t.Fatalf("enrollment state = %q, want joined (the tunnel spends the token)", got)
	}
	if info, ok := f.ingress.Session(f.nodeID); !ok || !info.Up {
		t.Fatalf("session for %s = %+v ok=%v", f.nodeID, info, ok)
	}
}

func TestTunnelTicketRules(t *testing.T) {
	f := newTunnelFixture(t)

	if _, status := f.ticket(t, ""); status != http.StatusUnauthorized {
		t.Fatalf("ticket without a credential = %d", status)
	}
	if _, status := f.ticket(t, "zn1_"+strings.Repeat("0", 32)+"_"+strings.Repeat("A", 43)); status != http.StatusUnauthorized {
		t.Fatalf("ticket with a foreign credential = %d", status)
	}

	dialURL, status := f.ticket(t, f.nodeCred)
	if status != http.StatusOK {
		t.Fatalf("ticket = %d", status)
	}
	// The dial URL carries a one-time ticket; a second request with the same
	// ticket must not reach the rem server.
	path := dialURL[strings.Index(dialURL, "/tunnel/"):]
	resp, err := http.Get(f.server.URL + path)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a replayed ticket opened a second tunnel session")
	}
}

// The policy fences: a ticket only admits the node it was issued for, and a
// node may only reach the two loopback destinations.
func TestTunnelPolicyFences(t *testing.T) {
	t.Run("alias mismatch", func(t *testing.T) {
		f := newTunnelFixture(t)
		dialURL, status := f.ticket(t, f.nodeCred)
		if status != http.StatusOK {
			t.Fatalf("ticket = %d", status)
		}
		port := freeTCPPort(t)
		startTunnelClient(t, fmt.Sprintf(
			"-c %s -k test-tunnel-key -q -a 00000000000000000000000000000000 -l port://127.0.0.1:%d/%s",
			dialURL, port, f.hubAddr))
		if roundTripThrough(port, 12*time.Second) {
			t.Fatal("a handshake with the wrong login alias reached the data path")
		}
	})

	t.Run("destination outside the fence", func(t *testing.T) {
		f := newTunnelFixture(t)
		evil := echoListener(t) // a valid echo, but not one of the two services
		dialURL, status := f.ticket(t, f.nodeCred)
		if status != http.StatusOK {
			t.Fatalf("ticket = %d", status)
		}
		port := freeTCPPort(t)
		startTunnelClient(t, fmt.Sprintf(
			"-c %s -k test-tunnel-key -q -a %s -l port://127.0.0.1:%d/%s",
			dialURL, f.nodeID, port, evil.Addr().String()))
		if roundTripThrough(port, 12*time.Second) {
			t.Fatal("the tunnel made the platform dial an address outside the fence")
		}
	})
}
