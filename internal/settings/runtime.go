package settings

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults for the harness's shared `traffic` section, written into the host
// runtime configuration below. The harness default for body_storage is "none"
// (bounded previews only, see cyber-harness pkg/config/traffic.go), which keeps
// the console from showing the exchange's original bytes; Tianwu switches the
// local scanner's store to disk and keeps the limits bounded.
const (
	defaultTrafficBodyStorage        = "disk"
	defaultTrafficBodyMaxBytes       = int64(8 << 20) // 8 MiB per request/response body
	defaultTrafficBodyRetentionBytes = int64(2 << 30) // 2 GiB retained on disk
)

// runtimeDocument builds the document RuntimeConfig writes. It is split out so
// a settings save can validate the merged configuration (ValidateRuntimeConfig)
// before it persists anything, without materialising a file.
func (s *Store) runtimeDocument() (*yaml.Node, error) {
	// The operator's cyber.yaml is copied through, not filtered down to the
	// shared sections. readDocument keeps every unknown key, and that is
	// deliberate for the product sections (mcp, search, guardrail, jev, …):
	// they live under `extensions` because that is the one place the harness's
	// strict file validator accepts a section the Hub itself does not declare.
	// The Hub preserves them as "extension unavailable in this host"
	// diagnostics (cyber-harness pkg/config/snapshot.go), and internal/host
	// reuses this same --config file for the local scan node
	// (Host.nodeArgs/Host.execLaunch), whose extensions do resolve them.
	// Stripping them here would silently drop the operator's
	// MCP/search/guardrail configuration. Only the LLM catalogue is rewritten
	// below; everything else stays verbatim. validateRootDocument then rejects
	// a legacy top-level product section with the correct location named.
	root := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	doc := root.Content[0]
	if s.harness != "" {
		var err error
		root, doc, err = s.readDocument()
		if err != nil {
			return nil, err
		}
		// The harness resolves misc.data_dir relative to its configuration
		// file. Moving that file must not move the operator's runtime data.
		if misc := mapGet(doc, "misc"); misc != nil {
			if dataDir := scalar(mapGet(misc, "data_dir")); dataDir != "" && !filepath.IsAbs(dataDir) {
				sourceDir, err := filepath.Abs(filepath.Dir(s.harness))
				if err != nil {
					return nil, err
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

	// The traffic section is a *shared* harness section: cyber-harness
	// pkg/config/shared_distribution.go projects TrafficOptions into
	// types.DistributeConfig.Traffic (body_storage / body_max_bytes /
	// body_retention_bytes), and pkg/web/service/node_config.go's nodeConfig
	// clones that DistributeConfig (proto.CloneOf) when it distributes the
	// runtime to every scan node. Writing it here is therefore enough for the
	// local scan node, whose harness then records HTTP bodies on disk (the
	// `--observe http` flows already reach the ledger as aop.traffic.Flow).
	//
	// The blank document was read from the operator's cyber.yaml, so an
	// explicit `traffic:` there wins field by field: readDocument preserves the
	// section, and ensureTraffic only fills keys the operator left out instead
	// of overwriting the whole mapping. Deliberately not validating "none" away:
	// an operator may keep the harness's preview-only mode.
	if err := ensureTraffic(doc); err != nil {
		return nil, err
	}
	// The same document is parsed by the Hub and by the local scan, and both
	// validate it strictly (cyber-harness pkg/config/snapshot.go
	// validateMapping): a top-level key that is not one of Option's grouped
	// fields is "unknown configuration field". Product extensions are declared
	// only by the scan, so they belong under `extensions:` where an undeclared
	// section is preserved and diagnosed instead of rejected. Rejecting a
	// legacy top-level section here names the operator's mistake while they are
	// editing settings, not at the next scan startup.
	if err := validateRootDocument(doc); err != nil {
		return nil, err
	}
	return root, nil
}

// ValidateRuntimeConfig applies the checks RuntimeConfig would apply, without
// writing. A settings save calls it before persisting so a legacy top-level
// product section (which the harness rejects at startup) is reported while the
// operator is editing, not at the next boot.
func (s *Store) ValidateRuntimeConfig() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.runtimeDocument()
	return err
}

// RuntimeConfig materialises the default host/audit configuration. The saved
// document also carries Tianwu admission and scheduling metadata, which the
// harness's strict parser must never see, even on an inactive profile.
// A content-addressed path makes Host.Update notice changes to file-only
// settings while a no-op save or admission toggle leaves live sessions alone.
func (s *Store) RuntimeConfig(dir string) (string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, err := s.runtimeDocument()
	if err != nil {
		return "", err
	}
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

// ensureTraffic fills the shared harness traffic section with Tianwu's bounded
// on-disk defaults. Only absent keys are added: the operator's cyber.yaml is the
// authority, and a partially specified section (for example body_storage only)
// must keep every explicit value while the rest falls back to the limits here.
// Values that the harness would reject are reported now, with the section named,
// instead of surfacing later as an opaque scan startup failure.
func ensureTraffic(doc *yaml.Node) error {
	traffic := mapGet(doc, "traffic")
	switch {
	case traffic == nil:
		traffic = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapSet(doc, "traffic", traffic)
	case traffic.Kind != yaml.MappingNode:
		return fmt.Errorf("harness 配置的 traffic 段必须是映射")
	}
	if mapGet(traffic, "body_storage") == nil {
		setScalar(traffic, "body_storage", defaultTrafficBodyStorage)
	}
	if mapGet(traffic, "body_max_bytes") == nil {
		mapSet(traffic, "body_max_bytes", intScalar(defaultTrafficBodyMaxBytes))
	}
	if mapGet(traffic, "body_retention_bytes") == nil {
		mapSet(traffic, "body_retention_bytes", intScalar(defaultTrafficBodyRetentionBytes))
	}
	return validateTraffic(traffic)
}

// validateTraffic mirrors the checks in cyber-harness pkg/config/traffic.go
// Normalize: body_storage is "none" or "disk", the limits are non-negative, the
// harness's zero-means-default spelling is honoured, and two body_max_bytes must
// fit the retention budget. Duplicating the rule is intentional — the control
// plane writes the file without the harness package available, and the operator
// gets the error while editing settings rather than at the next scan start.
func validateTraffic(traffic *yaml.Node) error {
	storage := strings.ToLower(scalar(mapGet(traffic, "body_storage")))
	if storage == "" {
		storage = "none" // harness Normalize: an empty value defaults to none
	}
	if storage != "none" && storage != "disk" {
		return fmt.Errorf("harness 配置的 traffic.body_storage 必须是 none 或 disk（当前 %q）", storage)
	}
	maxBytes, err := int64Field(traffic, "body_max_bytes")
	if err != nil {
		return err
	}
	retention, err := int64Field(traffic, "body_retention_bytes")
	if err != nil {
		return err
	}
	if maxBytes < 0 || retention < 0 {
		return fmt.Errorf("harness 配置的 traffic 上限不能为负（body_max_bytes=%d, body_retention_bytes=%d）", maxBytes, retention)
	}
	if maxBytes == 0 {
		maxBytes = defaultTrafficBodyMaxBytes
	}
	if retention == 0 {
		retention = defaultTrafficBodyRetentionBytes
	}
	if maxBytes > retention/2 {
		return fmt.Errorf("harness 配置的 traffic 限制不成立：body_max_bytes(%d) 不能超过 body_retention_bytes(%d) 的一半", maxBytes, retention)
	}
	return nil
}

// runtimeRootKeys is the set of root sections cyber-harness config.Option
// declares (pkg/config/options.go, `config:"..."` tags). Anything else is
// rejected by the harness's strict file validator, so the generated runtime
// document must never carry one.
var runtimeRootKeys = map[string]bool{
	"llm": true, "traffic": true, "agent": true, "inbox": true,
	"node": true, "extensions": true, "output": true, "misc": true,
}

// validateRootDocument rejects root sections the harness would reject. Product
// sections (mcp/search/guardrail/jev) are scan-side extensions: they are valid
// only under `extensions`. Naming the section and showing the correct location
// is the whole point — the harness error alone says "unknown configuration
// field mcp" and leaves the operator guessing.
func validateRootDocument(doc *yaml.Node) error {
	if doc == nil || doc.Kind != yaml.MappingNode {
		return fmt.Errorf("harness 配置根节点必须是映射")
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i].Value
		if runtimeRootKeys[key] {
			continue
		}
		if isProductSection(key) {
			return fmt.Errorf("harness 配置顶层字段 %q 不被 harness 接受；%s 段请写在 extensions: 下（例如 extensions.%s）", key, key, key)
		}
		return fmt.Errorf("harness 配置顶层字段 %q 不被 harness 接受", key)
	}
	return nil
}

// isProductSection names the sections the console edits through the product
// API, so the misplaced-section error can point at the right location.
func isProductSection(key string) bool {
	switch key {
	case "mcp", "search", "guardrail", "jev":
		return true
	}
	return false
}

func intScalar(value int64) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(value, 10)}
}

func int64Field(node *yaml.Node, key string) (int64, error) {
	value := mapGet(node, key)
	if value == nil || strings.TrimSpace(value.Value) == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value.Value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("harness 配置的 %s 不是整数：%q", key, value.Value)
	}
	return n, nil
}
