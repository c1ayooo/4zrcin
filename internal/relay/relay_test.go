//go:build remtunnel

package relay

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	_ "github.com/chainreactors/rem/protocol/tunnel/websocket"
	_ "github.com/chainreactors/rem/protocol/wrapper"
	remrunner "github.com/chainreactors/rem/runner"
)

// The reverse connection is rem's own reverse serve: the platform dials out,
// the relay opens the listener on its side, and a request to that listener
// arrives at the platform's console (docs/14 §7.5).
//
// The relay runs in a subprocess: rem keeps one process-global agent registry
// keyed by the login alias, so a server and a client sharing a process
// overwrite each other's agents. In production the relay is another host.
func TestRelayServerHelper(t *testing.T) {
	cmdline := os.Getenv("ZRCIN_RELAY_SERVER_CMD")
	if cmdline == "" {
		t.Skip("subprocess helper")
	}
	var opt remrunner.Options
	if err := opt.ParseArgs(strings.Fields(cmdline)); err != nil {
		t.Fatalf("relay args: %v", err)
	}
	r, err := opt.Prepare()
	if err != nil {
		t.Fatalf("relay prepare: %v", err)
	}
	r.URLs.ConsoleURL = r.ConsoleURLs[0]
	con, err := remrunner.NewConsole(r, r.URLs)
	if err != nil {
		t.Fatalf("relay console: %v", err)
	}
	if err := con.Run(); err != nil {
		t.Fatalf("relay run: %v", err)
	}
}

func startRelayServer(t *testing.T, port int, key string) {
	t.Helper()
	proc := exec.Command(os.Args[0], "-test.run=^TestRelayServerHelper$")
	proc.Env = append(os.Environ(), "ZRCIN_RELAY_SERVER_CMD="+strings.Join([]string{
		"-s", fmt.Sprintf("ws://127.0.0.1:%d/relay?wrapper=raw", port),
		"-i", "127.0.0.1", "-k", key, "--no-sub", "-q",
	}, " "))
	var out strings.Builder
	proc.Stdout, proc.Stderr = &out, &out
	if err := proc.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() {
		_ = proc.Process.Kill()
		_ = proc.Wait()
		if t.Failed() {
			t.Logf("relay output:\n%s", out.String())
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			conn.Close() //nolint:errcheck
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never started listening")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReverseConnectionCarriesTheConsole(t *testing.T) {
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello from the platform: %s", r.URL.Path)
	}))
	t.Cleanup(console.Close)

	serverPort, listenPort := freePort(t), freePort(t)
	key := "relay-test-key-0123456789abcd"
	startRelayServer(t, serverPort, key)

	relay, err := Prepare(Config{
		Server: fmt.Sprintf("ws://127.0.0.1:%d/relay", serverPort),
		Key:    key,
		Serves: []Serve{{
			Listen: fmt.Sprintf("127.0.0.1:%d", listenPort),
			Dest:   console.Listener.Addr().String(),
		}},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); relay.Close() })
	go relay.Run(ctx)

	// Through the relay-side listener — the address the public entry's proxy
	// would forward to — the request lands on the platform's console.
	want := "hello from the platform: /tunnel/check"
	ok := false
	deadline := time.Now().Add(20 * time.Second)
	for !ok && time.Now().Before(deadline) {
		resp, gerr := http.Get(fmt.Sprintf("http://127.0.0.1:%d/tunnel/check", listenPort))
		if gerr == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close() //nolint:errcheck
			ok = resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == want
		}
		if !ok {
			time.Sleep(150 * time.Millisecond)
		}
	}
	if !ok {
		t.Fatal("the request never reached the platform through the reverse connection")
	}
}

func TestPrepareRejectsUnusableConfig(t *testing.T) {
	cases := map[string]Config{
		"no server":    {Key: "k"},
		"bad scheme":   {Server: "tcp://host:1", Key: "k"},
		"no key":       {Server: "wss://host/relay"},
		"key too long": {Server: "wss://host/relay", Key: strings.Repeat("k", 32)},
		"no serves":    {Server: "wss://host/relay", Key: "k"},
		"listen slash": {Server: "wss://host/relay", Key: "k",
			Serves: []Serve{{Listen: "0.0.0.0:1/x", Dest: "127.0.0.1:2"}}},
		"dest slash": {Server: "wss://host/relay", Key: "k",
			Serves: []Serve{{Listen: "127.0.0.1:1", Dest: "host/x"}}},
	}
	for name, cfg := range cases {
		if _, err := Prepare(cfg); err == nil {
			t.Errorf("%s: accepted %+v", name, cfg)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck
	return ln.Addr().(*net.TCPAddr).Port
}
