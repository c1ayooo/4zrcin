package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// runInstall is the whole install path of docs/14 §7.2: join, precheck,
// download and verify, write the config, register the service. Every failure
// leaves no half-installed root (the temp files and the service are the last
// steps, and a failed precheck precedes any write).
func runInstall(ctx context.Context, joinURL, rootFlag string) error {
	base, token, err := splitJoinURL(joinURL)
	if err != nil {
		return err
	}
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return err
	}
	paths := pathsFor(root)
	installID, err := ensureInstallID(paths)
	if err != nil {
		return err
	}

	cfg, err := existingOrJoin(ctx, base, token, installID, root)
	if err != nil {
		return err
	}

	// Precheck: the mirrored data path must be ours (or absent), and there
	// must be room for the downloads. The loopback mirror ports are not a
	// precheck any more: they are allocated here, so a host that runs other
	// things on the platform's port numbers is not a failed install.
	changed, err := ensureMirrors(cfg)
	if err != nil {
		return err
	}
	if changed || cfg.MirrorHub == "" || cfg.MirrorConsole == "" {
		fmt.Fprintf(os.Stderr, "回环镜像口：Hub %s，控制台 %s（自动分配，不占用平台的 8080/18777）\n", cfg.MirrorHub, cfg.MirrorConsole)
	}
	if owned, exists := pathOwnedBy(cfg.DataDir, cfg.NodeID); !owned {
		if exists {
			return fmt.Errorf("数据目录 %s 已存在且不属于本节点（没有 %s 标记）；不会覆盖别人的目录", cfg.DataDir, ownerMarker)
		}
	}
	if err := precheckDisk(cfg); err != nil {
		return err
	}

	if err := os.MkdirAll(paths.BinDir, 0o755); err != nil {
		return fmt.Errorf("创建 %s：%w", paths.BinDir, err)
	}
	// The launcher installs itself: the service unit and the detached
	// uninstaller both exec <root>/bin/zrcin-node, and the bootstrap script's
	// temporary copy must not be the only one on disk.
	if err := installSelf(filepath.Join(paths.BinDir, "zrcin-node")); err != nil {
		return err
	}
	if err := downloadArtifact(ctx, base, cfg.Credential, enroll.ArtScanFull, filepath.Join(paths.BinDir, "cyber-scan")); err != nil {
		return err
	}
	if err := downloadArtifact(ctx, base, cfg.Credential, enroll.ArtCLI, filepath.Join(paths.BinDir, "zrcin")); err != nil {
		return err
	}
	cfg.ScanBin = filepath.Join(paths.BinDir, "cyber-scan")
	cfg.CLIBin = filepath.Join(paths.BinDir, "zrcin")
	cfg.Root = root

	if err := markOwned(root, cfg.NodeID); err != nil {
		return err
	}
	if err := markOwned(cfg.DataDir, cfg.NodeID); err != nil {
		return err
	}
	if err := saveConfig(paths.Config, cfg); err != nil {
		return err
	}

	service, err := installService(cfg)
	if err != nil {
		return err
	}
	cfg.Service = service
	if err := saveConfig(paths.Config, cfg); err != nil {
		return err
	}
	switch service {
	case "none":
		fmt.Fprintf(os.Stderr, "未检测到 systemd：已装好但不会随开机启动。现在前台运行：%s run --root %s\n",
			filepath.Join(paths.BinDir, "zrcin-node"), root)
	default:
		fmt.Printf("已安装：节点 %s（%s）；服务 zrcin-node（%s）已启动\n", cfg.Name, cfg.NodeID, service)
	}
	return nil
}

// installSelf copies the running launcher into the root, atomically. Running
// from that path already (repair, or the service) is a no-op.
func installSelf(dest string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位启动器：%w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	if filepath.Clean(exe) == filepath.Clean(dest) {
		return nil
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		return fmt.Errorf("读取启动器 %s：%w", exe, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".zrcin-node-*.tmp")
	if err != nil {
		return fmt.Errorf("写启动器：%w", err)
	}
	name := tmp.Name()
	defer os.Remove(name) //nolint:errcheck
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("写启动器：%w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("写启动器：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写启动器：%w", err)
	}
	if err := os.Rename(name, dest); err != nil {
		return fmt.Errorf("安装启动器：%w", err)
	}
	return nil
}

// existingOrJoin keeps a working credential across reinstall (repair) and
// joins otherwise.
func existingOrJoin(ctx context.Context, base, token, installID, root string) (*Config, error) {
	rootPath := pathsFor(root).Config
	if cfg, err := loadConfig(rootPath); err == nil {
		cfg.Root = root
		cfg.InstallID = installID
		fmt.Fprintf(os.Stderr, "修复安装：沿用节点 %s 的既有凭据\n", cfg.NodeID)
		return cfg, nil
	}
	resp, err := joinPlatform(ctx, base, token, installID)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Version:     configVersion,
		NodeID:      resp.NodeID,
		Name:        resp.NodeID, // the scan's --node-name; operators see the enrollment name
		PublicURL:   resp.PublicURL,
		TunnelKey:   resp.Tunnel.Key,
		TunnelPath:  resp.Tunnel.Path,
		HubAddr:     resp.Hub.Addr,
		HubKey:      resp.Hub.Key,
		ConsoleAddr: resp.ConsoleAddr,
		DataDir:     resp.DataDir,
		Credential:  resp.NodeCredential,
		InstallID:   installID,
		Root:        root,
	}
	return cfg, nil
}

