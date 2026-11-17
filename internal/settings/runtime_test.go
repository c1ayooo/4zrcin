package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeConfigUsesOnlySingleConnectionAndPreservesLegacySource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "cyber.yaml")
	document := `llm:
  active_profile: chosen
  providers:
    - id: chosen
      provider: openai
      base_url: http://gateway.test/v1
      model: gateway-route
      api_key: gateway-key
      api_keys: [retired-spare]
      tier: strong
      fallback: retired
    - id: retired
      base_url: http://retired.test/v1
      model: retired-model
      api_key: retired-key
agent:
  timeout: 60
`
	if err := os.WriteFile(source, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadAt(filepath.Join(dir, "settings.json"), source)
	if err != nil {
		t.Fatal(err)
	}
	path, err := s.RuntimeConfig(filepath.Join(dir, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"providers:", "active_profile:", "api_keys:", "tier:", "fallback:", "retired"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("runtime kept %s: %s", forbidden, data)
		}
	}
	for _, want := range []string{"gateway-route", "gateway-key", "http://gateway.test/v1", "timeout: 60"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s", want)
		}
	}
	original, _ := os.ReadFile(source)
	if string(original) != document {
		t.Fatal("reading runtime mutated operator configuration")
	}
	same, err := s.RuntimeConfig(filepath.Join(dir, "runtime"))
	if err != nil || same != path {
		t.Fatal("unchanged runtime configuration changed path")
	}
	for _, secret := range []string{"gateway-key", "retired-key", "retired-spare"} {
		found := false
		for _, v := range s.SecretValues() {
			if v == secret {
				found = true
			}
		}
		if !found {
			t.Fatalf("historic secret not redacted: %s", secret)
		}
	}
}
func TestSingleConnectionDoesNotSelectSpareKeys(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "cyber.yaml")
	if err := os.WriteFile(source, []byte("llm:\n  active_profile: old\n  providers:\n    - id: old\n      base_url: http://gateway.test/v1\n      model: route\n      api_keys: [spare-one, spare-two]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadAt(filepath.Join(dir, "settings.json"), source)
	if err != nil {
		t.Fatal(err)
	}
	if s.Effective().APIKey != "" {
		t.Fatal("removed key pool still supplies a credential")
	}
}
