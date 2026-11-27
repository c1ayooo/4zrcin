package aop

import (
	"github.com/chainreactors/cyber/aop/payloads"
	"testing"
)

// The seed extension is additive JSON-in-Any (docs/14 §8 Z1): an empty seed
// sends nothing, and the payload stays inspectable on the wire.
func TestCaseSeedExtension(t *testing.T) {
	if CaseSeedExtension(nil) != nil {
		t.Fatal("an empty seed must not send an extension")
	}
	seed := SeedFile{Path: ".zrcin/context.json", Mode: 0o640, Data: []byte(`{"a":1}`)}
	ext := CaseSeedExtension([]SeedFile{seed})
	if ext == nil || ext.GetTypeUrl() != CaseSeedExtensionURL {
		t.Fatalf("extension = %v", ext)
	}
	// The wire decodes through the contract's own parser — the same code the
	// node runs.
	files, err := payloads.ParseCaseSeed(ext)
	if err != nil || len(files) != 1 {
		t.Fatalf("parse = %v / %d files", err, len(files))
	}
	if files[0].Path != seed.Path || files[0].Mode != seed.Mode || string(files[0].Data) != string(seed.Data) {
		t.Fatalf("round trip = %+v", files[0])
	}
}
