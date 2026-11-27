package settings

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The capture switch is cyberhub.mitm: unset means record (the harness
// default), an explicit false means relay. The write path round-trips through
// the read projection and shares the cyberhub section with the egress proxy
// without touching its keys.
func TestTrafficCaptureSwitchRoundTrip(t *testing.T) {
	st := guardrailStore(t, "")

	if view := st.TrafficCapture(); view.CaptureMode != "record" {
		t.Fatalf("unset switch = %+v", view)
	}
	if changed, err := st.TrafficCaptureDiffers(TrafficCaptureInput{CaptureMode: "record"}); err != nil || changed {
		t.Fatalf("record-vs-default differs = %v, %v", changed, err)
	}
	if changed, err := st.TrafficCaptureDiffers(TrafficCaptureInput{CaptureMode: "relay"}); err != nil || !changed {
		t.Fatalf("relay-vs-default differs = %v, %v", changed, err)
	}

	if err := st.SetTrafficCapture(TrafficCaptureInput{CaptureMode: "relay"}); err != nil {
		t.Fatalf("relay write: %v", err)
	}
	if view := st.TrafficCapture(); view.CaptureMode != "relay" {
		t.Fatalf("view after relay write = %+v", view)
	}
	// The relay choice is an explicit false: the default is record, so it must
	// survive in the file.
	data, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "mitm: false") {
		t.Fatalf("relay write left no explicit mitm=false: %s", data)
	}

	// The proxy writer shares the section without losing the switch, and the
	// capture writer does not lose the proxy.
	if err := st.SetNodeProxy(NodeProxyInput{Proxy: "socks5://127.0.0.1:1080"}); err != nil {
		t.Fatalf("proxy write: %v", err)
	}
	if view := st.TrafficCapture(); view.CaptureMode != "relay" {
		t.Fatalf("proxy write disturbed the capture switch: %+v", view)
	}
	if view := st.NodeProxy(); view.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("capture write disturbed the proxy: %+v", view)
	}

	// Back to record: the explicit false is removed (the default is record).
	if err := st.SetTrafficCapture(TrafficCaptureInput{CaptureMode: "record"}); err != nil {
		t.Fatalf("record write: %v", err)
	}
	data, err = os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mitm") {
		t.Fatalf("record write left an explicit mitm key: %s", data)
	}
	if view := st.NodeProxy(); view.Proxy == "" {
		t.Fatal("record write removed the section despite the proxy key")
	}
}

// A legacy nested extensions.traffic block has no engine reader: the read
// path reports it as legacy instead of honoring it, and a legacy nested
// cyberhub block still reads.
func TestTrafficCaptureLegacyBlocks(t *testing.T) {
	st := guardrailStore(t, "extensions:\n  traffic:\n    capture_mode: record\n    decrypt_https: true\n")
	view := st.TrafficCapture()
	if view.CaptureMode != "record" || view.Reason == "" || !strings.Contains(view.Reason, "已废弃") {
		t.Fatalf("legacy capture block = %+v", view)
	}

	st = guardrailStore(t, "extensions:\n  cyberhub:\n    mitm: false\n    proxy: socks5://127.0.0.1:1080\n")
	if view := st.TrafficCapture(); view.CaptureMode != "relay" {
		t.Fatalf("legacy cyberhub view = %+v", view)
	}
	// Any capture write migrates the block: the nested layout would be
	// rejected by every harness binary.
	if err := st.SetTrafficCapture(TrafficCaptureInput{CaptureMode: "record"}); err != nil {
		t.Fatalf("migrating write: %v", err)
	}
	data, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "extensions:") {
		t.Fatalf("the write left the legacy nested layout in place: %s", data)
	}
	if view := st.NodeProxy(); view.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("migration lost the proxy: %+v", view)
	}
}

// Validation rejects anything outside the closed set before anything persists.
func TestTrafficCaptureValidatesMode(t *testing.T) {
	st := guardrailStore(t, "")
	if err := st.ValidateTrafficCapture(TrafficCaptureInput{CaptureMode: "intercept"}); err == nil {
		t.Fatal("an unknown mode must be rejected")
	}
	if err := st.SetTrafficCapture(TrafficCaptureInput{CaptureMode: "intercept"}); err == nil {
		t.Fatal("an unknown mode must not be written")
	}
}

// The settings view carries the capture projection without leaking anything.
func TestTrafficCaptureInsideSettingsView(t *testing.T) {
	st := guardrailStore(t, "cyberhub:\n  mitm: false\n")
	v := st.View()
	body, _ := json.Marshal(v)
	if !strings.Contains(string(body), `"traffic_capture"`) {
		t.Fatalf("settings view lacks traffic_capture: %.200s", body)
	}
	if !strings.Contains(string(body), `"capture_mode":"relay"`) {
		t.Fatalf("settings view lost the mode: %.300s", body)
	}
}
