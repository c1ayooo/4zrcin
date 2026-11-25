package aopexec

import (
	"context"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/aop"
	"github.com/c1ayooo/4zrcin/internal/executor"
)

// egressProxyFixture is one decoded 4zrcin.EgressProxy extension as the fake
// host read it off the wire, plus whether the request carried it at all. The
// fields mirror the manifest JSON because the manifest JSON is the wire
// contract.
type egressProxyFixture struct {
	Enabled  bool   `json:"enabled"`
	Scheme   string `json:"scheme"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Present  bool   `json:"-"`
	Raw      string `json:"-"`
}

// A configured project rides its proxy on every turn: the fork binds the
// endpoint to the turn's session, so runs of different projects on the same
// node egress independently.
func TestRunCarriesEgressProxyExtension(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{sessions: map[string]bool{}, hostDir: parentDir(work)}
	ex, done := newFake(t, h)
	defer done()

	payload := []byte(`{"enabled":true,"scheme":"socks5","host":"10.0.0.9","port":1080,"username":"ops","password":"secret"}`)
	_, err := ex.Run(context.Background(), executor.Request{
		IntentID: "i1", Prompt: "find the token", WorkDir: work,
		EgressProxy: payload,
	}, func(executor.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.egressProxies) != 1 {
		t.Fatalf("RunTurn extensions carried %d egress-proxy payloads, want 1", len(h.egressProxies))
	}
	got := h.egressProxies[0]
	if !got.Present || !got.Enabled || got.Scheme != "socks5" || got.Host != "10.0.0.9" || got.Port != 1080 {
		t.Fatalf("proxy on the wire = %+v", got)
	}
	if got.Username != "ops" || got.Password != "secret" {
		t.Fatalf("credentials on the wire = %+v", got)
	}
	if !strings.Contains(got.Raw, `"enabled":true`) {
		t.Fatalf("raw payload = %s", got.Raw)
	}
}

// A disabled draft still travels: the fork must learn the switch was turned
// off, or a session that outlives the save would keep egressing through the
// old endpoint.
func TestRunCarriesDisabledEgressProxyDraft(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{sessions: map[string]bool{}, hostDir: parentDir(work)}
	ex, done := newFake(t, h)
	defer done()

	payload := []byte(`{"enabled":false,"scheme":"trojan","host":"relay.example","port":443}`)
	_, err := ex.Run(context.Background(), executor.Request{
		IntentID: "i1", Prompt: "find the token", WorkDir: work,
		EgressProxy: payload,
	}, func(executor.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.egressProxies) != 1 || !h.egressProxies[0].Present || h.egressProxies[0].Enabled {
		t.Fatalf("disabled draft = %+v", h.egressProxies)
	}
	if h.egressProxies[0].Scheme != "trojan" || h.egressProxies[0].Host != "relay.example" {
		t.Fatalf("disabled draft payload = %+v", h.egressProxies[0])
	}
}

// A project that never configured a proxy sends no extension at all: the wire
// must stay byte-identical to the pre-proxy behavior.
func TestRunOmitsEgressProxyWhenUnset(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{sessions: map[string]bool{}, hostDir: parentDir(work)}
	ex, done := newFake(t, h)
	defer done()

	_, err := ex.Run(context.Background(), executor.Request{
		IntentID: "i1", Prompt: "find the token", WorkDir: work,
	}, func(executor.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.egressProxies) != 0 {
		t.Fatalf("unconfigured project sent a proxy: %+v", h.egressProxies)
	}
	// The extension URL is the fork contract the harness's protocol reader
	// matches on; pin it beside the wire test so a refactor cannot drift it.
	if aop.EgressProxyExtensionURL != "type.googleapis.com/4zrcin.EgressProxy" {
		t.Fatalf("extension url = %q", aop.EgressProxyExtensionURL)
	}
}
