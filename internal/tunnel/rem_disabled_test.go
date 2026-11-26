//go:build !remtunnel

package tunnel

import (
	"errors"
	"testing"
)

// A default build must say plainly that the tunnel is not compiled in, rather
// than serving a /tunnel route that cannot work (docs/15 §3.4).
func TestStartWithoutTagIsExplicit(t *testing.T) {
	ing, err := Start(nil, Config{})
	if !errors.Is(err, ErrNotCompiled) {
		t.Fatalf("Start in a default build = %v, want ErrNotCompiled", err)
	}
	if ing != nil {
		t.Fatal("a default build returned a tunnel surface")
	}
}
