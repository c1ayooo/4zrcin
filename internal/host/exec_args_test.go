package host

import (
	"strings"
	"testing"
)

// The console's HTTP traffic page is fed by the harness's observe(http) events.
// The observe extension is only built from the `--observe` command line, and
// that option has no config-file mapping, so dropping the flag here silently
// empties the page while everything else keeps working. Pin the flag: the kind
// has to be named, or a host that starts fine still publishes no flow.
func TestExecArgsEnableHTTPObservation(t *testing.T) {
	h := &Host{addr: "http://127.0.0.1:18766", db: `D:\data\host\cyber-web.db`, token: "tok"}
	args := h.nodeArgs(nil)
	if len(args) < 2 || args[0] != "agent" {
		t.Fatalf("args = %v", args)
	}
	kinds := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--observe" {
			kinds = args[i+1]
		}
	}
	if kinds == "" {
		t.Fatalf("host is launched without --observe; the traffic page would stay empty: %v", args)
	}
	found := false
	for _, kind := range strings.Split(kinds, ",") {
		if strings.TrimSpace(kind) == "http" {
			found = true
		}
	}
	if !found {
		t.Fatalf("--observe = %q does not include http", kinds)
	}
}

func TestExecArgsCarryTheHostIdentity(t *testing.T) {
	h := &Host{addr: "http://127.0.0.1:18766", db: `D:\data\host\cyber-web.db`, token: "tok"}
	args := h.execArgs()
	want := map[string]string{
		"--addr":  "127.0.0.1:18766",
		"--db":    `D:\data\host\cyber-web.db`,
		"--token": "tok",
	}
	for i := 0; i+1 < len(args); i++ {
		if value, ok := want[args[i]]; ok {
			if args[i+1] != value {
				t.Fatalf("%s = %q, want %q", args[i], args[i+1], value)
			}
			delete(want, args[i])
		}
	}
	if len(want) != 0 {
		t.Fatalf("args missing %v: %v", want, args)
	}
}
