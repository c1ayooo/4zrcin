package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// The capture switch rides the settings PUT like the retention override: the
// write lands in cyber.yaml (cyberhub.mitm — the switch the scan's own
// registry declares), the response projects the new state and reports the
// restart, and a rejected value leaves the model half unapplied.
func TestSettingsSaveWritesTrafficCapture(t *testing.T) {
	h, harness := singleModelHub(t)
	rec := settingsPUT(t, h, `{"base_url":"http://fast:8080","model":"fast-model",
		"traffic_capture_write":{"capture_mode":"relay"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings save = %d %s", rec.Code, rec.Body.String())
	}
	var view settings.View
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.TrafficCapture.CaptureMode != "relay" {
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
	if !strings.Contains(string(stored), "mitm: false") {
		t.Fatalf("stored document lacks the explicit mitm=false:\n%s", stored)
	}

	// A second identical save detects no change: no restart reason, and the
	// apply path stays out of the way.
	rec = settingsPUT(t, h, `{"traffic_capture_write":{"capture_mode":"relay"}}`)
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

// A rejected mode is a 400 before anything is persisted: the model half of
// the combined save must not land either.
func TestSettingsSaveRejectsBadTrafficCapture(t *testing.T) {
	h, harness := singleModelHub(t)
	before, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	rec := settingsPUT(t, h, `{"base_url":"http://fast:8080","model":"fast-model",
		"traffic_capture_write":{"capture_mode":"intercept"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a rejected capture write still touched cyber.yaml")
	}
}
