package main

import (
	"os"
	"sync"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// scanProxy keeps the scan process's egress on the node default the platform
// hands out (docs/14 §7.6). The proxy cannot ride a config file: a scan node
// takes the Hub's distributed config when it connects, and that carries none
// of the product sections, so a value left in a file is wiped. What survives
// is CYBER_PROXY in the process environment, which the harness applies over
// whatever it was distributed — so that is the delivery, here and for the
// local host alike.
//
// A change restarts scan, and a restart ends every session the node runs. The
// platform says with each answer whether the node is busy, and the restart
// waits for an idle one: a direction in flight is never interrupted for a
// proxy. Until scan has restarted, the platform is told the previous default
// still applies, and it holds the node's dispatch in the meantime.
type scanProxy struct {
	mu sync.Mutex
	// started is set once scan has been started for the first time; applied
	// is the platform default the running scan was started under, desired the
	// latest one the platform handed out.
	started bool
	applied string
	desired string
	// restart carries at most one pending restart to the supervisor.
	restart chan struct{}
}

func newScanProxy() *scanProxy {
	return &scanProxy{restart: make(chan struct{}, 1)}
}

// inheritedProxy is the CYBER_PROXY of the launcher's own environment. The
// harness reads it over anything else, so it is the operator's explicit
// choice for this host and the platform default is not restated over it.
func inheritedProxy() (string, bool) {
	value, ok := os.LookupEnv("CYBER_PROXY")
	return value, ok && value != ""
}

// offer records the platform's latest default and whether the node is busy,
// and reports whether scan must be restarted for it now. Before the first
// start the default is simply what scan will start with.
func (p *scanProxy) offer(desired string, busy bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.desired = desired
	if _, inherited := inheritedProxy(); inherited {
		// Nothing of this node's egress is the platform's to move.
		p.applied = desired
		return false
	}
	if !p.started || p.applied == desired || busy {
		return false
	}
	select {
	case p.restart <- struct{}{}:
	default:
	}
	return true
}

// launched records that scan was just (re)started for the current default.
func (p *scanProxy) launched() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started, p.applied = true, p.desired
}

// env is what scan starts with on top of the launcher's environment: the
// default as CYBER_PROXY, or nothing (direct, or an inherited value wins).
func (p *scanProxy) env() []string {
	p.mu.Lock()
	desired := p.desired
	p.mu.Unlock()
	if _, inherited := inheritedProxy(); inherited || desired == "" {
		return nil
	}
	return []string{"CYBER_PROXY=" + desired}
}

// due is the channel a running scan watches for its restart.
func (p *scanProxy) due() <-chan struct{} { return p.restart }

// takeRestart drops a restart that is no longer pending: a change that
// arrived between two starts is already what the next start uses.
func (p *scanProxy) takeRestart() {
	select {
	case <-p.restart:
	default:
	}
}

// digest is what the heartbeat reports: the fingerprint of the default scan
// runs under, so the platform knows whether the node still needs a restart.
func (p *scanProxy) digest() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return enroll.ScanProxyDigest(p.applied)
}
