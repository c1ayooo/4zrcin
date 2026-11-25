package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrafficCaptureAbsentMeansRelay(t *testing.T) {
	view := guardrailStore(t, "").TrafficCapture()
	if view.Configured || view.CaptureMode != "relay" || view.DecryptHTTPS {
		t.Fatalf("view = %+v", view)
	}
	if !strings.Contains(view.Reason, "未配置") {
		t.Fatalf("reason = %q", view.Reason)
	}
}

func TestTrafficCaptureParsesRecordConfiguration(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("CERTIFICATE-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	view := guardrailStore(t, "extensions:\n  traffic:\n    capture_mode: record\n    decrypt_https: true\n    ca_cert: "+ca+"\n").TrafficCapture()
	if !view.Configured || view.CaptureMode != "record" || !view.DecryptHTTPS {
		t.Fatalf("view = %+v", view)
	}
	if view.CACertPath != ca || !strings.HasPrefix(view.CAFingerprint, "sha256:") {
		t.Fatalf("cert projection = %q / %q", view.CACertPath, view.CAFingerprint)
	}
	if strings.Contains(view.CAFingerprint, "CERTIFICATE-BYTES") {
		t.Fatalf("fingerprint leaked file content: %q", view.CAFingerprint)
	}
}

func TestTrafficCaptureRejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"unknown key", "extensions:\n  traffic:\n    proxy_url: http://x\n", "未知字段"},
		{"bad mode", "extensions:\n  traffic:\n    capture_mode: steal\n", "不合法"},
		{"not a mapping", "extensions:\n  traffic: relay\n", "映射"},
		{"decrypt not bool", "extensions:\n  traffic:\n    decrypt_https: sure\n", "布尔值"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := guardrailStore(t, tc.body).TrafficCapture()
			if !strings.Contains(view.Reason, tc.want) {
				t.Fatalf("reason = %q, want substring %q", view.Reason, tc.want)
			}
		})
	}
}

func TestTrafficCaptureDecryptWithoutRecordIsANoop(t *testing.T) {
	// The flag mirrors the config truthfully; the reason states it is a no-op.
	view := guardrailStore(t, "extensions:\n  traffic:\n    capture_mode: relay\n    decrypt_https: true\n").TrafficCapture()
	if !view.Configured || !view.DecryptHTTPS {
		t.Fatalf("view = %+v", view)
	}
	if !strings.Contains(view.Reason, "不生效") {
		t.Fatalf("reason = %q", view.Reason)
	}
}

func TestTrafficCaptureUnreadableCACertIsHonest(t *testing.T) {
	view := guardrailStore(t, "extensions:\n  traffic:\n    capture_mode: record\n    decrypt_https: true\n    ca_cert: "+filepath.Join(t.TempDir(), "missing.pem")+"\n").TrafficCapture()
	if view.CAFingerprint != "" || !strings.Contains(view.Reason, "读不到") {
		t.Fatalf("view = %+v", view)
	}
}

// The settings View is the console's one read: the capture summary must be in
// it, and no certificate bytes may ever appear.
func TestTrafficCaptureInsideSettingsView(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("SECRET-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := guardrailStore(t, "extensions:\n  traffic:\n    capture_mode: record\n    decrypt_https: true\n    ca_cert: "+ca+"\n")
	v := st.View()
	body, _ := json.Marshal(v)
	if !strings.Contains(string(body), `"traffic_capture"`) {
		t.Fatalf("settings view lacks traffic_capture: %.200s", body)
	}
	if strings.Contains(string(body), "SECRET-BYTES") {
		t.Fatalf("settings view leaked certificate bytes")
	}
}