// splitJoinURL splits "<base>/join/<token>" into its base and token.
func splitJoinURL(joinURL string) (base, token string, err error) {
	u, err := url.Parse(strings.TrimSpace(joinURL))
	if err != nil {
		return "", "", fmt.Errorf("接入地址无法解析：%w", err)
	}
	idx := strings.LastIndex(u.Path, "/join/")
	if idx < 0 {
		return "", "", errors.New("接入地址必须形如 <平台地址>/join/<接入令牌>")
	}
	token = strings.Trim(strings.TrimPrefix(u.Path[idx:], "/join/"), "/")
	if token == "" || strings.Contains(token, "/") {
		return "", "", errors.New("接入地址缺少令牌")
	}
	u.Path = u.Path[:idx]
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), token, nil
}

// joinPlatform exchanges the enrollment token for the node credential.
func joinPlatform(ctx context.Context, base, token, installID string) (enroll.JoinResponse, error) {
	body, _ := json.Marshal(enroll.JoinRequest{InstallID: installID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/node/join", bytes.NewReader(body))
	if err != nil {
		return enroll.JoinResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return enroll.JoinResponse{}, fmt.Errorf("连接平台失败：%w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		return enroll.JoinResponse{}, fmt.Errorf("接入被拒（409）：%s", errorText(raw))
	case http.StatusGone:
		return enroll.JoinResponse{}, fmt.Errorf("接入令牌已作废或过期（410）：%s", errorText(raw))
	default:
		return enroll.JoinResponse{}, fmt.Errorf("接入失败（%d）：%s", resp.StatusCode, errorText(raw))
	}
	var out enroll.JoinResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return enroll.JoinResponse{}, fmt.Errorf("接入响应无法解析：%w", err)
	}
	return out, nil
}

func errorText(raw []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error != "" {
		return body.Error
	}
	return strings.TrimSpace(string(raw))
}

// downloadArtifact fetches one artifact and verifies the platform's digest
// before it replaces anything on disk.
func downloadArtifact(ctx context.Context, base, credential, name, dest string) error {
	// A file that already is the platform's artifact is not downloaded again:
	// a repair install (an upgrade is one) then costs one HEAD per unchanged
	// file instead of its whole payload. Any doubt about the answer — no
	// local file, a platform that does not answer the HEAD — falls through
	// to the download, which is always right.
	if local, err := fileDigest(dest); err == nil {
		if current, herr := artifactIsCurrent(ctx, base, credential, name, local); herr == nil && current {
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/node/dist/"+name, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载 %s 失败：%w", name, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("下载 %s 失败（%d）：%s", name, resp.StatusCode, errorText(raw))
	}
	want := strings.TrimSpace(resp.Header.Get("X-Checksum-Sha256"))
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+name+"-*.tmp")
	if err != nil {
		return fmt.Errorf("写 %s：%w", name, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("下载 %s：%w", name, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("写 %s：%w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写 %s：%w", name, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); want != "" && got != want {
		return fmt.Errorf("%s 校验失败：平台摘要 %s，下载摘要 %s；产物可能正在更新，重跑一次", name, want, got)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("安装 %s：%w", name, err)
	}
	return nil
}

// artifactIsCurrent asks the platform for the artifact's digest and says
// whether the local file already has it. A HEAD carries no body, so nothing
// but the answer crosses the tunnel.
func artifactIsCurrent(ctx context.Context, base, credential, name, localSum string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, base+"/node/dist/"+name, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("查询 %s 摘要：%w", name, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("查询 %s 摘要：平台响应 %d", name, resp.StatusCode)
	}
	return strings.TrimSpace(resp.Header.Get("X-Checksum-Sha256")) == localSum, nil
}

// fileDigest is the SHA-256 of the file at path, streamed.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// installService registers the launcher with systemd. Without systemd the
// install still completes and says plainly that nothing starts it at boot.
func installService(cfg *Config) (string, error) {
	if os.Getenv("ZRCIN_NODE_NO_SERVICE") == "1" {
		return "none", nil
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return "none", nil
	}
	launcher := filepath.Join(cfg.Root, "bin", "zrcin-node")
	unit := fmt.Sprintf(`[Unit]
Description=4zrcin node launcher
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --root %s
Restart=always
RestartSec=5
KillMode=mixed

[Install]
%s
`, launcher, cfg.Root, wantedBy())
	if os.Geteuid() == 0 {
		path := filepath.Join("/etc/systemd/system", serviceName+".service")
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
			return "", fmt.Errorf("写 systemd 单元 %s：%w", path, err)
		}
		if err := ensureService(false); err != nil {
			return "", err
		}
		return "system", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, serviceName+".service")
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return "", fmt.Errorf("写 systemd 用户单元 %s：%w", path, err)
	}
	if err := ensureService(true); err != nil {
		return "", err
	}
	return "user", nil
}

// serviceCtl is how the installer drives systemd; tests replace it.
var serviceCtl = systemctl

// ensureService enables and starts the launcher, and makes sure what runs is
// the install just written: a service that was already running keeps its old
// binaries otherwise, so a repair install (an upgrade is one) restarts it.
// A service that was not running is only started.
func ensureService(user bool) error {
	base := []string{"systemctl"}
	if user {
		base = append(base, "--user")
	}
	running := serviceCtl(append(append([]string{}, base...), "is-active", "--quiet", serviceName)...) == nil
	if err := serviceCtl(append(append([]string{}, base...), "enable", "--now", serviceName)...); err != nil {
		return err
	}
	if !running {
		return nil
	}
	return serviceCtl(append(append([]string{}, base...), "restart", serviceName)...)
}

func wantedBy() string {
	if os.Geteuid() == 0 {
		return "WantedBy=multi-user.target"
	}
	return "WantedBy=default.target"
}

func systemctl(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s 失败：%v（%s）", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// precheckDisk is a platform-specific space check; the launcher asks for the
// room the downloads need plus a margin.
var precheckDisk = func(*Config) error { return nil }
