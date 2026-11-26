package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// errUninstallRequested is the platform telling this launcher to remove
// itself (docs/14 §7.3). It is not an error to report: the caller runs the
// detached uninstall and exits.
var errUninstallRequested = errors.New("平台要求卸载")

// directive is one platform instruction (docs/14 §7.2). The sequence number is
// persisted so a redelivered directive is a no-op.
type directive struct {
	Seq   uint64            `json:"seq"`
	Op    string            `json:"op"`
	Shell *enroll.ShellTask `json:"shell,omitempty"`
}

type nextResponse struct {
	State      json.RawMessage `json:"state"`
	Directives []directive     `json:"directives"`
}

// directiveLoop is the launcher's control channel: the long-poll to
// /node/next, the persisted sequence, and the instruction switch. It returns
// nil with ctx, errUninstallRequested on uninstall, and errRevoked when the
// credential no longer exists.
func directiveLoop(ctx context.Context, cfg *Config) error {
	last := loadDirectiveSeq(cfg)
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		res, err := pollDirectives(ctx, cfg)
		if err != nil {
			if errors.Is(err, errRevoked) || errors.Is(err, errUninstallRequested) {
				return err
			}
			log.Printf("指令通道失败：%v；%s 后重试", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = 5 * time.Second
		for _, d := range res.Directives {
			if d.Seq <= last {
				continue // already executed before a restart
			}
			last = d.Seq
			if err := saveDirectiveSeq(cfg, last); err != nil {
				log.Printf("记录指令序号失败：%v", err)
			}
			switch d.Op {
			case "uninstall":
				return errUninstallRequested
			case enroll.OpShell:
				if d.Shell == nil {
					log.Printf("shell 指令缺少载荷（序号 %d），忽略", d.Seq)
					continue
				}
				startShellTask(ctx, cfg, *d.Shell)
			default:
				log.Printf("未知指令 %q（序号 %d），忽略", d.Op, d.Seq)
			}
		}
	}
	return nil
}

// pollDirectives asks for instructions with the documented 30 s wait. A 401 or
// 410 means the node's credential is gone.
func pollDirectives(ctx context.Context, cfg *Config) (nextResponse, error) {
	pollCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	url := strings.TrimRight(cfg.PublicURL, "/") + "/node/next?wait=30"
	req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, url, nil)
	if err != nil {
		return nextResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Credential)
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nextResponse{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusGone, http.StatusUnauthorized:
		return nextResponse{}, errRevoked
	default:
		return nextResponse{}, fmt.Errorf("平台响应 %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out nextResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nextResponse{}, fmt.Errorf("指令响应无法解析：%w", err)
	}
	return out, nil
}

// seqPath is where the executed directive sequence persists.
func seqPath(cfg *Config) string {
	return filepath.Join(cfg.Root, "var", "directives-seq")
}

func loadDirectiveSeq(cfg *Config) uint64 {
	raw, err := os.ReadFile(seqPath(cfg))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func saveDirectiveSeq(cfg *Config, seq uint64) error {
	if err := os.MkdirAll(filepath.Dir(seqPath(cfg)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(seqPath(cfg), []byte(strconv.FormatUint(seq, 10)+"\n"), 0o600)
}

// selfUninstall runs the uninstaller detached from this process. Under
// systemd it goes through a transient unit: the uninstaller stops the service
// (which kills this launcher and the scan), and a child in the service's own
// cgroup would die with it. Without systemd a detached session suffices.
func selfUninstall(cfg *Config) error {
	launcher := filepath.Join(cfg.Root, "bin", "zrcin-node")
	args := []string{"uninstall", "--root", cfg.Root}
	var cmd *exec.Cmd
	if _, err := os.Stat("/run/systemd/system"); err == nil && cfg.Service != "none" {
		scope := []string{"systemd-run", "--collect", "--no-block", "--unit", "zrcin-node-uninstall"}
		if cfg.Service == "user" {
			scope = []string{"systemd-run", "--user", "--collect", "--no-block", "--unit", "zrcin-node-uninstall"}
		}
		cmd = exec.Command(scope[0], append(scope[1:], append([]string{launcher}, args...)...)...)
	} else {
		cmd = exec.Command(launcher, args...)
		detachProcess(cmd)
	}
	cmd.Env = append(os.Environ(), "ZRCIN_NODE_DETACHED=1")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动卸载进程：%w", err)
	}
	return nil
}

// reportUninstalled is the best-effort receipt: the platform flips the node to
// destroyed on it, and a lost receipt leaves the node in "waiting" (the
// operator can mark it removed).
func reportUninstalled(cfg *Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = postReport(ctx, cfg, map[string]any{"event": "uninstalled"})
}
