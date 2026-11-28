package settings

// Product configuration (MCP servers and web search) is the scan node's own
// configuration, not a shared harness option. It lives in the operator's
// cyber.yaml under `extensions` — the one place the harness's strict file
// validator accepts product sections it does not itself declare. The generated
// runtime yaml copies the document through unchanged, so the scan reads the
// same sections from the file the local host is started with.
//
// The settings API is the operator surface for these two sections. It never
// echoes a stored secret: MCP env/header values read back as maskedValue, and
// the Tavily key reads back as has_key/count only. On save a masked value is
// kept from the stored section instead of being written literally.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	mcpext "github.com/chainreactors/cyber/exts/mcp"
	mcptools "github.com/chainreactors/cyber/tools/mcp"
	"gopkg.in/yaml.v3"
)

// maskedValue is the placeholder the read path uses for stored MCP env/header
// values. A save that sends it back keeps the previous value (it is not a
// credential anyone could legitimately want to set).
const maskedValue = "***"

// mcpAliasPattern is the control plane's own naming rule for product section
// aliases. It matches the harness's alias rule today; validateProductAlias
// runs ahead of the harness check so the commonest editing error stays in the
// operator's language, while the harness's own Validate remains the source of
// truth for what a scan actually accepts.
var mcpAliasPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Product is the stored product configuration. An empty Server map means the
// section is absent (the scan does not install MCP servers).
type Product struct {
	MCPServers map[string]mcptools.Config
	SearchKeys string
}

// ProductInput is one settings save. Nil MCPServers keeps the stored servers;
// an empty (non-nil) map removes the section. An empty SearchKey keeps the
// stored keys; ClearSearchKey removes them.
type ProductInput struct {
	MCPServers     *map[string]mcptools.Config
	SearchKey      string
	ClearSearchKey bool
}

// ProductView is the API projection: no secret values, only their presence.
type ProductView struct {
	MCPServers     map[string]mcptools.Config
	SearchKeySet   bool
	SearchKeyCount int
}

// validateProductAlias is the control plane's own alias-naming rule. The
// control plane names the bad alias in its error instead of failing later at
// scan startup.
func validateProductAlias(alias string) error {
	if !mcpAliasPattern.MatchString(alias) {
		return fmt.Errorf("MCP 服务器别名 %q 只能用字母、数字、- 和 _", alias)
	}
	return nil
}

// validateMCP is the save-time gate, and the harness's own rules are the only
// copy: mcptools.Config.Validate for the server shape (the hand-translated
// duplicate this used to carry drifted — it missed the timeout upper bound)
// and mcpext.ValidateBashAlias for the alias namespace the extension installs
// commands into. Only the alias-naming rule above stays local.
func validateMCP(alias string, server mcptools.Config) error {
	if err := validateProductAlias(alias); err != nil {
		return err
	}
	if err := server.Validate(alias); err != nil {
		return err
	}
	return mcpext.ValidateBashAlias(alias)
}

// validateSearchKey accepts the harness's comma-separated Tavily key list. The
// value is written verbatim; keys are credentials, so only shape is checked.
func validateSearchKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if strings.ContainsAny(key, "\r\n\x00") {
		return fmt.Errorf("search_tavily_key 不能包含换行或 NUL")
	}
	for _, part := range strings.Split(key, ",") {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("search_tavily_key 里逗号之间不能有空键")
		}
	}
	return nil
}

