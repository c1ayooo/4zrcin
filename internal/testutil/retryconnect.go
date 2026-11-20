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
func RetryConnect[T any](t *testing.T, what string, fn func() (T, error)) (T, error) {
	t.Helper()
	first, err := fn()
	if err == nil || !isDialFailure(err) {
		return first, err
	}
	t.Logf("dial retry: %s: %v", what, err)
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
