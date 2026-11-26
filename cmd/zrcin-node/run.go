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
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// tunnelRunner opens the tunnel to the platform and blocks until it drops;
// onReady fires once the node's two loopback mirrors accept connections. The
// implementation lives behind the remtunnel build tag (tunnel_rem.go); a
// default build leaves it nil and run says so.
type tunnelRunner func(ctx context.Context, cfg *Config, onReady func()) error

var openTunnel tunnelRunner

// errRevoked is the platform telling this node its credential is gone: the
// run loop stops retrying (the uninstall path hooks it in the next ticket).
var errRevoked = errors.New("节点凭据已被吊销")

const (
	// scanRestartMin/Max bound the scan supervisor's backoff.
	scanRestartMin = time.Second
	scanRestartMax = time.Minute
	// tunnelReconnectDelay is how long the run loop waits after a dropped
	// tunnel before opening a fresh one (with a fresh ticket).
	tunnelReconnectDelay = 5 * time.Second
)

// runNode is the service's main loop (docs/15 §4.3): keep a tunnel open and
// keep the scan process alive underneath it. Both restart on their own; a
// revoked credential stops the loop for good.
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
	log.Printf("节点 %s（%s）启动：隧道 %s，scan %s", cfg.Name, cfg.NodeID, cfg.PublicURL, cfg.ScanBin)

	go superviseScan(ctx, cfg)
	go heartbeat(ctx, cfg)

	for ctx.Err() == nil {
		err := openTunnel(ctx, cfg, func() {
			log.Printf("隧道已连接；本机回环镜像 %s（Hub）与 %s（控制台）", cfg.HubAddr, cfg.ConsoleAddr)
		})
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, errRevoked):
			return err
		case err != nil:
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

// scanArgs is the scan node's command line (docs/15 §4.3). It mirrors the
// local host's launch in 4zrcin: the web transport dials the Hub through the
// tunnel mirror, and the artifacts observe kind ships evidence and the
// handoff files back (docs/15 §8).
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

// heartbeat is the liveness signal the console's online window reads
// (docs/15 §3.2): host facts plus the CLI path the sessions put in
// $ZRCIN_BIN. A revoked credential stops the whole node.
func heartbeat(ctx context.Context, cfg *Config) {
	send := func() error {
		hostname, _ := os.Hostname()
		return postReport(ctx, cfg, enrollReport{
			Hostname:    hostname,
			OS:          runtime.GOOS,
			Arch:        runtime.GOARCH,
			NodeBinPath: cfg.CLIBin,
		})
	}
	if err := send(); err != nil {
		if errors.Is(err, errRevoked) {
			log.Printf("节点凭据已被吊销：停止运行")
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
					log.Printf("节点凭据已被吊销：停止运行")
					return
				}
				log.Printf("心跳失败：%v", err)
			}
		}
	}
}

// enrollReport is the platform's report shape; the launcher consumes the type
// from the same package so the two sides cannot drift.
type enrollReport = enroll.Report

// superviseScan keeps the scan process running: a crash is restarted with a
// backoff that resets after a healthy minute, and the process dies with ctx.
func superviseScan(ctx context.Context, cfg *Config) {
	backoff := scanRestartMin
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, cfg.ScanBin, scanArgs(cfg)...)
		cmd.Dir = cfg.DataDir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		started := time.Now()
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > scanRestartMax {
			backoff = scanRestartMin
		}
		log.Printf("scan 退出（%v）；%s 后重启", err, backoff)
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
