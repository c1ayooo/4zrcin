package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/noderes"
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
	// The mirror ports are this node's own: re-check them at every run, and
	// pick again if something on the host took one while the node was down.
	if changed, cerr := ensureMirrors(cfg); cerr != nil {
		return cerr
	} else if changed {
		if serr := saveConfig(pathsFor(root).Config, cfg); serr != nil {
			return serr
		}
		log.Printf("回环镜像口已改用：Hub %s，控制台 %s", cfg.MirrorHub, cfg.MirrorConsole)
	}
	// Every return path (uninstall included) ends this context, which is what
	// kills the supervised scan: a launcher that exits without it leaves the
	// scan running on a host nobody manages any more.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	state := newStateWriter(cfg)
	state.markStarted()
	revoked := make(chan struct{})
	var once sync.Once
	markRevoked := func() { once.Do(func() { close(revoked) }) }

	log.Printf("节点 %s（%s）启动：隧道 %s，scan %s", cfg.Name, cfg.NodeID, cfg.PublicURL, cfg.ScanBin)

	proxies := newScanProxy()
	first := seedScanProxy(ctx, cfg, proxies)

	go superviseScan(ctx, cfg, state, proxies)
	// wake carries the tunnel loop's "state changed, report now" nudges to the
	// heartbeat; runNode owns it because the tunnel loop runs here too.
	heartbeatWake := make(chan struct{}, 1)
	go heartbeat(ctx, cfg, state, markRevoked, heartbeatWake, proxies)

	dirErr := make(chan error, 1)
	go func() { dirErr <- directiveLoop(ctx, cfg, proxies, first) }()
	tunnelErr := make(chan error, 1)
	go func() { tunnelErr <- tunnelLoop(ctx, cfg, state, markRevoked, heartbeatWake) }()

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
func tunnelLoop(ctx context.Context, cfg *Config, state *stateWriter, markRevoked func(), wake chan<- struct{}) error {
	nudge := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	for ctx.Err() == nil {
		err := openTunnel(ctx, cfg, func() {
			state.setTunnel("up")
			log.Printf("隧道已连接；本机回环镜像 %s（Hub → %s）与 %s（控制台 → %s）", cfg.MirrorHub, cfg.HubAddr, cfg.MirrorConsole, cfg.ConsoleAddr)
			// The console's offline window counts heartbeats: report the flip
			// now instead of up to a beat later.
			nudge()
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
			nudge()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tunnelReconnectDelay):
		}
	}
	return nil
}

// seedScanProxy fetches the node's egress default before scan starts, so a
// boot does not begin with a direct scan only to restart it a moment later.
// The answer is returned for the directive loop: its directives must not be
// consumed behind the loop's back. An unreachable platform is not fatal —
// scan starts direct and the loop aligns it once the platform answers.
func seedScanProxy(ctx context.Context, cfg *Config, proxies *scanProxy) *nextResponse {
	res, err := pollDirectives(ctx, cfg, 0)
	if err != nil {
		log.Printf("启动时未能取到节点配置（%v）：scan 先按直连启动，连通后对齐", err)
		return nil
	}
	proxies.offer(res.ScanProxy, false)
	return &res
}

// scanArgs is the scan node's command line (docs/14 §7.2). It mirrors the
// local host's launch in 4zrcin: the web transport dials the Hub through the
// tunnel mirror, and the evidence switch ships evidence and the
// handoff files back (docs/14 §8). The enrollment recipe rides the same line
// since docs/19 P6: a capture enrollment adds --capture-only — one cyber-scan
// binary, the recipe decided at launch, not at link time.
func scanArgs(cfg *Config) []string {
	serverURL := (&url.URL{
		Scheme: "http",
		Host:   cfg.MirrorHub,
		User:   url.User(cfg.HubKey),
	}).String()
	args := []string{
		"agent",
		"--transport", "web",
		"--server-url", serverURL,
		"--node-id", cfg.NodeID,
		"--node-name", cfg.Name,
		"--observe", "http",
		"--evidence",
		"--timeout", "0",
	}
	if cfg.Recipe == enroll.RecipeCapture {
		args = append(args, "--capture-only")
	}
	return args
}

// scanTermGrace is how long a scan told to stop for a proxy change may take
// before it is killed: a deliberate stop is a request, not a guarantee.
const scanTermGrace = 10 * time.Second

