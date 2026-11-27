package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// Saving the judge credentials from the console works end to end: the key
// lands in the operator's cyber.yaml for the scan to read at start, the
// response carries the routing metadata without echoing the key, and the
// restart reason names the section — the file is storage, the node reads it
// at process start.
func TestSettingsSaveWritesJEVCredentials(t *testing.T) {
	h, harness := singleModelHub(t)

	rec := settingsPUT(t, h, `{"jev_write":{"api_key":"tsk-secret-1","base_url":"https://relay.example/v1","model":"judge-1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings save = %d %s", rec.Code, rec.Body.String())
	}
	var view settings.View
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Jev.Configured || view.Jev.Model != "judge-1" || view.Jev.BaseURL != "https://relay.example/v1" {
		t.Fatalf("jev view = %+v", view.Jev)
	}
	// Restart gating: the fixture runs no host, so applyModelPath reports the
	// "next start" form regardless of which section changed. The jev restart
	// note is asserted at the store level instead (jevChanged feeds
	// restartParts in serveSettings).
	if strings.Contains(rec.Body.String(), "tsk-secret-1") {
		t.Fatalf("response echoed the key: %s", rec.Body.String())
	}
	data, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tsk-secret-1") {
		t.Fatalf("the key must land in cyber.yaml for scan to read: %s", data)
	}

	// A save that does not touch the key keeps it (the console cannot echo it,
	// so an empty api_key means "leave alone").
	rec = settingsPUT(t, h, `{"jev_write":{"model":"judge-2"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second save = %d %s", rec.Code, rec.Body.String())
	}
	view = settings.View{}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Jev.Configured || view.Jev.Model != "judge-2" {
		t.Fatalf("jev view after routing-only save = %+v", view.Jev)
	}
	data, err = os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tsk-secret-1") {
		t.Fatalf("routing-only save wiped the key: %s", data)
	}

	// An explicit clear removes the key.
	rec = settingsPUT(t, h, `{"jev_write":{"clear_api_key":true}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear save = %d %s", rec.Code, rec.Body.String())
	}
	data, err = os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tsk-secret-1") {
		t.Fatalf("clear save left the key: %s", data)
	}

	// A rejected base URL never rides behind a saved model half.
	rec = settingsPUT(t, h, `{"jev_write":{"base_url":"ftp://nope"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid base_url = %d %s", rec.Code, rec.Body.String())
	}
}
