package settings

import (
	"os"
	"strings"
	"testing"
)

// The write path round-trips through the read projection: what SetJEV stores
// is exactly what JEV reports (minus the key, which is never echoed), the
// untouched-key rule keeps a save that did not touch the credential from
// wiping it, and the empty section leaves no husk in the file.
func TestSetJEVRoundTrip(t *testing.T) {
	st := guardrailStore(t, "")

	if err := st.SetJEV(JEVInput{APIKey: "tsk-123", BaseURL: "https://relay.example/v1", Model: "judge-1"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	view := st.JEV()
	if !view.Configured {
		t.Fatalf("view after write = %+v", view)
	}
	if view.BaseURL != "https://relay.example/v1" || view.Model != "judge-1" {
		t.Fatalf("routing values = %+v", view)
	}
	data, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tsk-123") {
		t.Fatalf("the key must land in the file for scan to read it")
	}

	// An identical save is no change.
	changed, err := st.JEVDiffers(JEVInput{APIKey: "tsk-123", BaseURL: "https://relay.example/v1", Model: "judge-1"})
	if err != nil || changed {
		t.Fatalf("identical save differs = %v, %v", changed, err)
	}
	// An untouched key is no change even though the input cannot carry it.
	changed, err = st.JEVDiffers(JEVInput{BaseURL: "https://relay.example/v1", Model: "judge-1"})
	if err != nil || changed {
		t.Fatalf("untouched key differs = %v, %v", changed, err)
	}
	// A routing-only save keeps the key and changes the model.
	if err := st.SetJEV(JEVInput{BaseURL: "https://relay.example/v2", Model: "judge-2"}); err != nil {
		t.Fatalf("routing-only save: %v", err)
	}
	if view := st.JEV(); !view.Configured || view.Model != "judge-2" || view.BaseURL != "https://relay.example/v2" {
		t.Fatalf("routing-only save lost the key or values: %+v", view)
	}

	// An explicit clear removes the key but keeps the routing values.
	if err := st.SetJEV(JEVInput{BaseURL: "https://relay.example/v2", ClearAPIKey: true}); err != nil {
		t.Fatalf("clear save: %v", err)
	}
	if view := st.JEV(); view.Configured {
		t.Fatalf("cleared save left a configured judge: %+v", view)
	}

	// The empty input removes the last fields — and a jev section that only
	// held them is removed entirely, so no empty husk survives.
	if err := st.SetJEV(JEVInput{}); err != nil {
		t.Fatalf("empty save: %v", err)
	}
	if view := st.JEV(); view.Configured || view.BaseURL != "" || view.Model != "" {
		t.Fatalf("empty save left values: %+v", view)
	}
	data, err = os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "jev") {
		t.Fatalf("empty save left a jev section: %s", data)
	}
	changed, err = st.JEVDiffers(JEVInput{})
	if err != nil || changed {
		t.Fatalf("empty-vs-unconfigured differs = %v, %v", changed, err)
	}
}

// Validation rejects the impossible before anything is written: an ambiguous
// clear-plus-key request, and a base URL the engine's client could not dial.
func TestValidateJEV(t *testing.T) {
	st := guardrailStore(t, "")

	if err := st.ValidateJEV(JEVInput{APIKey: "k", ClearAPIKey: true}); err == nil {
		t.Fatal("clear plus key must be rejected")
	}
	for _, bad := range []string{"ftp://x", "https://", "not a url"} {
		if err := st.ValidateJEV(JEVInput{BaseURL: bad}); err == nil {
			t.Fatalf("base_url %q must be rejected", bad)
		}
	}
	if err := st.ValidateJEV(JEVInput{BaseURL: "https://relay.example/v1"}); err != nil {
		t.Fatalf("valid base_url rejected: %v", err)
	}
}