// superviseScan keeps the scan process running: a crash is restarted with a
// backoff that resets after a healthy minute, and the process dies with ctx.
//
// A change of the node's egress default (scanProxy) restarts scan the same
// way, but on purpose: the stop is a TERM, the restart is immediate, and it is
// not a crash — the health rule reads restarts as "the scan keeps dying".
func superviseScan(ctx context.Context, cfg *Config, state *stateWriter, proxies *scanProxy) {
	backoff := scanRestartMin
	for ctx.Err() == nil {
		proxies.takeRestart()
		cmd := exec.CommandContext(ctx, cfg.ScanBin, scanArgs(cfg)...)
		cmd.Dir = cfg.DataDir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), proxies.env()...)
		if err := cmd.Start(); err != nil {
			log.Printf("scan 启动失败：%v；%s 后重试", err, backoff)
		} else {
			state.setScan(cmd.Process.Pid)
			proxies.launched()
			startedAt := time.Now()
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			var err error
			planned := false
			select {
			case <-ctx.Done():
				err = <-exited
			case err = <-exited:
			case <-proxies.due():
				log.Printf("出网代理已变更：重启 scan")
				_ = terminateProcess(cmd)
				select {
				case err = <-exited:
				case <-time.After(scanTermGrace):
					_ = cmd.Process.Kill()
					err = <-exited
				}
				planned = true
			}
			if ctx.Err() != nil {
				return
			}
			if planned {
				backoff = scanRestartMin
				continue
			}
			if time.Since(startedAt) > scanRestartMax {
				backoff = scanRestartMin
			}
			log.Printf("scan 退出（%v）；%s 后重启", err, backoff)
			state.bumpScanRestarts()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		case <-proxies.due():
			// The default moved while nothing ran: the next start carries it.
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
// (docs/14 §7): host facts, the CLI path the sessions put in $ZRCIN_BIN, and
// the tunnel/scan status the console shows. A revoked credential ends the
// node.
//
// Failures back off (30s doubling to 5m, ±20% jitter) so a long platform
// outage does not turn into a log firehose, and the tunnel loop can ask for
// an immediate report on a state change: the platform's offline window is
// three beats wide, and a flap should be visible on the next beat, not the
// next tick.
func heartbeat(ctx context.Context, cfg *Config, state *stateWriter, markRevoked func(), wake chan struct{}, proxies *scanProxy) {
	// nudge marks one immediate report due. Coalesced by the channel buffer: a
	// flap storm sends one report carrying the latest state, which is the only
	// state that matters.
	nudge := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go func() {
		for ctx.Err() == nil {
			prev, ok := state.snapshot()
			if !ok {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			for ctx.Err() == nil {
				time.Sleep(200 * time.Millisecond)
				cur, ok := state.snapshot()
				if !ok {
					continue
				}
				if cur != prev {
					nudge()
					return
				}
			}
		}
	}()

	beat := 0
	sampler := noderes.NewSampler(cfg.DataDir, scanPIDOf(state))
	send := func() error {
		hostname, _ := os.Hostname()
		st, ok := state.snapshot()
		tunnelUp := ok && st.Tunnel == "up"
		sample := sampler.Sample(600 * time.Millisecond)
		return postReport(ctx, cfg, enroll.Report{
			Hostname:          hostname,
			OS:                runtime.GOOS,
			Arch:              runtime.GOARCH,
			NodeBinPath:       cfg.CLIBin,
			TunnelUp:          &tunnelUp,
			Resources:         &sample,
			LauncherVersion:   buildVersion,
			UptimeSeconds:     int64(time.Since(state.startedAt()).Seconds()),
			ScanRestarts:      state.restartCount(),
			MirrorHubPort:     portNumber(cfg.MirrorHub),
			MirrorConsolePort: portNumber(cfg.MirrorConsole),
			// What the platform needs to know whether this node's egress is
			// the default it hands out, never the proxy itself.
			ScanProxyDigest: proxies.digest(),
		})
	}
	fail := func(what string, err error) {
		if errors.Is(err, errRevoked) {
			markRevoked()
			return
		}
		if beat >= 3 {
			delay := heartbeatBackoff(beat)
			log.Printf("%s失败（连续 %d 次）：%v；%s 后重试", what, beat, err, delay)
		} else {
			log.Printf("%s失败：%v", what, err)
		}
	}
	if err := send(); err != nil {
		beat++
		fail("首次心跳", err)
	} else {
		beat = 0
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			if err := send(); err != nil {
				beat++
				fail("状态上报", err)
			} else {
				beat = 0
			}
		case <-ticker.C:
			if err := send(); err != nil {
				beat++
				// While the platform is unreachable a failing beat waits out a
				// longer slice of the same ticker instead of stacking retries.
				if beat >= 3 {
					if d := heartbeatBackoff(beat); d > 30*time.Second {
						timer := time.NewTimer(d - 30*time.Second)
						select {
						case <-ctx.Done():
							timer.Stop()
							return
						case <-timer.C:
						}
					}
				}
				fail("心跳", err)
			} else {
				beat = 0
			}
		}
	}
}

// heartbeatBackoff is the delay before the next attempt after beat-th
// consecutive failures: 30s doubling to 5m with ±20% jitter, so a fleet of
// nodes that lost the platform does not retry in lockstep when it returns.
func heartbeatBackoff(beat int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < beat && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	jitter := time.Duration(rand.Int63n(int64(d) / 5))
	return d - jitter/2 + jitter
}
