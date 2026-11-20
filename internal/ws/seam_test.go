package ws

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"
)

// The seam must not change the default: ws:// keeps dialing through a 15s
// net.Dialer unless a test explicitly installs something else. This guards
// against the variable being cleared or its timeout drifting.
func TestWSDialKeepsTheContractDialerByDefault(t *testing.T) {
	if wsNetDial == nil {
		t.Fatal("wsNetDial is nil: ws:// would panic instead of dialing")
	}
	if wsDialer == nil {
		t.Fatal("wsDialer is nil: wss:// would panic instead of dialing")
	}
	if wsDialer.Timeout != 15*time.Second {
		t.Fatalf("wsDialer.Timeout = %s, want 15s", wsDialer.Timeout)
	}
}

// A test that installs a connect uses it for ws://, and restoring brings the
// contract dialer back.
func TestSetNetDialForTestsIsUsedForWSAndRestorable(t *testing.T) {
	before := wsNetDial
	used := false
	restore := SetNetDialForTests(func(ctx context.Context, network, addr string) (net.Conn, error) {
		used = true
		return nil, errors.New("seam")
	})
	_, err := Dial(context.Background(), "ws://seam.invalid/x", nil)
	restore()
	if !used {
		t.Fatal("ws:// dial did not go through the installed connect")
	}
	if err == nil {
		t.Fatal("the installed connect returned an error that Dial swallowed")
	}
	if reflect.ValueOf(wsNetDial).Pointer() != reflect.ValueOf(before).Pointer() {
		t.Fatal("restore did not put the contract dialer back")
	}
}
