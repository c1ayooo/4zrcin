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

func observeKindsOf(t *testing.T, args []string) []string {
	t.Helper()
	value := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--observe" {
			value = args[i+1]
		}
	}
	if value == "" {
		t.Fatalf("host is launched without --observe: %v", args)
	}
	return strings.Split(value, ",")
}

// The tools/commands/processes observe kinds only have a consumer when the
// operator configured an OTLP exporter endpoint; without it the scan must keep
// the historic --observe http exactly. Setting the endpoint is the opt-in, so
// every variable of the standard contract has to work, including the logs one.
func TestNodeArgsObserveFollowsOTelEndpointEnv(t *testing.T) {
	keys := []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
	}
	clear := func(t *testing.T) {
		t.Helper()
		for _, key := range keys {
			t.Setenv(key, "")
		}
	}
	h := &Host{addr: "http://127.0.0.1:18766", db: `D:\data\host\cyber-web.db`, token: "tok"}

	clear(t)
	if got := observeKindsOf(t, h.nodeArgs(nil)); len(got) != 1 || got[0] != "http" {
		t.Fatalf("without an OTLP endpoint --observe = %v, want [http]", got)
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			clear(t)
			t.Setenv(key, "http://collector.test:4317")
			kinds := observeKindsOf(t, h.nodeArgs(nil))
			want := map[string]bool{"http": false, "tools": false, "commands": false, "processes": false}
			for _, kind := range kinds {
				if _, ok := want[kind]; ok {
					want[kind] = true
				}
			}
			for kind, found := range want {
				if !found {
					t.Fatalf("--observe = %v misses %q (env %s set)", kinds, kind, key)
				}
			}
		})
	}
}
