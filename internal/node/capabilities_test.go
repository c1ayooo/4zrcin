package node

import (
	"testing"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// The recipe declaration rides every NodeInfo the executor routes on
// (docs/04 §3.1): a capture-only spec must stay visible to the dispatch gate
// even before the node has ever answered a probe.
func TestInfosCarriesDeclaredCapabilities(t *testing.T) {
	r, err := New([]Spec{
		{ID: "local", AOP: "http://127.0.0.1:18766"},
		{ID: "fence", AOP: "http://10.0.0.9:18766", Capabilities: []string{"scan-capture"}},
	}, "local")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]executor.NodeInfo{}
	for _, info := range r.Infos() {
		byID[info.ID] = info
	}
	if len(byID["fence"].Capabilities) != 1 || byID["fence"].Capabilities[0] != "scan-capture" {
		t.Fatalf("fence capabilities = %v", byID["fence"].Capabilities)
	}
	if len(byID["local"].Capabilities) != 0 {
		t.Fatalf("local capabilities = %v, want empty (full)", byID["local"].Capabilities)
	}
}
