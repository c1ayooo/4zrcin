package connectretry

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

// Delays is the backoff between connect attempts: 0.3s, 1s, 2s, 4s. The sum
// (7.3s) stays under Window so the delays are the schedule a reader sees, not
// an approximation of it.
var Delays = []time.Duration{
	300 * time.Millisecond,
	time.Second,
	2 * time.Second,
	4 * time.Second,
}

// Window caps the whole connect phase — every wait and every attempt together —
// so a host that is really gone is reported in seconds rather than after the
// sum of every backoff token.
const Window = 8 * time.Second

// wsaeTimedout is WSAETIMEDOUT: how Windows reports a connect whose SYN was
// dropped. A literal so the package still builds on Linux and macOS, where the
// value is never produced.
const wsaeTimedout = syscall.Errno(10060)

// IsTimeout reports whether err is a timeout on the wire.
//
// The type checks are the normal case. The errno check is not optional on
// Windows: Errno.Timeout() there accepts only EAGAIN/EWOULDBLOCK/ETIMEDOUT
// (ETIMEDOUT = 536,871,033 on that platform), so a genuine WSAETIMEDOUT
// (10060) answers Timeout()==false and a plain net.Error test misses it.
// Measured 2026-11-22 in a full run: the connect was stalled and the log said
// "不是连接超时", which skipped the retry that exists for exactly that failure.
//
// A refusal is not a timeout and never matches: no wait makes a listener
// appear.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == wsaeTimedout || errors.Is(errno, syscall.ETIMEDOUT)
	}
	return false
}

// Label renders the schedule for an error or a log line, e.g.
// "300ms/1s/2s/4s，整段上限 8s".
func Label() string {
	out := ""
	for i, d := range Delays {
		if i > 0 {
			out += "/"
		}
		out += d.String()
	}
	return out + "，整段上限 " + Window.String()
}

// WaitForNext clips the schedule's nth backoff (1-based) to what is left of the
// window. It returns 0 when the window is spent, and callers stop there.
func WaitForNext(attempt int, deadline time.Time) time.Duration {
	remaining := time.Until(deadline)
	if remaining <= 0 || attempt < 1 || attempt > len(Delays) {
		return 0
	}
	if d := Delays[attempt-1]; d < remaining {
		return d
	}
	return remaining
}
