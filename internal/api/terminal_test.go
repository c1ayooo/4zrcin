package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/kksty/tianwu/internal/aop/pb"
	"github.com/kksty/tianwu/internal/aop/pty"
	"github.com/kksty/tianwu/internal/executor"
	"github.com/kksty/tianwu/internal/testutil"
	"github.com/kksty/tianwu/internal/ws"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// ptyHost is a minimal AOP host that serves one PTY stream.
func ptyHost(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cyber.rpc.agent.AgentService/ListAgents" {
			_, _ = w.Write([]byte(`{"agents":[{"hello":{"nodeId":"0123456789abcdef0123456789abcdef","name":"local"}}]}`))
			return
		}
		conn, err := ws.Upgrade(w, r)
		if err != nil {
			return
		}
		defer conn.Close()
		send := func(msg *pty.ProtocolMessage) {
			payload, _ := anypb.New(msg)
			raw, _ := proto.Marshal(&pb.Envelope{Id: msgStream(msg), Payload: payload})
			_ = conn.WriteBinary(raw)
		}
		for {
			raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env pb.Envelope
			if proto.Unmarshal(raw, &env) != nil || env.Payload == nil {
				continue
			}
			var msg pty.ProtocolMessage
			if env.Payload.UnmarshalTo(&msg) != nil {
				continue
			}
			switch v := msg.Message.(type) {
			case *pty.ProtocolMessage_Open:
				id := v.Open.GetStreamId()
				send(&pty.ProtocolMessage{Message: &pty.ProtocolMessage_Opened{Opened: &pty.Opened{StreamId: id}}})
				send(&pty.ProtocolMessage{Message: &pty.ProtocolMessage_Output{Output: &pty.Output{StreamId: id, Data: []byte("hello")}}})
			case *pty.ProtocolMessage_Input:
				send(&pty.ProtocolMessage{Message: &pty.ProtocolMessage_Output{
					Output: &pty.Output{StreamId: v.Input.GetStreamId(), Data: append([]byte("echo:"), v.Input.GetData()...)},
				}})
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func msgStream(msg *pty.ProtocolMessage) string {
	if out := msg.GetOutput(); out != nil {
		return out.GetStreamId()
	}
	return ""
}

type termExec struct{ url string }

func (termExec) Name() string { return "term" }
func (termExec) Run(context.Context, executor.Request, func(executor.Event)) (executor.Result, error) {
	return executor.Result{ExitCode: 0}, nil
}
func (termExec) Nodes() []executor.NodeInfo {
	return []executor.NodeInfo{{ID: "lab", Transport: "aop", Endpoint: "http://host"}}
}
func (termExec) ResolveNode(string) string { return "lab" }
func (e termExec) TerminalEndpoint(string) (string, string, string, bool) {
	return e.url, "tok", "local", true
}

// terminalOpen is one successful bridge open: the socket and the greeting frame
// the server sent first.
type terminalOpen struct {
	conn     *ws.Conn
	greeting string
}

// hostUnreachable is "the bridge could not reach its host", reconstructed from
// the fatal frame the server sends instead of returning the dial error. The
// original *net.OpError is gone by the time the test sees it, so the retry
// predicate in testutil.RetryConnect (which looks for a dial OpError) cannot
// recognise it; this type is what RetryConnectWhen gets to inspect.
type hostUnreachable struct{ message string }

func (e *hostUnreachable) Error() string {
	return "bridge could not reach its host: " + e.message
}

// hostConnectFailure returns the message of a fatal exit frame whose cause is a
// connection-establishment failure, and "" for every other frame. Only connect
// vocabulary counts: a refusal, an auth error or a broken stream after the
// connection existed must not be retried.
func hostConnectFailure(frame []byte) string {
	var parsed struct {
		Fatal   bool   `json:"fatal"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(frame, &parsed) != nil || !parsed.Fatal || parsed.Type != "exit" {
		return ""
	}
	for _, marker := range []string{"dial tcp", "connectex:", "connect: ", "no route to host"} {
		if strings.Contains(parsed.Message, marker) {
			return parsed.Message
		}
	}
	return ""
}

func TestNodeTerminalBridgesBytes(t *testing.T) {
	host := ptyHost(t)
	s := New(nil, termExec{url: host.URL}, t.TempDir(), t.TempDir())
	mux := http.NewServeMux()
	s.Register(mux)
	front := httptest.NewServer(mux)
	defer front.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.FixtureWindow)
	defer cancel()
	url := "ws" + strings.TrimPrefix(front.URL, "http") + "/api/nodes/lab/terminal?cols=80&rows=24"
	// Opening the bridge is not just the front dial: the server then reaches its
	// host over HTTP to attach the stream, and when that dial is dropped the
	// server answers with a fatal frame instead of an error. So the whole open is
	// what gets retried, and only when the greeting is that connect failure — a
	// wrong greeting, a refusal or a protocol error still fails on the first try.
	opened, err := testutil.RetryConnectWhen(t, "terminal open", func() (terminalOpen, error) {
		conn, err := ws.Dial(ctx, url, nil)
		if err != nil {
			return terminalOpen{}, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(testutil.FixtureAttempt))
		data, readErr := conn.ReadMessage()
		if readErr != nil {
			_ = conn.Close()
			return terminalOpen{}, readErr
		}
		if msg := hostConnectFailure(data); msg != "" {
			_ = conn.Close()
			return terminalOpen{}, &hostUnreachable{message: msg}
		}
		return terminalOpen{conn: conn, greeting: string(data)}, nil
	}, func(err error) bool {
		var unreachable *hostUnreachable
		return errors.As(err, &unreachable)
	})
	if err != nil {
		t.Fatalf("open bridge: %v", err)
	}
	conn := opened.conn
	defer conn.Close()

	read := func() string {
		_ = conn.SetReadDeadline(time.Now().Add(testutil.FixtureAttempt))
		data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return string(data)
	}
	if opened.greeting != "hello" {
		t.Fatalf("greeting = %q", opened.greeting)
	}
	if err := conn.WriteBinary([]byte("ls")); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "echo:ls" {
		t.Fatalf("echo = %q", got)
	}
}

// mode=repl must attach the host's singleton `main-repl` instead of opening a
// second shell: the kind/name/singleton triple is the contract with harness
// (pkg/console.StartPersistent).
func TestNodeTerminalReplAttachesSingletonMainRepl(t *testing.T) {
	opens := make(chan *pty.Open, 1)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cyber.rpc.agent.AgentService/ListAgents" {
			_, _ = w.Write([]byte(`{"agents":[{"hello":{"nodeId":"0123456789abcdef0123456789abcdef","name":"local"}}]}`))
			return
		}
		conn, err := ws.Upgrade(w, r)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env pb.Envelope
			if proto.Unmarshal(raw, &env) != nil || env.Payload == nil {
				continue
			}
			var msg pty.ProtocolMessage
			if env.Payload.UnmarshalTo(&msg) != nil {
				continue
			}
			open, ok := msg.Message.(*pty.ProtocolMessage_Open)
			if !ok {
				continue
			}
			opens <- open.Open
			payload, _ := anypb.New(&pty.ProtocolMessage{Message: &pty.ProtocolMessage_Opened{
				Opened: &pty.Opened{StreamId: open.Open.GetStreamId()},
			}})
			raw, _ = proto.Marshal(&pb.Envelope{Id: open.Open.GetStreamId(), Payload: payload})
			_ = conn.WriteBinary(raw)
		}
	}))
	defer host.Close()

	s := New(nil, termExec{url: host.URL}, t.TempDir(), t.TempDir())
	mux := http.NewServeMux()
	s.Register(mux)
	front := httptest.NewServer(mux)
	defer front.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.FixtureWindow)
	defer cancel()
	url := "ws" + strings.TrimPrefix(front.URL, "http") +
		"/api/nodes/lab/terminal?cols=80&rows=24&mode=repl"
	conn, err := testutil.RetryConnect(t, "terminal bridge", func() (*ws.Conn, error) { return ws.Dial(ctx, url, nil) })
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	select {
	case open := <-opens:
		if open.GetKind() != "repl" || open.GetName() != "main-repl" || !open.GetSingleton() {
			t.Fatalf("open = %+v, want repl/main-repl/singleton", open)
		}
	case <-time.After(livenessWait):
		t.Fatal("host never saw the open frame")
	}
}

// An audit node is a local one-shot process: it has no host to dial, so the
// bridge refuses before upgrading instead of pretending to open a shell.
func TestNodeTerminalRefusesNodeWithoutHost(t *testing.T) {
	s := New(nil, nodeExec{}, t.TempDir(), t.TempDir())
	mux := http.NewServeMux()
	s.Register(mux)
	front := httptest.NewServer(mux)
	defer front.Close()

	res, err := http.Get(front.URL + "/api/nodes/audit/terminal")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "只有 AOP 宿主能开终端") {
		t.Fatalf("refusal = %d %s", res.StatusCode, body)
	}
}

// Closing the browser socket must detach a REPL (the host keeps the singleton
// conversation for the next page load) and close a shell (no leaked process).
func TestNodeTerminalDisconnectDetachesRepl(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		want string
	}{
		{"repl detaches", "&mode=repl", "detach"},
		{"shell closes", "", "close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan string, 4)
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cyber.rpc.agent.AgentService/ListAgents" {
					_, _ = w.Write([]byte(`{"agents":[{"hello":{"nodeId":"0123456789abcdef0123456789abcdef","name":"local"}}]}`))
					return
				}
				conn, err := ws.Upgrade(w, r)
				if err != nil {
					return
				}
				defer conn.Close()
				for {
					raw, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var env pb.Envelope
					if proto.Unmarshal(raw, &env) != nil || env.Payload == nil {
						continue
					}
					var msg pty.ProtocolMessage
					if env.Payload.UnmarshalTo(&msg) != nil {
						continue
					}
					switch v := msg.Message.(type) {
					case *pty.ProtocolMessage_Open:
						id := v.Open.GetStreamId()
						for _, reply := range []*pty.ProtocolMessage{
							{Message: &pty.ProtocolMessage_Opened{Opened: &pty.Opened{StreamId: id}}},
							{Message: &pty.ProtocolMessage_Output{Output: &pty.Output{StreamId: id, Data: []byte("ready")}}},
						} {
							payload, _ := anypb.New(reply)
							raw, _ := proto.Marshal(&pb.Envelope{Id: id, Payload: payload})
							_ = conn.WriteBinary(raw)
						}
					case *pty.ProtocolMessage_Detach:
						seen <- "detach"
					case *pty.ProtocolMessage_Close:
						seen <- "close"
					}
				}
			}))
			defer host.Close()

			s := New(nil, termExec{url: host.URL}, t.TempDir(), t.TempDir())
			mux := http.NewServeMux()
			s.Register(mux)
			front := httptest.NewServer(mux)
			defer front.Close()

			ctx, cancel := context.WithTimeout(context.Background(), testutil.FixtureWindow)
			defer cancel()
			url := "ws" + strings.TrimPrefix(front.URL, "http") +
				"/api/nodes/lab/terminal?cols=80&rows=24" + tc.mode
			conn, err := testutil.RetryConnect(t, "terminal bridge", func() (*ws.Conn, error) { return ws.Dial(ctx, url, nil) })
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			// Wait for the PTY to be attached before hanging up, otherwise the
			// request is canceled while it is still opening.
			_ = conn.SetReadDeadline(time.Now().Add(testutil.FixtureAttempt))
			if first, err := conn.ReadMessage(); err != nil || string(first) != "ready" {
				t.Fatalf("ready frame = %q err=%v", first, err)
			}
			_ = conn.Close()

			select {
			case got := <-seen:
				if got != tc.want {
					t.Fatalf("host saw %q, want %q", got, tc.want)
				}
			case <-time.After(livenessWait):
				t.Fatalf("host never saw %q", tc.want)
			}
		})
	}
}
