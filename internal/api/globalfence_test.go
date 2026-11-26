package api

import (
	"encoding/json"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// The per-turn fence payload: nil for a bare server and for an empty fence
// (the wire stays byte-identical), the stored shape once the hub supplies the
// read callback.
func TestGlobalFenceJSON(t *testing.T) {
	s := &Server{}
	if got := s.globalFenceJSON(); got != nil {
		t.Fatalf("a bare server sent a fence: %s", got)
	}
	s.GlobalFence = func() settings.Fence { return settings.Fence{} }
	if got := s.globalFenceJSON(); got != nil {
		t.Fatalf("an empty fence was sent: %s", got)
	}
	s.GlobalFence = func() settings.Fence {
		return settings.Fence{
			EnabledDomains: true,
			Domains:        []settings.GlobalDomainRule{{ID: "gf-10", Host: "gov.example"}},
		}
	}
	got := s.globalFenceJSON()
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["enabled_domains"] != true || decoded["enabled_commands"] != nil {
		t.Fatalf("payload = %s", got)
	}
}
