package testutil

import (
	"errors"
	"net"
	"testing"
)

// RetryConnect runs fn at most twice, retrying only when it failed at the TCP
// connect itself. That is the one case docs/01-architecture.md §9 allows: the
// host occasionally drops the SYN, so connect() fails after ~1s even though the
// listener is healthy, and no request was ever sent.
//
// Anything past the connect — a refused handshake, a 401, a broken stream — is
// returned to the caller unchanged on the first attempt: retrying that would
// hide a real failure.
//
// The retry is recorded in the same counter as the transport-level retry, so a
// package whose TestMain calls Summary() prints one count line at the end: a
// retry never stays silent even when the test passes.
func RetryConnect[T any](t *testing.T, what string, fn func() (T, error)) (T, error) {
	t.Helper()
	return RetryConnectWhen(t, what, fn, isDialFailure)
}

// RetryConnectWhen is RetryConnect with the caller supplying the test for "the
// connection was never established".
//
// It exists for the case where the failing dial sits behind an interface that
// reports the failure as a value instead of an error: the terminal bridge, for
// example, sends the host's connection error as its greeting frame, so by the
// time the test sees it there is no *net.OpError left to inspect. The caller
// turns that value back into an error and says how to recognise it; the retry
// stays at two attempts and is recorded exactly like every other retry, so it
// can never be silent. A non-connect failure — a refusal, a wrong greeting, a
// protocol error — is returned unchanged on the first attempt.
func RetryConnectWhen[T any](t *testing.T, what string, fn func() (T, error), dialFailed func(error) bool) (T, error) {
	t.Helper()
	first, err := fn()
	if err == nil || !dialFailed(err) {
		return first, err
	}
	noteRetry(what, err)
	var zero T
	retried, err := fn()
	if err != nil {
		t.Logf("dial retry failed for %s: %v (reported as-is)", what, err)
		return zero, err
	}
	return retried, nil
}

// isDialFailure reports whether err came from establishing the TCP connection,
// as opposed to anything that happened after it existed.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}
	return opErr.Op == "dial"
}
