package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// A settings save that leaves the model half out must not unconfigure the
// model. The console always sends the whole half, but a script or a test that
// saves only a product section used to empty base_url, model and provider
// while keeping the key — which is how a live instance came to run degraded
// with "model is required" from two saves on 2026-11-27. A field the request
// carries is applied as before, an explicit empty string included: clearing
// the model on purpose still works.
func TestSettingsSaveKeepsModelFieldsTheRequestLeftOut(t *testing.T) {
	h, harness := singleModelHub(t)
	before := h.settings.Effective()

	// 1. Only a product section: nothing of the model half moves.
	rec := settingsPUT(t, h, `{"search_tavily_key":"tv-one"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("product-only save = %d %s", rec.Code, rec.Body.String())
	}
	after := h.settings.Effective()
	if after != before {
		t.Fatalf("a product-only save changed the model half:\n before %+v\n after  %+v", before, after)
	}

	// 2. The saved document still names the model.
	text, err := os.ReadFile(harness)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"base_url: http://fast:8080", "model: fast-model"} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("saved harness config lost %q:\n%s", want, text)
		}
	}

	// 3. A carried field is applied: the model changes, the rest stays.
	rec = settingsPUT(t, h, `{"model":"fast-model-2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("model-only save = %d %s", rec.Code, rec.Body.String())
	}
	if after = h.settings.Effective(); after.Model != "fast-model-2" || after.BaseURL != before.BaseURL || after.APIKey != before.APIKey {
		t.Fatalf("model-only save = %+v", after)
	}

	// 4. Clearing on purpose still works: an explicit empty model is a value.
	rec = settingsPUT(t, h, `{"model":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit clear = %d %s", rec.Code, rec.Body.String())
	}
	if after = h.settings.Effective(); after.Model != "" || after.BaseURL != before.BaseURL {
		t.Fatalf("explicit clear = %+v", after)
	}

	// 5. null is neither "keep" nor a value.
	rec = settingsPUT(t, h, `{"base_url":null,"model":"m"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("null model field = %d %s", rec.Code, rec.Body.String())
	}
}

// The response and the stored document agree after a partial save.
func TestSettingsPartialSaveResponseKeepsModel(t *testing.T) {
	h, _ := singleModelHub(t)
	rec := settingsPUT(t, h, `{"search_tavily_key":"tv-one,tv-two"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("partial save = %d %s", rec.Code, rec.Body.String())
	}
	var view struct {
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.BaseURL != "http://fast:8080" || view.Model != "fast-model" {
		t.Fatalf("response after a partial save = %+v", view)
	}
}
