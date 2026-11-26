//go:build remtunnel

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	_ "github.com/chainreactors/rem/protocol/serve/portforward"
	_ "github.com/chainreactors/rem/protocol/serve/raw"
	_ "github.com/chainreactors/rem/protocol/tunnel/websocket"
	_ "github.com/chainreactors/rem/protocol/wrapper"
	remrunner "github.com/chainreactors/rem/runner"
)

func init() { openTunnel = remTunnel }

// remTunnel is the node end of docs/14 §7.2: one ticket per handshake, the
// two required loopback mirrors opened locally, and the platform dialing the
// Hub and the console API through them. The connection is long-lived; the run
// loop re-opens it with a fresh ticket when it drops.
func remTunnel(ctx context.Context, cfg *Config, onReady func()) error {
	dialURL, err := fetchTicket(ctx, cfg)
	if err != nil {
		return err
	}
	_, hubPort, okHub := hostPortSplit(cfg.HubAddr)
	_, conPort, okCon := hostPortSplit(cfg.ConsoleAddr)
	if !okHub || !okCon {
		return fmt.Errorf("配置里的回环地址不合法：Hub %q，控制台 %q", cfg.HubAddr, cfg.ConsoleAddr)
	}
	cmdline := fmt.Sprintf(
		"-c %s -k %s -q -a %s -l port://127.0.0.1:%s/%s -l port://127.0.0.1:%s/%s",
		dialURL, cfg.TunnelKey, cfg.NodeID,
		hubPort, cfg.HubAddr,
		conPort, cfg.ConsoleAddr,
	)
	con, err := remrunner.NewConsoleWithCMD(cmdline)
	if err != nil {
		return fmt.Errorf("隧道客户端：%w", err)
	}
	defer con.Close() //nolint:errcheck

	node, err := con.Dial(con.ConsoleURL)
	if err != nil {
		return fmt.Errorf("隧道握手失败：%w", err)
	}
	go con.Handler(node)

	// Ready once the Hub mirror accepts: the scan dials it next.
	ready := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if conn, derr := net.DialTimeout("tcp", "127.0.0.1:"+hubPort, 200*time.Millisecond); derr == nil {
			conn.Close() //nolint:errcheck
			ready = true
			break
		}
		if node.IsClosed() {
			return errors.New("隧道在就绪前断开")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return errors.New("隧道 15 秒内没有建立回环转发")
	}
	if onReady != nil {
		onReady()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if node.IsClosed() {
				return errors.New("隧道已断开")
			}
		}
	}
}
