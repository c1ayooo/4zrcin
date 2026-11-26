package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// tunnelRunner opens the tunnel to the platform and blocks until it drops;
// onReady fires once the node's two loopback mirrors accept connections. The
// implementation lives behind the remtunnel build tag (tunnel_rem.go); a
// default build leaves it nil and run says so.
type tunnelRunner func(ctx context.Context, cfg *Config, onReady func()) error

var openTunnel tunnelRunner

// errRevoked is the platform telling this node its credential is gone: run
// uninstalls instead of retrying forever (docs/14 §7.3).
var errRevoked = errors.New("节点凭据已被吊销")

const (
	// scanRestartMin/Max bound the scan supervisor's backoff.
	scanRestartMin = time.Second
	scanRestartMax = time.Minute
	// tunnelReconnectDelay is how long the tunnel loop waits after a drop
	// before opening a fresh one (with a fresh ticket).
	tunnelReconnectDelay = 5 * time.Second
)

// runNode is the service's main loop (docs/14 §7.2): a tunnel, a supervised
// scan and the platform's directive channel run together. A revoked
// credential or an uninstall directive ends in a detached uninstall; anything
// else keeps retrying.
func runNode(ctx context.Context, rootFlag string) error {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(pathsFor(root).Config)
	if err != nil {
		return err
	}
	if openTunnel == nil {
		return errors.New("本次构建未编入隧道支持：用 -tags remtunnel 重新构建 zrcin-node")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录 %s：%w", cfg.DataDir, err)
	}
	// Every return path (uninstall included) ends this context, which is what
	// kills the supervised scan: a launcher that exits without it leaves the
	// scan running on a host nobody manages any more.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	state := newStateWriter(cfg)
	revoked := make(chan struct{})
	var once sync.Once
	markRevoked := func() { once.Do(func() { close(revoked) }) }

	log.Printf("节点 %s（%s）启动：隧道 %s，scan %s", cfg.Name, cfg.NodeID, cfg.PublicURL, cfg.ScanBin)

	go superviseScan(ctx, cfg, state)
	go heartbeat(ctx, cfg, markRevoked)

	dirErr := make(chan error, 1)
	go func() { dirErr <- directiveLoop(ctx, cfg) }()
	tunnelErr := make(chan error, 1)
	go func() { tunnelErr <- tunnelLoop(ctx, cfg, state, markRevoked) }()

	select {
	case <-ctx.Done():
		state.setTunnel("down")
		return nil
	case <-revoked:
		log.Printf("节点凭据已被吊销：执行卸载")
		reportUninstalled(cfg)
		return selfUninstall(cfg)
	case err := <-dirErr:
		if errors.Is(err, errUninstallRequested) || errors.Is(err, errRevoked) {
			log.Printf("平台要求卸载：执行卸载")
			reportUninstalled(cfg)
			return selfUninstall(cfg)
		}
		return err
	case err := <-tunnelErr:
		return err
	}
}

// tunnelLoop keeps one tunnel open, re-opened with a fresh ticket whenever it
// drops. Revocation is reported to the caller, not retried.
func tunnelLoop(ctx context.Context, cfg *Config, state *stateWriter, markRevoked func()) error {
	for ctx.Err() == nil {
		err := openTunnel(ctx, cfg, func() {
			state.setTunnel("up")
			log.Printf("隧道已连接；本机回环镜像 %s（Hub）与 %s（控制台）", cfg.HubAddr, cfg.ConsoleAddr)
		})
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, errRevoked):
			markRevoked()
			return nil
		case err != nil:
			state.setTunnel("down")
			log.Printf("隧道断开：%v；%s 后重连", err, tunnelReconnectDelay)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tunnelReconnectDelay):
		}
	}
	return nil
}

// scanArgs is the scan node's command line (docs/14 §7.2). It mirrors the
// local host's launch in 4zrcin: the web transport dials the Hub through the
// tunnel mirror, and the artifacts observe kind ships evidence and the
// handoff files back (docs/14 §8).
func scanArgs(cfg *Config) []string {
	serverURL := (&url.URL{
		Scheme: "http",
		Host:   cfg.HubAddr,
		User:   url.User(cfg.HubKey),
	}).String()
	return []string{
		"agent",
		"--transport", "web",
		"--server-url", serverURL,
		"--node-id", cfg.NodeID,
		"--node-name", cfg.Name,
		"--observe", "http,artifacts",
		"--timeout", "0",
	}
}

// superviseScan keeps the scan process running: a crash is restarted with a
// backoff that resets after a healthy minute, and the process dies with ctx.
func superviseScan(ctx context.Context, cfg *Config, state *stateWriter) {
	backoff := scanRestartMin
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, cfg.ScanBin, scanArgs(cfg)...)
		cmd.Dir = cfg.DataDir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("scan 启动失败：%v；%s 后重试", err, backoff)
		} else {
			state.setScan(cmd.Process.Pid)
			started := time.Now()
			err := cmd.Wait()
			if ctx.Err() != nil {
				return
			}
			if time.Since(started) > scanRestartMax {
				backoff = scanRestartMin
			}
			log.Printf("scan 退出（%v）；%s 后重启", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < scanRestartMax {
			backoff *= 2
			if backoff > scanRestartMax {
				backoff = scanRestartMax
			}
		}
	}
}

// heartbeat is the liveness signal the console's online window reads
// (docs/14 §7): host facts plus the CLI path the sessions put in
// $ZRCIN_BIN. A revoked credential ends the node.
func heartbeat(ctx context.Context, cfg *Config, markRevoked func()) {
	send := func() error {
		hostname, _ := os.Hostname()
		return postReport(ctx, cfg, enroll.Report{
			Hostname:    hostname,
			OS:          runtime.GOOS,
			Arch:        runtime.GOARCH,
			NodeBinPath: cfg.CLIBin,
		})
	}
	if err := send(); err != nil {
		if errors.Is(err, errRevoked) {
			markRevoked()
			return
		}
		log.Printf("首次心跳失败：%v", err)
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				if errors.Is(err, errRevoked) {
					markRevoked()
					return
				}
				log.Printf("心跳失败：%v", err)
			}
		}
	}
}
