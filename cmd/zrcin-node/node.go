package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runStatus prints what the operator needs to paste into a ticket: the node's
// identity, where it comes from and what starts it (docs/14 §7).
func runStatus(rootFlag string) error {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(pathsFor(root).Config)
	if err != nil {
		return err
	}
	fmt.Printf("节点：%s（%s）\n", cfg.Name, cfg.NodeID)
	fmt.Printf("接入：%s\n", cfg.PublicURL)
	fmt.Printf("主控镜像：Hub %s，控制台 %s\n", cfg.HubAddr, cfg.ConsoleAddr)
	fmt.Printf("数据目录：%s\n", cfg.DataDir)
	fmt.Printf("安装根：%s（服务：%s）\n", cfg.Root, describeService(cfg.Service))
	st := loadRunState(statePath(cfg))
	tunnel := st.Tunnel
	if tunnel == "" {
		tunnel = "未知（服务未运行过）"
	}
	scan := "未运行"
	if st.ScanPID > 0 {
		scan = fmt.Sprintf("PID %d", st.ScanPID)
	}
	fmt.Printf("隧道：%s；scan：%s；更新于 %s\n", tunnel, scan, st.Updated)
	fmt.Printf("凭据：已保存（不显示）\n")
	return nil
}

func describeService(service string) string {
	switch service {
	case "system":
		return "systemd 系统单元"
	case "user":
		return "systemd 用户单元"
	default:
		return "无（需手动运行 run）"
	}
}

// runUninstall removes what install created (docs/14 §7). It only deletes a
// directory that carries this node's ownership marker: a mirrored data path
// that already existed belongs to somebody else and is left alone.
//
// The launcher runs this as its own process from the platform's directive, so
// stopping the service here would kill the uninstaller itself; the directive
// path (L2) detaches it first. Called by hand, it stops the service itself.
func runUninstall(_ context.Context, rootFlag string) error {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(pathsFor(root).Config)
	if err != nil {
		return err
	}
	detached := os.Getenv("ZRCIN_NODE_DETACHED") == "1"
	if !detached {
		disableService(cfg)
	}
	removeUnitFiles()
	removedRoot := false
	if ownerOf(root) == cfg.NodeID {
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("删除安装根 %s：%w", root, err)
		}
		removedRoot = true
	}
	removedData := false
	if cfg.DataDir != "" && ownerOf(cfg.DataDir) == cfg.NodeID {
		if err := os.RemoveAll(cfg.DataDir); err != nil {
			return fmt.Errorf("删除镜像数据目录 %s：%w", cfg.DataDir, err)
		}
		removedData = true
	}
	fmt.Printf("已卸载：安装根 %s，镜像数据目录 %s\n", presence(removedRoot), presence(removedData))
	return nil
}

func presence(ok bool) string {
	if ok {
		return "已删除"
	}
	return "未删除（归属标记不匹配）"
}

// disableService stops and disables the unit, best effort: an uninstall must
// not stop because systemd is already gone.
func disableService(cfg *Config) {
	switch cfg.Service {
	case "system":
		_ = systemctl("systemctl", "disable", "--now", serviceName)
	case "user":
		_ = systemctl("systemctl", "--user", "disable", "--now", serviceName)
	}
}

// removeUnitFiles deletes the unit file from both scopes; a missing file is
// not an error.
func removeUnitFiles() {
	if home, err := os.UserHomeDir(); err == nil {
		_ = os.Remove(filepath.Join(home, ".config", "systemd", "user", serviceName+".service"))
	}
	_ = os.Remove(filepath.Join("/etc/systemd/system", serviceName+".service"))
}

// fetchTicket asks the platform for a one-time tunnel ticket. A 410 means the
// credential is gone (destroyed), which the run loop treats as final.
func fetchTicket(ctx context.Context, cfg *Config) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.PublicURL, "/")+"/node/ticket", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Credential)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("取隧道票据失败：%w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusGone:
		return "", errRevoked
	default:
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &body)
		return "", fmt.Errorf("取隧道票据失败（%d）：%s", resp.StatusCode, strings.TrimSpace(body.Error))
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.URL == "" {
		return "", errors.New("平台返回的票据没有拨号地址")
	}
	return body.URL, nil
}

// postReport sends one heartbeat/report to the platform (docs/14 §7.2).
func postReport(ctx context.Context, cfg *Config, report any) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.PublicURL, "/")+"/node/report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Credential)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()                               //nolint:errcheck
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck
	if resp.StatusCode == http.StatusGone {
		return errRevoked
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上报失败（%d）", resp.StatusCode)
	}
	return nil
}
