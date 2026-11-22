package bench

import "time"

// Snapshot is one answer of GET /challenges, with the platform's authority made
// explicit. Every fact the run acts on — progress, whether an environment is
// up, which addresses are legal targets — is read from the newest snapshot.
// Nothing is carried across snapshots: after a close, the addresses the previous
// snapshot listed may already belong to another challenge (best practice §三.2),
// so a cached address is not stale data, it is somebody else's target.
type Snapshot struct {
	At         time.Time
	Challenges []Challenge
}

// NewSnapshot binds one poll's answers to the moment they were read.
func NewSnapshot(at time.Time, list []Challenge) Snapshot {
	return Snapshot{At: at, Challenges: list}
}

// Open is the remaining work: challenges the platform has not marked complete.
func (s Snapshot) Open() []Challenge {
	var out []Challenge
	for _, ch := range s.Challenges {
		if !ch.IsCompleted {
			out = append(out, ch)
		}
	}
	return out
}

// Active is the challenges whose environments are ready right now. Only these
// may be used as targets: any other status means the platform is still starting,
// stopping, or has stopped the container, and its address list is empty by
// contract.
func (s Snapshot) Active() []Challenge {
	var out []Challenge
	for _, ch := range s.Challenges {
		if ch.ContainerStatus == ContainerAvailable {
			out = append(out, ch)
		}
	}
	return out
}

// AllowedAddrs is the complete set of addresses this run may touch, taken from
// the platform and nowhere else. An empty result means "no target is legal right
// now" — never "unrestricted", which is the one reading that would turn a
// scope check into a rubber stamp.
func (s Snapshot) AllowedAddrs() []string {
	seen := make(map[string]bool)
	var out []string
	for _, ch := range s.Active() {
		for _, a := range ch.ContainerAddr {
			if a == "" || seen[a] {
				continue
			}
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// ByCode finds one challenge by its unique code.
func (s Snapshot) ByCode(code string) (Challenge, bool) {
	for _, ch := range s.Challenges {
		if ch.UniqueCode == code {
			return ch, true
		}
	}
	return Challenge{}, false
}

// Completed counts finished challenges, for the run log only.
func (s Snapshot) Completed() int {
	n := 0
	for _, ch := range s.Challenges {
		if ch.IsCompleted {
			n++
		}
	}
	return n
}
