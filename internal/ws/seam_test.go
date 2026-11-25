package ws

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// SetNetDialForTests is a test seam, not a transport switch. Nothing outside a
// _test.go file may call it: the day production code reaches for it, the seam
// has become a public extension point and the "default is untouched" guarantee
// is gone. This walks the whole repo so the misuse shows up wherever it starts.
func TestSetNetDialForTestsIsNeverCalledFromProductCode(t *testing.T) {
	root := filepath.Join("..", "..")
	skip := func(path string, d fs.DirEntry) bool {
		if !d.IsDir() {
			return false
		}
		switch d.Name() {
		case ".git", "node_modules", "vendor", "testdata":
			return true
		}
		return strings.Contains(filepath.ToSlash(path), "web/dist") || strings.Contains(filepath.ToSlash(path), "internal/web/bundle")
	}
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip(path, d) {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, "SetNetDialForTests") {
				continue
			}
			if strings.HasPrefix(trimmed, "//") {
				continue // a comment may name it; only a call is a misuse
			}
			if strings.Contains(trimmed, "func SetNetDialForTests") {
				continue // the declaration itself is fine; a call is not
			}
			offenders = append(offenders, filepath.ToSlash(path)+": "+trimmed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("SetNetDialForTests is called from non-test code: %v", offenders)
	}
}
