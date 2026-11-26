//go:build remtunnel

package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Two reverse serves over one client console: the first carries console HTTP,
// the second a second local service (the node tunnel's server in production).
// If rem drops the extra serve, the console API through the entry dies.
func TestTwoReverseServesBothCarry(t *testing.T) {
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "console:%s", r.URL.Path)
	}))
	t.Cleanup(console.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "second:%s", r.URL.Path)
	}))
	t.Cleanup(second.Close)

	serverPort := freePort(t)
	key := "relay-two-serve-key-012345"
	startRelayServer(t, serverPort, key)

	listen1, listen2 := freePort(t), freePort(t)
	relay, err := Prepare(Config{
		Server: fmt.Sprintf("ws://127.0.0.1:%d/relay", serverPort),
		Key:    key,
		Serves: []Serve{
			{Listen: fmt.Sprintf("127.0.0.1:%d", listen1), Dest: console.Listener.Addr().String()},
			{Listen: fmt.Sprintf("127.0.0.1:%d", listen2), Dest: second.Listener.Addr().String()},
		},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); relay.Close() })
	go relay.Run(ctx)

	fetch := func(port int, path string) (string, error) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close() //nolint:errcheck
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil
	}
	deadline := time.Now().Add(20 * time.Second)
	var body1, body2 string
	for time.Now().Before(deadline) {
		body1, _ = fetch(listen1, "/console-check")
		body2, _ = fetch(listen2, "/second-check")
		if body1 == "console:/console-check" && body2 == "second:/second-check" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("serves did not both carry: listen1=%q listen2=%q", body1, body2)
}
