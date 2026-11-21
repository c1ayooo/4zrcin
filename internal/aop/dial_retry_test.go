package aop

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	pb "github.com/kksty/tianwu/internal/aop/pb"
	"github.com/kksty/tianwu/internal/ws"
	"google.golang.org/protobuf/types/known/anypb"
)

// Connecting gets exactly one retry. A dropped SYN on loopback used to kill a
// whole round with zero events while the same port answered a second later
// (2026-11-21: two commander rounds and several verifier rounds). These tests pin
// the three boundaries: a transient failure is recovered with one retry, a dead
// port still fails with both causes recorded, and the first success is not
// retried at all.
func TestDialRetriesOnceAfterARefusedConnect(t *testing.T) {
	hub := startTestHub(t, func(*testHub, *ws.Conn, *pb.Envelope, *anypb.Any) {})
	defer hub.server.Close()

	var attempts int32
	restore := setDialWSForTest(func(ctx context.Context, url string, header http.Header) (*ws.Conn, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			return nil, errors.New("dial tcp 127.0.0.1:18799: connectex: 由于目标计算机积极拒绝，无法连接")
		}
		return ws.Dial(ctx, url, header)
	})
	defer restore()

	c := New(hub.server.URL, "testkey")
	conn, err := c.Dial(context.Background())
	if err != nil {
		t.Fatalf("a refused first connect was not recovered: %v", err)
	}
	defer conn.Close()
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("dial attempts = %d, want 2 (one retry)", got)
	}
}

func TestDialReportsBothCausesWhenEveryAttemptFails(t *testing.T) {
	var attempts int32
	restore := setDialWSForTest(func(context.Context, string, http.Header) (*ws.Conn, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return nil, errors.New("第一次：connectex 拒绝")
		}
		return nil, errors.New("第二次：connectex 排队超时")
	})
	defer restore()

	c := New("http://127.0.0.1:18799", "")
	if _, err := c.Dial(context.Background()); err == nil {
		t.Fatal("a dead port was reported as connected")
	} else {
		// The exit_reason has to show a retry happened and why each attempt
		// failed; collapsing to one cause loses that.
		for _, want := range []string{"已重试 1 次", "第一次：connectex 拒绝", "第二次：connectex 排队超时"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q lost %q", err.Error(), want)
			}
		}
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("dial attempts = %d, want 2", got)
	}
}

func TestDialDoesNotRetryASuccessfulConnect(t *testing.T) {
	hub := startTestHub(t, func(*testHub, *ws.Conn, *pb.Envelope, *anypb.Any) {})
	defer hub.server.Close()

	var attempts int32
	restore := setDialWSForTest(func(ctx context.Context, url string, header http.Header) (*ws.Conn, error) {
		atomic.AddInt32(&attempts, 1)
		return ws.Dial(ctx, url, header)
	})
	defer restore()

	c := New(hub.server.URL, "testkey")
	conn, err := c.Dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("dial attempts = %d, want 1 (no retry on success)", got)
	}
}

// setDialWSForTest swaps the package's handshake dial and returns a restore.
func setDialWSForTest(fn func(context.Context, string, http.Header) (*ws.Conn, error)) func() {
	prev := dialWS
	dialWS = fn
	return func() { dialWS = prev }
}
