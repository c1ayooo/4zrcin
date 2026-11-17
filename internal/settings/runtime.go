package settings

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RuntimeConfig materialises the default host/audit configuration. The saved
// document also carries Tianwu admission and scheduling metadata, which the
// harness's strict parser must never see, even on an inactive profile.
// A content-addressed path makes Host.Update notice changes to file-only
// settings while a no-op save or admission toggle leaves live sessions alone.
func (s *Store) RuntimeConfig(dir string) (string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	doc := root.Content[0]
	if s.harness != "" {
		var err error
		root, doc, err = s.readDocument()
		if err != nil {
			return "", err
		}
		// The harness resolves misc.data_dir relative to its configuration
		// file. Moving that file must not move the operator's runtime data.
		if misc := mapGet(doc, "misc"); misc != nil {
			if dataDir := scalar(mapGet(misc, "data_dir")); dataDir != "" && !filepath.IsAbs(dataDir) {
				sourceDir, err := filepath.Abs(filepath.Dir(s.harness))
				if err != nil {
					return "", err
				}
				setScalar(misc, "data_dir", filepath.Join(sourceDir, dataDir))
			}
		}
	}
	// The runtime sees one endpoint, key and model. Legacy provider catalogues
	// remain in the operator file; they cannot become fallback routes.
	m := s.Effective()
	llm := ensureMapping(doc, "llm")
	mapDelete(llm, "providers")
	mapDelete(llm, "active_profile")
	for _, key := range []string{"pricing", "tier", "api_keys", "max_concurrency", "fallback", "paused"} {
		mapDelete(llm, key)
	}
	setScalar(llm, "provider", m.Provider)
	setScalar(llm, "base_url", m.BaseURL)
	setScalar(llm, "api_key", m.APIKey)
	setScalar(llm, "model", m.Model)
	setIntOrDelete(llm, "context_window", m.ContextWindow)
	setIntOrDelete(llm, "max_tokens", m.MaxTokens)
	data, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("编码宿主运行配置失败")
	}
	digest := sha256.Sum256(data)
	path, err := filepath.Abs(filepath.Join(dir, fmt.Sprintf("runtime-%x.yaml", digest[:12])))
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, data); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) SecretValues() []string {
	values := []string{s.Effective().APIKey}
	// Historic secrets still need redaction, even though they are never selected.
	if root, _, err := s.readDocument(); err == nil {
		var walk func(*yaml.Node)
		walk = func(node *yaml.Node) {
			if node.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(node.Content); i += 2 {
					key, value := node.Content[i].Value, node.Content[i+1]
					if key == "api_key" && value.Kind == yaml.ScalarNode {
						values = append(values, value.Value)
					}
					if key == "api_keys" {
						for _, item := range value.Content {
							if item.Kind == yaml.ScalarNode {
								values = append(values, item.Value)
							}
						}
					}
					walk(value)
				}
			} else {
				for _, item := range node.Content {
					walk(item)
				}
			}
		}
		walk(root)
	}
	return values
}
