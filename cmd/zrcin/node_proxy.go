package main

import (
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/settings"
)

// savedNodeProxy is the node default egress proxy the operator saved
// (extensions.cyberhub.proxy), or "" for direct. Unlike nodeProxyValue it does
// not yield to this process's own CYBER_PROXY: that variable is the local
// host's override and says nothing about what a tunnel node should do.
func savedNodeProxy(st *settings.Store) string {
	if st == nil {
		return ""
	}
	view := st.NodeProxy()
	if !view.Configured {
		return ""
	}
	return view.Proxy
}

// nodeBusy reports whether a run is executing on a node right now, in any
// project. A tunnel launcher reads it before it restarts scan for a proxy
// change (docs/14 §7.6): busy means "a restart here would end a direction".
func (h *hub) nodeBusy(nodeID string) bool {
	h.mu.RLock()
	kits := make([]*kit, 0, len(h.kits))
	for _, k := range h.kits {
		kits = append(kits, k)
	}
	h.mu.RUnlock()
	for _, k := range kits {
		if k.srv == nil {
			continue
		}
		for _, r := range k.srv.Store.Runs() {
			if r.Status == model.RunRunning && r.Node == nodeID {
				return true
			}
		}
	}
	return false
}

// startNodeProxy hands the tunnel nodes the node default egress proxy and the
// busy answer. It runs at boot, so nodes are evaluated against the saved
// value as their records load, and again after every proxy save, so a change
// reaches the launchers now rather than at their next heartbeat.
func (h *hub) startNodeProxy() {
	if h.cfg.RemoteNodes == nil {
		return
	}
	h.cfg.RemoteNodes.SetScanProxy(func() string { return savedNodeProxy(h.settings) })
	h.cfg.RemoteNodes.SetNodeBusy(h.nodeBusy)
}
