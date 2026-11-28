package ws

// The adapter's own contracts, over gorilla. The framing underneath (masking,
// fragmentation, ping/pong defaults, close handshake, UTF-8, limit handling)
// is gorilla's and is pinned by that library's test suite; what this file pins
// is the surface the shell depends on:
//
//   - text and binary round-trips with the opcode kept (ReadMessageType);
//   - a close frame surfacing as *CloseError with code and text, and still
//     reading as io.EOF (DisconnectReason's three-way attribution depends on
//     it); an empty close payload naming 1005, not 0;
//   - a write to a stalled peer failing at the write deadline, and Close
//     reclaiming the connection instead of waiting on the stalled write;
//   - a handshake against a host that accepts and stalls failing inside the
//     caller's context (gorilla pins the ctx deadline onto the connection);
//   - the dial seam: the contract dialer stays installed by default, a test
//     install is used and restorable, and nothing outside _test.go touches it;
//   - a close frame ending one connection without poisoning the endpoint.

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/testutil"
	"github.com/gorilla/websocket"
)

// TestEchoRoundTripKeepsTheOpcode covers text and binary (the two kinds the
// shell multiplexes) through a real server Upgrade and client Dial, including
// a payload larger than one frame.
func TestEchoRoundTripKeepsTheOpcode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			op, msg, err := c.ReadMessageType()
			if err != nil {
				return
			}
			payload := append([]byte("echo:"), msg...)
			if op == OpText {
				err = c.WriteMessage(payload)
			} else {
				err = c.WriteBinary(payload)
			}
			if err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialTestServer(t, ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, tc := range []struct {
		name    string
		text    bool
		payload string
	}{
		{"text", true, "hello"},
		{"binary", false, "hello"},
		{"large text", true, strings.Repeat("x", 70_000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.text {
				err = c.WriteMessage([]byte(tc.payload))
			} else {
				err = c.WriteBinary([]byte(tc.payload))
			}
			if err != nil {
				t.Fatal(err)
			}
			op, got, err := c.ReadMessageType()
			if err != nil {
				t.Fatal(err)
			}
			wantOp := byte(websocket.BinaryMessage)
			if tc.text {
				wantOp = OpText
			}
			if op != wantOp {
				t.Fatalf("opcode = %d, want %d", op, wantOp)
			}
			if string(got) != "echo:"+tc.payload {
				t.Fatalf("echo mismatch: %d bytes back", len(got))
			}
		})
	}
}

func TestUpgradeRejectsPlainGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := Upgrade(w, r); err == nil {
			t.Fatal("plain GET must not upgrade")
		}
	}))
	defer srv.Close()
	resp, err := testutil.RetryConnect(t, "plain GET", func() (*http.Response, error) { return http.Get(srv.URL) })
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

// A ping is answered by gorilla's default handler and must not surface as a
// message: the server pings and then sends text, and the client sees only the
// text.
func TestPingIsAnsweredTransparently(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The raw gorilla connection sends the ping; the adapter must answer it
		// through gorilla's default ping handler and skip it on read.
		u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteControl(websocket.PingMessage, []byte("are you there"), time.Now().Add(time.Second))
		_ = c.WriteMessage(websocket.TextMessage, []byte("after-ping"))
		_, _, _ = c.ReadMessage() // wait for the pong (or the close)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialTestServer(t, ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	msg, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "after-ping" {
		t.Fatalf("got %q", msg)
	}
}

