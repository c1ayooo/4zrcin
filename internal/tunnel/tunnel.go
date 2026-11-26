// Package tunnel is the platform side of the remote-node tunnel (docs/15 §3.4).
//
// A node dials the platform's public console port with a one-time ticket and
// speaks the rem protocol through the reverse proxy; the platform runs a rem
// server on loopback whose only permitted job is to forward that node's two
// required loopback addresses (the Hub and the console API). The rem server
// itself is compiled in only under the `remtunnel` build tag, the same way the
// embedded Hub needs `inprochost`: the default build graph must stay free of
// the ../rem checkout.
package tunnel

import (
	"errors"
	"net/http"
	"time"
)

// ErrNotCompiled says the binary was built without the tunnel: an explicit
// message is better than a dead public route.
var ErrNotCompiled = errors.New("隧道入口未编入本次构建：用 -tags remtunnel 重新构建 zrcin")

// TicketTTL is how long one ticket admits a handshake (docs/15 §3.4).
const TicketTTL = 60 * time.Second

// Config is what the tunnel needs from the instance. Both addresses are the
// platform's own loopback view; the node mirrors them, which is why the
// control plane's absolute URLs work unchanged on the node.
type Config struct {
	// PublicURL is the console's public base URL, used to render the ws URL in
	// the ticket response.
	PublicURL string
	// ListenPort is the loopback port of the rem server; 0 picks a free port.
	ListenPort int
	// Key is the rem login key (the instance tunnel key, hex encoded).
	Key string
	// HubAddr and ConsoleAddr are the only two loopback destinations a node
	// may make the server dial.
	HubAddr     string
	ConsoleAddr string
	// Now is the clock seam for tests.
	Now func() time.Time
}

// SessionInfo is one node's live tunnel as the platform observes it.
type SessionInfo struct {
	Up       bool
	BytesIn  int64
	BytesOut int64
	Since    time.Time
}

// Ingress is the tunnel surface handed to the hub: the ticket endpoint the
// launcher calls, the websocket endpoint the node dials, and the session
// table the console reads.
type Ingress interface {
	// TicketHandler serves POST /node/ticket (node credential required).
	TicketHandler() http.Handler
	// TunnelHandler serves GET /tunnel/{ticket} (one-time ticket required).
	TunnelHandler() http.Handler
	// Session reports the live tunnel of a node, if any.
	Session(nodeID string) (SessionInfo, bool)
	// Close tears one node's tunnel down (destroy, docs/15 §5).
	Close(nodeID string) error
	// Port is the loopback port the rem server listens on.
	Port() int
}
