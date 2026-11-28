// Package ws is the shell's thin adapter over github.com/gorilla/websocket
// (the same library the harness's pkg/aopws sits on). RFC 6455 itself —
// framing, masking, fragmentation, ping/pong, the close handshake — is
// gorilla's; what remains here is the shell-specific surface the rest of the
// repository is written against:
//
//   - ReadMessageType/WriteBinary/OpText: one connection carries both text
//     control frames and binary payload frames (terminal bytes, AOP protobuf).
//   - CloseError that unwraps to io.EOF: callers tell an intentional shutdown
//     (a close frame with a code) from a broken link with errors.As and
//     errors.Is on the same error.
//   - serialised writes with a per-write deadline, so a peer that stops
//     reading (suspended browser tab, half-open TCP) cannot hold the
//     connection — and Close can take the lock back.
//   - SetNetDialForTests: the narrow dial-level retry this host's tests need
//     (the machine occasionally drops SYN packets), never a transport switch.
//
// The contracts above are pinned by ws_gorilla_test.go; everything underneath
// is gorilla's test suite.
package ws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsDialer is the TCP dialer behind Dial. Its 15s timeout is part of the
// product contract: a control-plane call must not hang on a dead host.
var wsDialer = &net.Dialer{Timeout: 15 * time.Second}

// wsNetDial is the connect used by a ws:// handshake. It is a variable only so
// a test that cannot avoid a real socket can install the narrow dial-level
// retry allowed by docs/01-architecture.md §9 (this host occasionally drops the
// SYN, so connect fails after ~1s against a healthy listener). Production never
// assigns it, and nothing reads it from configuration or the environment.
var wsNetDial = wsDialer.DialContext

// SetNetDialForTests installs fn as the ws:// connect and returns a restore
// function. Test seam only: it exists so tests can retry a failed connect, not
// so callers can pick a transport.
func SetNetDialForTests(fn func(context.Context, string, string) (net.Conn, error)) func() {
	prev := wsNetDial
	wsNetDial = fn
	return func() { wsNetDial = prev }
}

// MaxMessage is the largest message either side will assemble (8 MiB). A frame
// bigger than this ends the connection instead of growing the heap.
const MaxMessage = 8 << 20

// Write and handshake bounds. A peer that stops reading (suspended browser tab,
// half-open TCP) would otherwise block Write while holding the write mutex,
// which also blocks Close: the connection and its owner leak. Deadlines turn
// both into an error the caller can act on.
//
// Variables, not constants, so tests can shorten them.
var (
	writeTimeout     = 30 * time.Second
	handshakeTimeout = 15 * time.Second
)

// OpText is the text-frame opcode, exported for callers that read frames and
// need to tell control text from terminal bytes.
const OpText = websocket.TextMessage

// Conn is one websocket connection. Gorilla enforces one concurrent reader and
// one concurrent writer; the write mutex below keeps every writer — keystrokes
// from the console, a resize, a control frame on close — framed atomically.
type Conn struct {
	c      *websocket.Conn
	mu     sync.Mutex
	closed bool
}

// Upgrade turns an HTTP request into a websocket connection. The caller has
// already authenticated the request: Upgrade does not look at the token, and it
// does not restrict Origin — the console serves the socket same-origin today
// and any client without an Origin header (curl, an agent) must keep working.
// A request that is not a websocket upgrade is answered with an HTTP error
// status by gorilla before the error returns.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	u := websocket.Upgrader{
		CheckOrigin:      func(*http.Request) bool { return true },
		HandshakeTimeout: handshakeTimeout,
	}
	c, err := u.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(MaxMessage)
	return &Conn{c: c}, nil
}

// Dial opens a client connection to ws:// or wss://host/path.
//
// Gorilla implements HandshakeTimeout as a context timeout, and pins the
// context deadline onto the connection: the handshake read is bounded by
// whichever comes first, the 15s handshake budget or the caller's deadline —
// the old contract that a host which accepts and then stalls cannot hold the
// caller past the context. TCP connect errors surface unwrapped, so the AOP
// client's connect-timeout retry keeps classifying them unchanged.
func Dial(ctx context.Context, rawURL string, header http.Header) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return nil, fmt.Errorf("不支持的 scheme：%s", u.Scheme)
	}
	d := &websocket.Dialer{
		NetDialContext:   wsNetDial,
		HandshakeTimeout: handshakeTimeout,
	}
	c, _, err := d.DialContext(ctx, rawURL, header)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(MaxMessage)
	return &Conn{c: c}, nil
}

// ReadMessage returns the next complete text or binary message.
func (c *Conn) ReadMessage() ([]byte, error) {
	_, payload, err := c.ReadMessageType()
	return payload, err
}

// ReadMessageType is ReadMessage with the opcode: text frames are control
// (resize, close) and binary frames are terminal bytes. Pings are answered
// transparently by gorilla's default handler; a close frame surfaces as
// *CloseError.
func (c *Conn) ReadMessageType() (byte, []byte, error) {
	op, payload, err := c.c.ReadMessage()
	if err != nil {
		return 0, nil, fromGorilla(err)
	}
	return byte(op), payload, nil
}

// WriteMessage sends one text message.
func (c *Conn) WriteMessage(p []byte) error { return c.write(websocket.TextMessage, p) }

// WriteBinary sends one binary message. AOP frames are protobuf, so they need
// the binary opcode.
func (c *Conn) WriteBinary(p []byte) error { return c.write(websocket.BinaryMessage, p) }

// CloseError is the peer's close frame: the peer ended the stream on purpose
// and said why. Flattening it into io.EOF drops the one fact an operator needs —
// whether an intentional shutdown or a broken link ended the turn — so the code
// and text are kept here. It still unwraps to io.EOF: callers that only care
// that the stream ended keep working.
type CloseError struct {
	Code int
	Text string
}

func (e *CloseError) Error() string {
	if e.Text == "" {
		return fmt.Sprintf("对端关闭了连接（code=%d）", e.Code)
	}
	return fmt.Sprintf("对端关闭了连接（code=%d，%s）", e.Code, e.Text)
}

// Unwrap keeps end-of-stream detection working: errors.Is(err, io.EOF) holds for
// a close frame.
func (e *CloseError) Unwrap() error { return io.EOF }

// fromGorilla translates gorilla's close-frame error into ours; every other
// read error (a TCP EOF, a deadline) passes through unchanged so downstream
// attribution keeps working.
func fromGorilla(err error) error {
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		return &CloseError{Code: ce.Code, Text: ce.Text}
	}
	return err
}

func (c *Conn) write(op int, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("连接已关闭")
	}
	// The write deadline is what makes Close() able to win the mutex: without
	// it a peer that stopped reading blocks this Write (and the lock) forever.
	_ = c.c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.c.WriteMessage(op, payload)
}

// Close closes the underlying connection. Gorilla's Close sends no close
// frame and waits for nothing, matching the old behaviour: the AOP layer
// already said goodbye at the protocol level (CancelWatch) and an abrupt TCP
// close is what releases a reader blocked on a dead socket.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	// Fail any write still waiting on a stalled peer immediately.
	_ = c.c.SetWriteDeadline(time.Now())
	return c.c.Close()
}

// SetReadDeadline bounds how long a read may block (zero clears it).
func (c *Conn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }
