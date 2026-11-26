//go:build !remtunnel

package tunnel

import "github.com/c1ayooo/4zrcin/internal/enroll"

// Start refuses in default builds: the public node ingress is only usable in a
// binary that carries the tunnel (docs/15 §3.4). main turns this into a
// startup error naming the build tag, so an operator never gets a live
// /tunnel route that cannot do anything.
func Start(_ *enroll.Service, _ Config) (Ingress, error) {
	return nil, ErrNotCompiled
}
