// Package connectretry holds the one budget for retrying a connect that timed
// out on this host.
//
// Two places retry a connect: the production AOP client (internal/aop) before a
// round or a bridge is opened, and the test harness (internal/testutil) for the
// few tests that cannot avoid a real loopback socket. They retry the same
// physical failure, so they must share the same numbers: two copies drift, and
// then a test fails on a stall the product already survives (or the other way
// round, which hides a real outage).
//
// Why this budget: measured 2026-11-22 — this host drops the SYN of new
// connections for 1–12s at a time; every new connect failed together (v4
// loopback, v6 loopback, the production hub, a LAN address) while an echo on an
// established socket ran 5,854 round trips with a 1.5ms worst case. A single
// 300ms retry could not outlive that: on 2026-11-21 five commander rounds died
// with zero events on `connectex: did not properly respond`.
//
// Both retry loops apply the same rule on top of these numbers: only a timeout
// is retried (the handshake never completed, so nothing was sent and a retry
// cannot duplicate a request), the wait between attempts is clipped by Window,
// and a cancelled caller stops at once.
package connectretry

import "time"

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
