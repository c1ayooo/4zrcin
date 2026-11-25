package aop

import (
	"encoding/json"
	"testing"
)

func TestToolEnvExtensionCarriesAbsoluteValues(t *testing.T) {
	if ToolEnvExtension(nil) != nil {
		t.Fatal("an empty env must not create an extension")
	}
	if ToolEnvExtension(map[string]string{" ": "x"}) != nil {
		t.Fatal("a blank key must not create an extension")
	}
	ext := ToolEnvExtension(map[string]string{
		"ZRCIN_BATTLE": "http://127.0.0.1:8080/p/p-1/api",
		"ZRCIN_AUTH":   "Authorization: Bearer tok",
	})
	if ext.GetTypeUrl() != ToolEnvExtensionURL {
		t.Fatalf("type url = %q", ext.GetTypeUrl())
	}
	var payload toolEnvPayload
	if err := json.Unmarshal(ext.GetValue(), &payload); err != nil {
		t.Fatalf("payload = %q: %v", ext.GetValue(), err)
	}
	if payload.Env["ZRCIN_BATTLE"] != "http://127.0.0.1:8080/p/p-1/api" || payload.Env["ZRCIN_AUTH"] != "Authorization: Bearer tok" {
		t.Fatalf("payload = %+v", payload.Env)
	}
}
