package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// The capture section rides the settings PUT like the retention override: the
// write lands in cyber.yaml, the response projects the new state and reports
// the restart (extensions.traffic is read by scan at process start), and a
// rejected combination leaves the model half unapplied.
func TestSettingsSaveWritesTrafficCapture(t *testing.T) {
	h, harness := singleModelHub(t)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("CERTIFICATE-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"base_url":"http://fast:8080","model":"fast-model",
		"traffic_capture_write":{"capture_mode":"record","decrypt_https":true,"ca_cert":"` + ca + `"}}`
	rec := settingsPUT(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings save = %d %s", rec.Code, rec.Body.String())
	}
	var view settings.View
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.TrafficCapture.Configured || view.TrafficCapture.CaptureMode != "record" || !view.TrafficCapture.DecryptHTTPS {
		t.Fatalf("capture projection = %+v", view.TrafficCapture)
	}
	// With a managed local host the reason names the capture section; a
	// hostless test hub reports "next launch". Either way the save must say
	// the change is not live yet.
	if !strings.Contains(view.RestartReason, "流量捕获配置") && !strings.Contains(view.RestartReason, "下次启动时生效") {
		t.Fatalf("restart state = %v / %q", view.RequiresRestart, view.RestartReason)
	}
	stored, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"capture_mode: record", "decrypt_https: true", "ca_cert: " + ca} {
		if !strings.Contains(string(stored), want) {
			t.Fatalf("stored document lacks %q:\n%s", want, stored)
		}
	}

	// A second identical save detects no change: no restart reason, and the
	// apply path stays out of the way.
	rec = settingsPUT(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("second save = %d %s", rec.Code, rec.Body.String())
	}
	var again settings.View
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.RequiresRestart {
		t.Fatalf("unchanged capture save requested a restart: %q", again.RestartReason)
	}
}

// An impossible combination is a 400 before anything is persisted: the model
// half of the combined save must not land either.
func TestSettingsSaveRejectsBadTrafficCapture(t *testing.T) {
	h, harness := singleModelHub(t)
	before, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	rec := settingsPUT(t, h, `{"base_url":"http://fast:8080","model":"fast-model",
		"traffic_capture_write":{"capture_mode":"relay","decrypt_https":true}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "relay 透传模式无法解密") {
		t.Fatalf("error text = %s", rec.Body.String())
	}
	after, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("rejected save modified the document")
	}
}
