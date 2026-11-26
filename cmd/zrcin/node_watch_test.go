package main

import (
	"testing"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/notify"
)

// A tunnel node's IM story is the watcher's transitions: 上线 when a heartbeat
// lands after an offline stretch, 离线 when the online window lapses, 已销毁
// on the receipt. Nodes unseen before a restart prime silently (docs/14 §7.3).
func TestNodeTransitions(t *testing.T) {
	node := func(online bool, state string) enroll.NodeView {
		return enroll.NodeView{ID: "n1", Name: "edge-01", Hostname: "host-1", OS: "linux", Arch: "amd64", State: state, Online: online}
	}
	prev := map[string]nodeSnapshot{"n1": {online: true, state: enroll.NodeOnline}}

	// No change: no notifications.
	if got := nodeTransitions(prev, []enroll.NodeView{node(true, enroll.NodeOnline)}); len(got) != 0 {
		t.Fatalf("steady state emitted %d notifications", len(got))
	}

	// Online → offline.
	got := nodeTransitions(prev, []enroll.NodeView{node(false, enroll.NodeOnline)})
	if len(got) != 1 || got[0].Kind != notify.EventNodeOffline {
		t.Fatalf("offline transition = %+v", got)
	}

	// Offline → online (the flap the operator cares about).
	got = nodeTransitions(prev, []enroll.NodeView{node(true, enroll.NodeOnline)})
	if len(got) != 1 || got[0].Kind != notify.EventNodeOnline {
		t.Fatalf("online transition = %+v", got)
	}
	if got[0].Title != "节点上线：edge-01" || got[0].Summary == "" {
		t.Fatalf("online notification = %+v", got[0])
	}

	// Online → destroyed (receipt lands while the node is gone).
	got = nodeTransitions(prev, []enroll.NodeView{node(false, enroll.NodeDestroyed)})
	if len(got) != 1 || got[0].Kind != notify.EventNodeDestroyed {
		t.Fatalf("destroyed transition = %+v", got)
	}

	// A destroyed node stays quiet.
	if got := nodeTransitions(prev, []enroll.NodeView{node(false, enroll.NodeDestroyed)}); len(got) != 0 {
		t.Fatalf("destroyed node emitted again: %+v", got)
	}

	// A brand-new node primes silently: the watcher's first sight is not an event.
	fresh := map[string]nodeSnapshot{}
	if got := nodeTransitions(fresh, []enroll.NodeView{node(true, enroll.NodeOnline)}); len(got) != 0 {
		t.Fatalf("first sight emitted %d notifications", len(got))
	}
}
