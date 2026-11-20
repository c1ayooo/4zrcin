package testutil

import (
	"context"
	"log"
	"net"
	"sync"
	"testing"
	"time"
)

// Dial-level retry, for the few tests that cannot avoid a real loopback
// connection (WebSocket upgrade, SSE streaming, TLS handshake, "the port is
// closed" refusals). Those tests fail when the host drops the SYN, not when the
// code under test is wrong.
//
// The exception is deliberately narrow, per docs/01-architecture.md §9:
//   - only connection-establishment failures are retried. A dial error means no
//     connection was established; once a request is written, any error is
//     reported to the test unchanged and never retried.
//   - at most two dial attempts (the first plus one retry). A second failure is
//     a test failure, exactly as before.
//   - every retry is recorded and reported; a rising count means the host is
//     sick, not the test.
const maxDialAttempts = 2

var (
	retryMu  sync.Mutex
	retryLog []retryEvent
)

type retryEvent struct {
	addr string
	err  error
}

// newRetryDialContext wraps base (usually a net.Dialer.DialContext) with the
// narrow retry. A nil base keeps net.Dialer's defaults.
func newRetryDialContext(base func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	dial := base
	if dial == nil {
		dial = dialer.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var last error
		for attempt := 1; attempt <= maxDialAttempts; attempt++ {
			conn, err := dial(ctx, network, addr)
			if err == nil {
				return conn, nil
			}
			last = err
			// A cancelled deadline is the test's own contract, not a host
			// fault: retrying would hide it.
			if ctx.Err() != nil {
				return nil, err
			}
			if attempt < maxDialAttempts {
				noteRetry(addr, err)
			}
		}
		return nil, last
	}
}

// ReportDialRetries logs every dial retry that happened from the moment it is
// called until the test ends, plus the count. Use it in tests that keep a real
// socket so a host problem is visible instead of silent.
func ReportDialRetries(t *testing.T) {
	t.Helper()
	start := len(retrySnapshot())
	t.Cleanup(func() {
		events := retrySnapshot()
		if start > len(events) {
			start = len(events)
		}
		for _, e := range events[start:] {
			t.Logf("dial retry: %s: %v", e.addr, e.err)
		}
		if n := len(events) - start; n > 0 {
			t.Logf("dial retries this test: %d (host loopback was not reliable; connection re-established before any request was sent)", n)
		}
	})
}

func noteRetry(addr string, err error) {
	retryMu.Lock()
	retryLog = append(retryLog, retryEvent{addr: addr, err: err})
	retryMu.Unlock()
	// Visible in the test binary's output even without -v, so a retry is never
	// silent.
	log.Printf("testutil: dial retry %s: %v", addr, err)
}

// RetryCount is the number of dial retries recorded so far in this test binary.
func RetryCount() int {
	retryMu.Lock()
	defer retryMu.Unlock()
	return len(retryLog)
}

// Summary logs the package-wide retry count at the end of a test binary. Call it
// from TestMain after m.Run: a rising count means the host's loopback is sick,
// not that the tests are flaky.
func Summary() {
	if n := RetryCount(); n > 0 {
		log.Printf("testutil: %d dial retries in this package (host loopback was not reliable; each retry happened before any request was sent)", n)
	}
}

func retrySnapshot() []retryEvent {
	retryMu.Lock()
	defer retryMu.Unlock()
	out := make([]retryEvent, len(retryLog))
	copy(out, retryLog)
	return out
}
