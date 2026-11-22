package bench

import (
	"context"
	"time"
)

// SlotLimit is the platform's documented ceiling on live containers. It is a
// constant here because it is a property of the platform contract, not of a run:
// a run that raises it does not run faster, it just gets refused.
const SlotLimit = 3

// RequeueDelay is how long a challenge waits after `start` answered 409
// invalid_state, before trying to acquire a slot again. The refusal means the
// platform counts three containers somewhere — possibly one this run forgot to
// stop, possibly another task instance — so the answer is to queue behind the
// releases we can see, and to re-list challenges before concluding anything.
const RequeueDelay = 5 * time.Second

// Gate is the local half of the platform's container ceiling: at most limit
// workers hold a container at once, and the rest wait in FIFO order.
//
// It is deliberately a gate and not a retry loop. A `start` refused with 409
// invalid_state is not this gate failing; the caller releases nothing, waits,
// re-reads the challenge list, and requeues at the tail — so the ceiling is
// respected instead of being fought for.
type Gate struct {
	slots chan struct{}
}

// NewGate builds a gate for limit live containers. A non-positive limit falls
// back to SlotLimit rather than to "unbounded": the ceiling is the reason this
// type exists.
func NewGate(limit int) *Gate {
	if limit <= 0 {
		limit = SlotLimit
	}
	return &Gate{slots: make(chan struct{}, limit)}
}

// Acquire blocks until a slot is free or ctx is done. Send order on a channel is
// FIFO, so waiters are admitted in the order they called.
func (g *Gate) Acquire(ctx context.Context) error {
	select {
	case g.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees one slot. Releasing an unheld slot blocks forever, which is a
// programming error the caller must not paper over: a stray Release would let
// the run exceed the ceiling silently.
func (g *Gate) Release() {
	<-g.slots
}

// InUse and Limit report the gate so the run log can prove the ceiling held.
func (g *Gate) InUse() int { return len(g.slots) }
func (g *Gate) Limit() int { return cap(g.slots) }
