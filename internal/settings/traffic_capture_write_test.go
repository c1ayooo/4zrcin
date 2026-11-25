package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The write path round-trips through the read projection: what SetTrafficCapture
// stores is exactly what TrafficCapture reports, and the all-default input
// removes the section instead of storing an explicit relay block.
func TestSetTrafficCaptureRoundTrip(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("CERTIFICATE-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := guardrailStore(t, "")

	// record + decrypt + CA: the full MITM shape, stored under extensions.
	if err := st.SetTrafficCapture(TrafficCaptureInput{CaptureMode: "record", DecryptHTTPS: true, CACertPath: ca}); err != nil {
		t.Fatalf("record write: %v", err)
	}
	view := st.TrafficCapture()
	if !view.Configured || view.CaptureMode != "record" || !view.DecryptHTTPS || view.CACertPath != ca {
		t.Fatalf("view after record write = %+v", view)
	}
	if !strings.HasPrefix(view.CAFingerprint, "sha256:") {
		t.Fatalf("fingerprint = %q", view.CAFingerprint)
	}

	// An unchanged write is reported as no change; flipping the mode is.
	changed, err := st.TrafficCaptureDiffers(TrafficCaptureInput{CaptureMode: "record", DecryptHTTPS: true, CACertPath: ca})
	if err != nil || changed {
		t.Fatalf("same write differs = %v, %v", changed, err)
	}
	changed, err = st.TrafficCaptureDiffers(TrafficCaptureInput{CaptureMode: "relay"})
	if err != nil || !changed {
		t.Fatalf("relay-after-record differs = %v, %v", changed, err)
	}

	// The all-default input deletes the section: the read path returns to the
	// honest "not configured" state instead of an explicit relay block.
	if err := st.SetTrafficCapture(TrafficCaptureInput{}); err != nil {
		t.Fatalf("default write: %v", err)
	}
	view = st.TrafficCapture()
	if view.Configured {
		t.Fatalf("all-default write left a configured section: %+v", view)
	}
	changed, err = st.TrafficCaptureDiffers(TrafficCaptureInput{})
	if err != nil || changed {
		t.Fatalf("default-vs-unconfigured differs = %v, %v", changed, err)
	}

	// An explicit relay section means the same as no section: the default write
	// repairs it into absence, and until then it counts as a change.
	st = guardrailStore(t, "extensions:\n  traffic:\n    capture_mode: relay\n")
	if changed, err := st.TrafficCaptureDiffers(TrafficCaptureInput{}); err != nil || !changed {
		t.Fatalf("explicit-relay section vs default differs = %v, %v", changed, err)
	}
	if err := st.SetTrafficCapture(TrafficCaptureInput{}); err != nil {
		t.Fatalf("default write over explicit relay: %v", err)
	}
	if view := st.TrafficCapture(); view.Configured {
		t.Fatalf("explicit relay survived the default write: %+v", view)
	}
}

// Impossible combinations are rejected before anything is written — the same
// pre-validation contract the retention override follows, so a rejected value
// in a combined settings save cannot leave the model half applied.
func TestSetTrafficCaptureRejectsInvalid(t *testing.T) {
	st := guardrailStore(t, "")
	before, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   TrafficCaptureInput
		want string
	}{
		{"decrypt without record", TrafficCaptureInput{CaptureMode: "relay", DecryptHTTPS: true}, "relay 透传模式无法解密"},
		{"unknown mode", TrafficCaptureInput{CaptureMode: "intercept"}, "只允许 relay/record"},
		{"decrypt without CA", TrafficCaptureInput{CaptureMode: "record", DecryptHTTPS: true}, "必须提供 ca_cert"},
		{"unreadable CA", TrafficCaptureInput{CaptureMode: "record", DecryptHTTPS: true, CACertPath: "/nonexistent/ca.pem"}, "读不到"},
		{"empty CA file", TrafficCaptureInput{CaptureMode: "record", DecryptHTTPS: true, CACertPath: writeEmpty(t)}, "是空的"},
	}
	for _, tc := range cases {
		if err := st.SetTrafficCapture(tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	after, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("rejected writes modified the document")
	}
}

func writeEmpty(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
