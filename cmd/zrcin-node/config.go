package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Layout (docs/15 §4.1): one root owns the binaries, the config and the logs;
// the mirrored data directory lives at the platform's --data path and carries
// its own ownership marker, because the uninstaller may only delete paths this
// node created.
const (
	ownerMarker   = ".zrcin-node-owner"
	configRel     = "etc/node.json"
	installIDRel  = "var/install-id"
	binRel        = "bin"
	defaultRoot   = "/opt/zrcin-node"
	userRootName  = ".zrcin-node"
	serviceName   = "zrcin-node"
	configVersion = 1
)

// Config is what install writes and run reads: the join response plus the
// local choices. It holds two live credentials (the node credential and the
// Hub access key), so the file is 0600 and never logged.
type Config struct {
	Version int    `json:"version"`
	NodeID  string `json:"node_id"`
	Name    string `json:"name"`
	Recipe  string `json:"recipe"`

	PublicURL   string `json:"public_url"`
	TunnelKey   string `json:"tunnel_key"` // hex; the instance tunnel/login key
	TunnelPath  string `json:"tunnel_path"`
	HubAddr     string `json:"hub_addr"`     // loopback host:port mirrored locally
	HubKey      string `json:"hub_key"`      // Hub access key for --server-url
	ConsoleAddr string `json:"console_addr"` // loopback host:port mirrored locally
	DataDir     string `json:"data_dir"`     // the platform's --data path, mirrored

	Credential string `json:"credential"` // zn1_…
	InstallID  string `json:"install_id"` // stable per host, binds token→host
	Root       string `json:"root"`       // installation root
	Service    string `json:"service"`    // "system" | "user" | "none"
	ScanBin    string `json:"scan_bin"`   // absolute path of the scan binary
	CLIBin     string `json:"cli_bin"`    // absolute path of zrcin (for $ZRCIN_BIN)
}

// Paths is the root layout.
type Paths struct {
	Root    string
	BinDir  string
	Config  string
	Install string
	VarDir  string
}

func pathsFor(root string) Paths {
	return Paths{
		Root:    root,
		BinDir:  filepath.Join(root, binRel),
		Config:  filepath.Join(root, configRel),
		Install: filepath.Join(root, installIDRel),
		VarDir:  filepath.Join(root, "var"),
	}
}

// defaultRootFor picks the installation root: root installs go to /opt, a
// non-root operator gets a home directory. --root overrides both.
func defaultRootFor() (string, error) {
	if os.Geteuid() == 0 {
		return defaultRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户目录：%w", err)
	}
	return filepath.Join(home, userRootName), nil
}

// loadConfig reads etc/node.json; a missing or unreadable file is an error the
// caller reports, never a silent fresh start (a fresh start would orphan a
// running tunnel's credential).
func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s：%w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s 不是合法配置：%w", path, err)
	}
	if cfg.Version != configVersion {
		return nil, fmt.Errorf("%s 的版本是 %d，本程序支持 %d；请重新安装", path, cfg.Version, configVersion)
	}
	return &cfg, nil
}

// saveConfig writes the config atomically with 0600: it carries credentials.
func saveConfig(path string, cfg *Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("编码配置：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录：%w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".node-*.tmp")
	if err != nil {
		return fmt.Errorf("写配置：%w", err)
	}
	name := tmp.Name()
	defer os.Remove(name) //nolint:errcheck
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("写配置：%w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("写配置：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写配置：%w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("写配置：%w", err)
	}
	return nil
}

// ensureInstallID returns this host's stable install id. It is derived from
// the machine id so a reinstall (and a platform restart) maps back to the same
// node; hosts without a machine id fall back to a random id persisted under
// the root.
func ensureInstallID(paths Paths) (string, error) {
	for _, candidate := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		raw, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(raw)); id != "" {
			sum := sha256.Sum256([]byte("4zrcin/install-id/v1\x00" + id))
			return hex.EncodeToString(sum[:8]), nil
		}
	}
	if raw, err := os.ReadFile(paths.Install); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id, nil
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成安装标识：%w", err)
	}
	id := hex.EncodeToString(b[:])
	if err := os.MkdirAll(filepath.Dir(paths.Install), 0o700); err != nil {
		return "", fmt.Errorf("写安装标识：%w", err)
	}
	if err := os.WriteFile(paths.Install, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写安装标识：%w", err)
	}
	return id, nil
}

// ownerOf reads a directory's ownership marker. Absent marker is "".
func ownerOf(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ownerMarker))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// markOwned writes the ownership marker for a node.
func markOwned(dir, nodeID string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s：%w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ownerMarker), []byte(nodeID+"\n"), 0o644); err != nil {
		return fmt.Errorf("写归属标记 %s：%w", dir, err)
	}
	return nil
}

// pathOwnedBy reports whether dir is absent, or present and marked as this
// node's. Anything else is somebody else's directory and must not be touched.
func pathOwnedBy(dir, nodeID string) (owned bool, exists bool) {
	st, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, false
	}
	if err != nil || !st.IsDir() {
		return false, true
	}
	return ownerOf(dir) == nodeID, true
}

// hostPortSplit keeps host and port from "host:port".
func hostPortSplit(addr string) (host, port string, ok bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "", "", false
	}
	return host, port, true
}
