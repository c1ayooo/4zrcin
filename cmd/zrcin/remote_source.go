package main

import (
	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/tunnel"
)

// remoteProfileFor adapts a node's enrollment record and its live tunnel
// session to the profile the registry and the router read (docs/14 §7.3). It is
// the one place the two halves meet: the record says how the node was deployed
// and whether it may take work, the tunnel says whether it is reachable now.
//
// Without this source a discovered tunnel node would look like a direct node:
// the console would not group it as remote, and the router would not send a
// turn seed — the node would then have no case files to read.
func remoteProfileFor(records *enroll.Service, ti tunnel.Ingress) func(agentID string) *executor.RemoteNode {
	return func(agentID string) *executor.RemoteNode {
		view, ok := records.NodeView(agentID)
		if !ok {
			return nil
		}
		remote := &executor.RemoteNode{
			Deploy:            view.Deploy,
			Recipe:            view.Recipe,
			State:             view.State,
			DispatchReady:     view.DispatchesOK,
			CLIPath:           view.NodeBinPath,
			Resources:         view.Resources,
			Health:            view.Health,
			LauncherVersion:   view.LauncherVersion,
			UptimeSeconds:     view.UptimeSeconds,
			ScanRestarts:      view.ScanRestarts,
			HubMirrorPort:     view.MirrorHubPort,
			ConsoleMirrorPort: view.MirrorConsolePort,
		}
		if ti != nil {
			if info, up := ti.Session(agentID); up {
				remote.TunnelUp = info.Up
			}
		}
		switch {
		case remote.DispatchReady:
		case view.ProxyPending:
			// Online, but its scan still runs the previous egress: a direction
			// sent now would leave from where the operator just moved it away.
			remote.DispatchBlockedReason = "出网代理变更待生效：节点空闲后自动重启 scan"
		default:
			remote.DispatchBlockedReason = "节点不在线或正在销毁，暂不能承接意图"
		}
		return remote
	}
}