func searchKeyCount(key string) int {
	count := 0
	for _, part := range strings.Split(key, ",") {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
}

// Product reads the stored product sections. A missing file/section is an
// empty product, not an error. A malformed section is reported: silently
// showing "no MCP servers" would let a save wipe a configuration the operator
// cannot see.
func (s *Store) Product() (Product, error) {
	_, doc, err := s.readDocument()
	if err != nil {
		return Product{}, err
	}
	return productFromDocument(doc)
}

func productFromDocument(doc *yaml.Node) (Product, error) {
	var out Product
	mcp, err := productMapping(doc, "mcp")
	if err != nil {
		return out, err
	}
	// The merged engine's mcp section (post-#205) maps service names directly;
	// the legacy mcpServers wrapper is the fork's pre-merge shape and still
	// reads.
	servers := mapGet(mcp, "mcpServers")
	if servers != nil && len(servers.Content) > 0 {
		if servers.Kind != yaml.MappingNode {
			return out, fmt.Errorf("mcp.mcpServers 必须是映射")
		}
		var fields map[string]any
		if err := servers.Decode(&fields); err != nil {
			return out, fmt.Errorf("mcp.mcpServers 结构无效：%w", err)
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return out, fmt.Errorf("mcp.mcpServers 结构无效：%w", err)
		}
		// The harness's own parser is the single source for the section's shape:
		// it rejects unknown fields and validates every server, so a document
		// the scan would refuse is reported here instead of being silently
		// cleaned on the next save.
		out.MCPServers, err = mcptools.Parse(encoded)
		if err != nil {
			return out, err
		}
	}
	if search, err := productMapping(doc, "search"); err != nil {
		return out, err
	} else if search != nil {
		out.SearchKeys = scalar(mapGet(search, "tavily_keys"))
	}
	return out, nil
}

// ProductView is the masked read. MCP env/header values become maskedValue.
func (s *Store) ProductView() (ProductView, error) {
	product, err := s.Product()
	if err != nil {
		return ProductView{}, err
	}
	view := ProductView{MCPServers: maskMCP(product.MCPServers), SearchKeySet: product.SearchKeys != "", SearchKeyCount: searchKeyCount(product.SearchKeys)}
	return view, nil
}

func maskMCP(servers map[string]mcptools.Config) map[string]mcptools.Config {
	if len(servers) == 0 {
		return nil
	}
	out := make(map[string]mcptools.Config, len(servers))
	for alias, server := range servers {
		server = server.Clone()
		for key := range server.Env {
			server.Env[key] = maskedValue
		}
		for key := range server.Headers {
			server.Headers[key] = maskedValue
		}
		out[alias] = server
	}
	return out
}

// ValidateProduct is the save-time validation without writing: the API must be
// able to reject a bad product document before it persists the model half of a
// combined save.
func (s *Store) ValidateProduct(in ProductInput) error {
	if s.harness == "" {
		return fmt.Errorf("没有 harness 配置路径：设置 ZRCIN_HARNESS_CONFIG")
	}
	if in.MCPServers == nil && strings.TrimSpace(in.SearchKey) == "" && !in.ClearSearchKey {
		return nil
	}
	current, err := s.Product()
	if err != nil {
		return err
	}
	if in.MCPServers != nil {
		servers, err := mergeMCPSecrets(*in.MCPServers, current.MCPServers)
		if err != nil {
			return err
		}
		for alias, server := range servers {
			if err := validateMCP(alias, server); err != nil {
				return err
			}
		}
	}
	if in.ClearSearchKey {
		return nil
	}
	if key := strings.TrimSpace(in.SearchKey); key != "" {
		return validateSearchKey(key)
	}
	return nil
}

// ProductChanged reports whether a save would alter the stored product
// sections. The hot-update path is only safe for a shared-section (model)
// change; a product change needs the restart that makes the scan re-read its
// runtime file.
func (s *Store) ProductChanged(in ProductInput) (bool, error) {
	if in.MCPServers == nil && strings.TrimSpace(in.SearchKey) == "" && !in.ClearSearchKey {
		return false, nil
	}
	current, err := s.Product()
	if err != nil {
		return false, err
	}
	if in.MCPServers != nil {
		servers, err := mergeMCPSecrets(*in.MCPServers, current.MCPServers)
		if err != nil {
			return false, err
		}
		if !equalMCPServers(servers, current.MCPServers) {
			return true, nil
		}
	}
	search := current.SearchKeys
	if in.ClearSearchKey {
		search = ""
	} else if key := strings.TrimSpace(in.SearchKey); key != "" {
		search = key
	}
	return search != current.SearchKeys, nil
}

func equalMCPServers(a, b map[string]mcptools.Config) bool {
	if len(a) != len(b) {
		return false
	}
	for alias, left := range a {
		right, ok := b[alias]
		if !ok || !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

// SaveProduct writes the product sections into the operator's harness file,
// preserving every other section and every other extension. writeMu is shared
// with Save: both rewrite the same YAML document, so two saves cannot each
// write back the other's stale view.
func (s *Store) SaveProduct(in ProductInput) error {
	if s.harness == "" {
		return fmt.Errorf("没有 harness 配置路径：设置 ZRCIN_HARNESS_CONFIG")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, doc, err := s.readDocument()
	if err != nil {
		return err
	}
	current, err := productFromDocument(doc)
	if err != nil {
		return err
	}
	if in.MCPServers != nil {
		servers, err := mergeMCPSecrets(*in.MCPServers, current.MCPServers)
		if err != nil {
			return err
		}
		for alias, server := range servers {
			if err := validateMCP(alias, server); err != nil {
				return err
			}
		}
		if err := setMCPSection(doc, servers); err != nil {
			return err
		}
	}
	searchKey := current.SearchKeys
	switch {
	case in.ClearSearchKey:
		searchKey = ""
	case strings.TrimSpace(in.SearchKey) != "":
		searchKey = strings.TrimSpace(in.SearchKey)
	}
	if err := validateSearchKey(searchKey); err != nil {
		return err
	}
	if in.ClearSearchKey || strings.TrimSpace(in.SearchKey) != "" {
		setSearchSection(doc, searchKey)
	}
	if err := validateRootDocument(doc); err != nil {
		return err
	}
	return s.writeDocument(root)
}

// mergeMCPSecrets replaces the maskedValue placeholder with the stored value so
// a console round-trip (read masked → edit one field → save) cannot overwrite a
// working credential with "***". A placeholder with no stored value is an error,
// not a silent drop.
func mergeMCPSecrets(incoming, stored map[string]mcptools.Config) (map[string]mcptools.Config, error) {
	out := make(map[string]mcptools.Config, len(incoming))
	for alias, server := range incoming {
		server = server.Clone()
		was := stored[alias]
		for key, value := range server.Env {
			if value != maskedValue {
				continue
			}
			previous, ok := was.Env[key]
			if !ok {
				return nil, fmt.Errorf("MCP 服务器 %s 的环境变量 %s 是脱敏占位符，但没有已保存的值；请重新填写", alias, key)
			}
			server.Env[key] = previous
		}
		for key, value := range server.Headers {
			if value != maskedValue {
				continue
			}
			previous, ok := was.Headers[key]
			if !ok {
				return nil, fmt.Errorf("MCP 服务器 %s 的请求头 %s 是脱敏占位符，但没有已保存的值；请重新填写", alias, key)
			}
			server.Headers[key] = previous
		}
		out[alias] = server
	}
	return out, nil
}

func setMCPSection(doc *yaml.Node, servers map[string]mcptools.Config) error {
	migrateProductSections(doc)
	if len(servers) == 0 {
		mapDelete(mapGet(doc, "mcp"), "mcpServers")
		return nil
	}
	mcp := ensureMapping(doc, "mcp")
	encoded, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("编码 MCP 配置失败：%w", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return fmt.Errorf("编码 MCP 配置失败：%w", err)
	}
	var node yaml.Node
	if err := node.Encode(fields); err != nil {
		return fmt.Errorf("编码 MCP 配置失败：%w", err)
	}
	mapSet(mcp, "mcpServers", &node)
	return nil
}

func setSearchSection(doc *yaml.Node, key string) {
	migrateProductSections(doc)
	search := ensureMapping(doc, "search")
	if key == "" {
		mapDelete(search, "tavily_keys")
		return
	}
	setScalar(search, "tavily_keys", key)
}

// writeDocument encodes the whole operator document with the same indentation
// writeHarnessModel uses, so a product save does not reflow unrelated sections.
func (s *Store) writeDocument(root *yaml.Node) error {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("编码 %s 失败", s.harness)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("编码 %s 失败", s.harness)
	}
	return writeFileAtomic(s.harness, []byte(buf.String()))
}
