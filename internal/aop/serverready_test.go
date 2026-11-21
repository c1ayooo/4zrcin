package aop

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// waitTestServerAnswers blocks until the test server answers one plain HTTP
// request, then returns.
//
// httptest.NewServer returns as soon as the listener is bound, but a bound
// listener only means the kernel accepts: the connection sits in the backlog
// until the handler goroutine is scheduled. On a busy machine (this repo's gate
// runs next to a live project) that scheduling can be delayed far longer than a
// test's whole operation budget. The 2026-11-22 gate failure was exactly that:
// "read tcp ...: i/o timeout" at the websocket handshake, after the TCP connect
// had already succeeded — the test's single 10s context had been spent waiting
// for a handler that was merely slow to run, not dead.
//
// Each attempt carries its own short context, so one starved attempt cannot
// consume the wait; readyAfter bounds the whole wait, so a server that never
// answers still fails the test instead of hanging. Only an HTTP response counts:
// a TCP connect probe would pass against the kernel backlog and prove nothing
// about the handler loop.
func waitTestServerAnswers(t *testing.T, url string) {
	t.Helper()
	const (
		attemptTimeout = 250 * time.Millisecond
		readyAfter     = 20 * time.Second
	)
	deadline := time.Now().Add(readyAfter)
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			t.Fatalf("readiness request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("test server %s did not answer within %v (%d attempts): %v", url, readyAfter, attempt, err)
		}
		// An immediate refusal would otherwise spin this loop without bound.
		time.Sleep(20 * time.Millisecond)
	}
}

// dialTestConn dials the test server, retrying only when an attempt died on a
// wire timeout.
//
// Readiness is not a one-time event: a machine under load can answer one HTTP
// request and then starve the very next accept/upgrade for longer than a single
// attempt's budget (observed 2026-11-22: readiness passed after 1s, the
// handshake then timed out at 10s). Each attempt therefore gets a fresh context
// of its own, and one timeout does not condemn the server.
//
// What is retried stays narrow: isTransportTimeout is the same predicate the
// client uses to tell "the reply may have been lost" from "the host answered".
// A refused handshake, a 401 or a bad frame is returned on the first attempt —
// retrying those would turn a real protocol failure into a slow test. Each
// retry is logged, and a failure carries the attempt count, so a retry never
// stays silent.
func dialTestConn(t *testing.T, c *Client) *Conn {
	t.Helper()
	deadline := time.Now().Add(testBudget)
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), wireAttemptBudget)
		conn, err := c.Dial(ctx)
		cancel()
		if err == nil {
			return conn
		}
		if !isTransportTimeout(err) || time.Now().After(deadline) {
			t.Fatalf("dial attempt %d failed: %v", attempt, err)
		}
		t.Logf("dial attempt %d timed out on the wire, retrying: %v", attempt, err)
	}
}

// openTerminalAttempt opens a PTY stream against a test host, retrying only a
// wire timeout, with a fresh context per attempt.
//
// OpenTerminal dials and then waits for the host's open acknowledgement inside
// the same context, so it has the same exposure as Dial: a starved host can
// consume an attempt's whole budget. The error is returned rather than raised so
// a test can assert on a refusal (a closed stream, a bad node) — those are
// protocol answers, not timeouts, and are returned on the first attempt.
func openTerminalAttempt(t *testing.T, url, token, nodeID string, cols, rows int) (*Terminal, error) {
	t.Helper()
	deadline := time.Now().Add(testBudget)
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), wireAttemptBudget)
		term, err := OpenTerminal(ctx, url, token, nodeID, cols, rows)
		cancel()
		if err == nil {
			return term, nil
		}
		if !isTransportTimeout(err) || time.Now().After(deadline) {
			return nil, err
		}
		t.Logf("terminal open attempt %d timed out on the wire, retrying: %v", attempt, err)
	}
}

// openTerminalReady is openTerminalAttempt for tests that require success.
func openTerminalReady(t *testing.T, url, token, nodeID string, cols, rows int) *Terminal {
	t.Helper()
	term, err := openTerminalAttempt(t, url, token, nodeID, cols, rows)
	if err != nil {
		t.Fatalf("terminal open: %v", err)
	}
	return term
}

// testBudget is how long a protocol test may take before it is treated as
// hung. It is a liveness guard, not a latency assertion: the assertions in
// these tests are about framing and correlation, and the gate shares this
// machine with a live project, where a healthy handler can be delayed by
// seconds. Keeping it tight only relabels machine load as a protocol failure.
const testBudget = 60 * time.Second

// wireAttemptBudget bounds one connect or open attempt. Every attempt starts a
// fresh context, so a delay in one does not consume the next one's budget.
const wireAttemptBudget = 10 * time.Second
