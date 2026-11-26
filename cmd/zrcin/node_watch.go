package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/notify"
)

// Tunnel nodes have no health prober (their endpoint is a loopback mirror the
// prober cannot see), so the operator learns about them from heartbeats. This
// watcher compares the node records on a ticker and turns the transitions the
// console renders — 上线 / 离线 / 已销毁 — into the same IM notifications the
// direct nodes already get from the prober's flips (docs/14 §7.3).
//
// A restart primes the snapshot without emitting: the boot of the control
// plane is not a fleet-wide "everyone came online" event.

type nodeSnapshot struct {
	online bool
	state  string
	health string
}

// nodeTransitions compares the previous snapshot with the current records and
// returns the notifications to send, oldest node first. Nodes never seen
// before are recorded silently.
func nodeTransitions(prev map[string]nodeSnapshot, nodes []enroll.NodeView) []notify.Snapshot {
	out := make([]notify.Snapshot, 0, 4)
	for _, n := range nodes {
		p, seen := prev[n.ID]
		prev[n.ID] = nodeSnapshot{online: n.Online, state: n.State, health: n.Health.State}
		if !seen {
			continue
		}
		label := n.Name
		if label == "" {
			label = n.ID
		}
		// Health degradation is deduped per stretch: entering degraded/abnormal
		// notifies once, recovering notifies once, flapping between the two bad
		// states does not re-notify.
		bad := n.Health.State == "degraded" || n.Health.State == "abnormal"
		wasBad := p.health == "degraded" || p.health == "abnormal"
		if n.Online && bad && !wasBad {
			out = append(out, notify.Snapshot{
				Kind: notify.EventNodeDegraded, RefID: n.ID,
				Title:   "节点降级：" + label,
				Summary: strings.Join(n.Health.Reasons, "；"),
			})
		} else if n.Online && !bad && wasBad && p.health != "" {
			out = append(out, notify.Snapshot{
				Kind: notify.EventNodeHealthy, RefID: n.ID,
				Title:   "节点恢复：" + label,
				Summary: "资源与健康状态回到正常。",
			})
		}
		switch {
		case n.State == enroll.NodeDestroyed && p.state != enroll.NodeDestroyed:
			out = append(out, notify.Snapshot{
				Kind: notify.EventNodeDestroyed, RefID: n.ID,
				Title:   "节点已销毁：" + label,
				Summary: "卸载回执已收到或操作员已标记移除；凭据已吊销，项目绑定已解除。",
			})
		case n.Online && !p.online && p.state != enroll.NodeDestroyed:
			summary := ""
			if n.Hostname != "" {
				summary = "主机 " + n.Hostname
			}
			if n.OS != "" || n.Arch != "" {
				summary += "（" + n.OS + "/" + n.Arch + "）"
			}
			out = append(out, notify.Snapshot{
				Kind: notify.EventNodeOnline, RefID: n.ID,
				Title:   "节点上线：" + label,
				Summary: summary,
			})
		case !n.Online && p.online:
			summary := fmt.Sprintf("超过 %s 没有心跳；隧道节点会自行重连，长期离线请检查主机或销毁重建。",
				enroll.OnlineWindow)
			if n.LastError != "" {
				summary += " 最近一次上报：" + n.LastError
			}
			out = append(out, notify.Snapshot{
				Kind: notify.EventNodeOffline, RefID: n.ID,
				Title:   "节点离线：" + label,
				Summary: summary,
			})
		}
	}
	return out
}

// startNodeWatch runs the transition watcher until ctx ends.
func (h *hub) startNodeWatch(ctx context.Context) {
	if h.cfg.RemoteNodes == nil || h.notifier == nil {
		return
	}
	prev := make(map[string]nodeSnapshot)
	for _, n := range h.cfg.RemoteNodes.Nodes() {
		prev[n.ID] = nodeSnapshot{online: n.Online, state: n.State}
	}
	tick := time.NewTicker(15 * time.Second)
	go func() {
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				for _, snap := range nodeTransitions(prev, h.cfg.RemoteNodes.Nodes()) {
					h.notifier.Emit(snap.Kind, snap.RefID, snap.ProjectID, snap.Title, snap.Summary, snap.Severity)
				}
			}
		}
	}()
}
