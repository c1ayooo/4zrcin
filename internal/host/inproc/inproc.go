//go:build inprochost

// Package inproc runs the harness Hub inside the zrcin process (docs/04 M1,
// patch 22). Only builds with the inprochost tag carry it: the tag excludes
// the cyber module from the default build graph, so plain go builds and test
// binaries never need the fork checkout, and an operator who wants the old
// exec form builds without the tag (or passes --harness, which overrides the
// default at runtime).
//
// The scan node stays a subprocess in both forms: it owns the heavy engine
// dependency tree, and CYBER_EXCLUDE_TOOLS (a scan-side contract) reaches it
// through the child environment, exactly as before. Nothing about the node
// registry changes — the embedded Hub serves the same AOP/management surface
// on the same loopback endpoint the "local" spec already points at.
package inproc

import (
	"context"
	"io"
	"net"

	webhost "github.com/c1ayooo/cyber/pkg/web/host"
	hubapp "github.com/c1ayooo/cyber/pkg/web/hubapp"
)

// Enabled reports whether this build embeds the Hub. main.go consults it for
// the default launch mode; a false value makes every --harness path exec.
const Enabled = true

// Run assembles the Hub from args — the same list a child cyber-harness would
// receive (--config, model flags, --db, --token, --addr) — and serves it on
// listener until ctx is cancelled. The embedded listener overrides the parsed
// --addr (patch 22), so both forms share one flag contract while the control
// plane keeps ownership of the socket. Harness log lines are routed through
// logf instead of a child's stderr pipe.
func Run(ctx context.Context, listener net.Listener, args []string, logf func(format string, args ...any)) error {
	return hubapp.App(ctx, args, io.Discard, writerFunc(logf), func(c *webhost.Config) {
		c.Listener = listener
	})
}

// writerFunc turns the host's line logger into the io.Writer the Hub's
// telemetry stream expects.
type writerFunc func(format string, args ...any)

func (f writerFunc) Write(p []byte) (int, error) {
	f("%s", string(p))
	return len(p), nil
}