// A peer that ends the stream on purpose says why. That fact has to survive the
// read path: flattening a close frame into io.EOF made an intentional shutdown
// and a broken link indistinguishable, which is the whole point of keeping the
// code and text.
func TestCloseFrameKeepsThePeersReason(t *testing.T) {
	srv := closeOnceServer(t, websocket.FormatCloseMessage(1001, "host closing"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialTestServer(t, ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, err = c.ReadMessage()
	var closed *CloseError
	if !errors.As(err, &closed) {
		t.Fatalf("a close frame must surface as *CloseError, got %v", err)
	}
	if closed.Code != 1001 || closed.Text != "host closing" {
		t.Fatalf("close = %d %q", closed.Code, closed.Text)
	}
	// Callers that only care that the stream ended keep working.
	if !errors.Is(err, io.EOF) {
		t.Fatal("a close frame stopped being end-of-stream for errors.Is(err, io.EOF)")
	}
}

// An empty close payload carries no code; RFC 6455 names that 1005, not 0.
func TestEmptyCloseFrameIsCode1005(t *testing.T) {
	srv := closeOnceServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialTestServer(t, ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, err = c.ReadMessage()
	var closed *CloseError
	if !errors.As(err, &closed) {
		t.Fatalf("an empty close frame must surface as *CloseError, got %v", err)
	}
	if closed.Code != 1005 || closed.Text != "" {
		t.Fatalf("close = %d %q, want 1005 with no text", closed.Code, closed.Text)
	}
}

// A close frame ends that connection, not the endpoint. Every AOP attempt dials
// fresh, so a close path that poisoned the next Dial would break the retry.
func TestDialAfterPeerCloseStillConnects(t *testing.T) {
	var mu sync.Mutex
	conns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		mu.Lock()
		conns++
		first := conns == 1
		mu.Unlock()
		if first {
			_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1001, "host closing"), time.Now().Add(time.Second))
			return
		}
		_, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		_ = c.WriteMessage(websocket.TextMessage, append([]byte("echo:"), msg...))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	first, err := testutil.RetryConnect(t, "ws dial after a peer close (first)", func() (*Conn, error) {
		return Dial(ctx, url, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.ReadMessage(); err == nil {
		t.Fatal("the first connection did not report the peer's close")
	}
	_ = first.Close()

	second, err := testutil.RetryConnect(t, "ws redial after a peer close", func() (*Conn, error) {
		return Dial(ctx, url, nil)
	})
	if err != nil {
		t.Fatalf("redial after a peer close failed: %v", err)
	}
	defer second.Close()
	if err := second.WriteMessage([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	got, err := second.ReadMessage()
	if err != nil || string(got) != "echo:hi" {
		t.Fatalf("second connection = %q, %v", got, err)
	}
}

// A host that accepts the TCP connection and never answers the upgrade must not
// hold the caller past its context: gorilla pins the ctx deadline onto the
// connection, so the handshake read dies with it.
func TestDialHandshakeIsBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		time.Sleep(2 * time.Second) // accept, never respond
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Dial(ctx, "ws://"+ln.Addr().String()+"/x", nil); err == nil {
		t.Fatal("handshake against a mute host must fail")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("handshake blocked for %s", d)
	}
}

// pipePair hands back a gorilla connection over a net.Pipe whose far end is
// answered by hand. net.Pipe has no buffering, so a write with nobody reading
// blocks deterministically — that is what the two stalled-peer tests below
// need without a real socket.
func pipePair(t *testing.T) *Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	respSeen := make(chan struct{})
	go func() {
		br := bufio.NewReader(server)
		req, err := http.ReadRequest(br)
		if err != nil {
			close(respSeen)
			return
		}
		// RFC 6455 §4.2.2: the accept key is base64(sha1(key + GUID)).
		h := sha1.New()
		_, _ = io.WriteString(h, req.Header.Get("Sec-WebSocket-Key")+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
		accept := base64.StdEncoding.EncodeToString(h.Sum(nil))
		_, _ = server.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"))
		close(respSeen)
	}()

	c, _, err := (&websocket.Dialer{
		NetDial: func(string, string) (net.Conn, error) { return client, nil },
	}).Dial("ws://pipe.invalid/x", nil)
	if err != nil {
		// The accept key must match gorilla's challenge or the test setup
		// itself is wrong.
		t.Fatalf("canned handshake failed: %v", err)
	}
	<-respSeen
	return &Conn{c: c}
}

// A peer that stops reading must not block Write forever: the per-write
// deadline turns the stall into an error the caller can act on.
func TestWriteTimesOutWhenPeerDoesNotRead(t *testing.T) {
	old := writeTimeout
	writeTimeout = 80 * time.Millisecond
	defer func() { writeTimeout = old }()

	c := pipePair(t)

	start := time.Now()
	err := c.WriteBinary(make([]byte, 1<<20))
	if err == nil {
		t.Fatal("a write to a stalled peer must fail, not block")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("write blocked for %s", d)
	}
}

// Close must be able to take the lock back from a stalled write instead of
// waiting on it — the connection and its owner would otherwise leak behind a
// suspended browser tab.
func TestCloseDoesNotWaitForAStalledWrite(t *testing.T) {
	old := writeTimeout
	writeTimeout = 150 * time.Millisecond
	defer func() { writeTimeout = old }()

	c := pipePair(t)
	go func() { _ = c.WriteBinary(make([]byte, 1<<20)) }()
	time.Sleep(20 * time.Millisecond) // let the write take the mutex

	start := time.Now()
	_ = c.Close()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Close waited %s for a stalled write", d)
	}
}

// The seam must not change the default: ws:// keeps dialing through a 15s
// contract dialer unless a test explicitly installs something else.
func TestWSDialKeepsTheContractDialerByDefault(t *testing.T) {
	if wsNetDial == nil {
		t.Fatal("wsNetDial is nil: ws:// would panic instead of dialing")
	}
	if wsDialer == nil {
		t.Fatal("wsDialer is nil: wss:// would panic instead of dialing")
	}
	if wsDialer.Timeout != 15*time.Second {
		t.Fatalf("wsDialer.Timeout = %s, want 15s", wsDialer.Timeout)
	}
	if handshakeTimeout != 15*time.Second {
		t.Fatalf("handshakeTimeout = %s, want 15s", handshakeTimeout)
	}
	if writeTimeout != 30*time.Second {
		t.Fatalf("writeTimeout = %s, want 30s", writeTimeout)
	}
}

// A test that installs a connect uses it for ws://, and restoring brings the
// contract dialer back.
func TestSetNetDialForTestsIsUsedForWSAndRestorable(t *testing.T) {
	before := wsNetDial
	used := false
	restore := SetNetDialForTests(func(ctx context.Context, network, addr string) (net.Conn, error) {
		used = true
		return nil, errors.New("seam")
	})
	_, err := Dial(context.Background(), "ws://seam.invalid/x", nil)
	restore()
	if !used {
		t.Fatal("ws:// dial did not go through the installed connect")
	}
	if err == nil {
		t.Fatal("the installed connect returned an error that Dial swallowed")
	}
	if reflect.ValueOf(wsNetDial).Pointer() != reflect.ValueOf(before).Pointer() {
		t.Fatal("restore did not put the contract dialer back")
	}
}

// SetNetDialForTests is a test seam, not a transport switch. Nothing outside a
// _test.go file may call it: the day production code reaches for it, the seam
// has become a public extension point and the "default is untouched" guarantee
// is gone. This walks the whole repo so the misuse shows up wherever it starts.
func TestSetNetDialForTestsIsNeverCalledFromProductCode(t *testing.T) {
	root := filepath.Join("..", "..")
	skip := func(path string, d fs.DirEntry) bool {
		if !d.IsDir() {
			return false
		}
		switch d.Name() {
		case ".git", "node_modules", "vendor", "testdata":
			return true
		}
		return strings.Contains(filepath.ToSlash(path), "web/dist") || strings.Contains(filepath.ToSlash(path), "internal/web/bundle")
	}
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip(path, d) {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, "SetNetDialForTests") {
				continue
			}
			if strings.HasPrefix(trimmed, "//") {
				continue // a comment may name it; only a call is a misuse
			}
			if strings.Contains(trimmed, "func SetNetDialForTests") {
				continue // the declaration itself is fine; a call is not
			}
			offenders = append(offenders, filepath.ToSlash(path)+": "+trimmed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("SetNetDialForTests is called from non-test code: %v", offenders)
	}
}

// The helper exists so a stalled first connect does not fail the test: install a
// dialer that drops the first SYN and check the handshake still completes, on a
// second attempt. Without the retry this test hangs on the synthetic timeout.
func TestDialTestServerRetriesAStalledFirstConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage([]byte("hello"))
	}))
	defer srv.Close()

	var attempts int32
	restore := SetNetDialForTests(func(ctx context.Context, network, addr string) (net.Conn, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: os.ErrDeadlineExceeded}
		}
		return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, addr)
	})
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.FixtureWindow)
	defer cancel()
	c, err := dialTestServer(t, ctx, srv)
	if err != nil {
		t.Fatalf("dial after one stalled connect: %v", err)
	}
	defer c.Close()
	got, err := c.ReadMessage()
	if err != nil || string(got) != "hello" {
		t.Fatalf("greeting = %q, %v", got, err)
	}
	if n := atomic.LoadInt32(&attempts); n < 2 {
		t.Fatalf("the stalled connect was not retried: %d attempt(s)", n)
	}
}

// closeOnceServer upgrades one connection with the raw gorilla server (so the
// test can put a close frame on the wire) and then sends that frame.
func closeOnceServer(t *testing.T, closeFrame []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		if closeFrame == nil {
			_ = c.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second))
			return
		}
		_ = c.WriteControl(websocket.CloseMessage, closeFrame, time.Now().Add(time.Second))
	}))
}

// dialTestServer is how the ws tests open a real socket. This host drops SYNs
// for seconds at a time (measured 2-12s, idle machine included), and "the test
// server is up but the connect timed out" is not a fact about the code under
// test. Only the connect phase is retried, with the shared testutil budget: a
// handshake answer, a close frame or a protocol error still fails on the first
// attempt — TestDialHandshakeIsBounded deliberately keeps its bare Dial.
func dialTestServer(t *testing.T, ctx context.Context, srv *httptest.Server) (*Conn, error) {
	t.Helper()
	return testutil.RetryConnect(t, "ws dial", func() (*Conn, error) {
		return Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	})
}
