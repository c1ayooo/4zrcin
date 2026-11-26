package aop

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// The seed extension is additive JSON-in-Any (docs/15 §8 Z1): an empty seed
// sends nothing, and the payload stays inspectable on the wire.
func TestCaseSeedExtension(t *testing.T) {
	if CaseSeedExtension(nil) != nil {
		t.Fatal("an empty seed must not send an extension")
	}
	ext := CaseSeedExtension([]SeedFile{{Path: ".zrcin/context.json", Mode: 0o640, Data: []byte(`{"a":1}`)}})
	if ext == nil || ext.GetTypeUrl() != CaseSeedExtensionURL {
		t.Fatalf("extension = %v", ext)
	}
	var payload seedPayload
	if err := json.Unmarshal(ext.GetValue(), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(payload.Files) != 1 || payload.Files[0].Path != ".zrcin/context.json" || payload.Files[0].Mode != 0o640 {
		t.Fatalf("payload = %+v", payload)
	}
	data, err := base64.StdEncoding.DecodeString(payload.Files[0].Data)
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("data = %q %v", data, err)
	}
}
