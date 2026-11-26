//go:build remtunnel

// Package relay keeps the platform's reverse connection to a public rem
// server (docs/14 §7.5). A platform on an operator machine has no public
// address; the public host runs a rem server, and the platform dials out as a
// rem client with a reverse serve — `-r port://LISTEN/DEST` — which makes the
// public host open LISTEN on its own loopback and carry every connection back
// through the tunnel to DEST, the platform's console.
//
// The node side does not change: nodes dial the public entry, the entry's
// reverse proxy hands the connection to the relay's listener, and the tunnel
// brings it home. The platform never needs an inbound port.
package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/chainreactors/rem/protocol/tunnel/websocket"
	_ "github.com/chainreactors/rem/protocol/wrapper"
	remrunner "github.com/chainreactors/rem/runner"
)

// Config is one platform's reverse connection.
type Config struct {
	// Server is the rem server's WebSocket URL on the public host, as the
	// platform reaches it: wss://host/relay (TLS in front, verified against
	// the system roots) or ws:// for a lab.
	Server string
	// Key is the shared login key (at most 31 chars: rem pads it to an AES
	// key).
	Key string
	// Serves are the reverse forwards: each opens Listen on the public
	// host's loopback and carries its connections through the tunnel to Dest
	// on the platform. Two are needed (docs/14 §7.5): the node tunnel's
	// handshake lands on the platform's node-tunnel server listener, and the
	// console HTTP on the console itself.
	Serves []Serve
	// Console is kept for callers that only expose the console.
	Console string
	// ProbeURL is the public entry as nodes see it. The relay asks its
	// /healthz to learn whether the whole chain carries traffic; empty leaves
	// the state unknown.
	ProbeURL string
	// Logf receives the state transitions (nil: silent).
	Logf func(format string, args ...any)
}

// Serve is one reverse forward: public Listen → platform Dest.
type Serve struct {
	Listen string
	Dest   string
}

// State is what the console shows about the reverse connection.
type State struct {
	// Up means the public entry answered /healthz through the tunnel.
	Up bool `json:"up"`
	// Since is when the chain was last seen up (zero when never).
	Since time.Time `json:"since,omitempty"`
	// Detail is the last probe failure, for the operator.
	Detail string `json:"detail,omitempty"`
}

const (
	defaultRelayListen  = "127.0.0.1:18081"
	defaultRelayConsole = "127.0.0.1:8080"
	relayAlias          = "platform"
	// probeInterval is how often the public entry is asked for the chain's
	// state; it is one small request through the operator's own tunnel.
	probeInterval = 10 * time.Second
)

// Relay is the prepared reverse connection. Prepare builds rem's console and
// assigns rem's process-global logger, so every rem console in the process
// (this one and the node tunnel's server) must be prepared before any of them
// runs; Run keeps the connection open.
type Relay struct {
	cfg    Config
	con    *remrunner.Console
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.Mutex
	state State
}

// Prepare validates the configuration and builds the rem console without
// running it.
func Prepare(cfg Config) (*Relay, error) {
	server := strings.TrimRight(strings.TrimSpace(cfg.Server), "/")
	if server == "" {
		return nil, errors.New("反向连接缺少公网 rem 服务地址")
	}
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return nil, fmt.Errorf("反向连接地址 %q 不合法：需要 ws:// 或 wss:// 的 rem 服务地址", cfg.Server)
	}
	key := strings.TrimSpace(cfg.Key)
	if key == "" {
		return nil, errors.New("反向连接缺少密钥：公网 rem 服务与平台必须共用一把密钥")
	}
	if len(key) > 31 {
		return nil, fmt.Errorf("反向连接密钥长度 %d 超过 31：rem 会把它填充成非法 AES 密钥", len(key))
	}
	serves := append([]Serve(nil), cfg.Serves...)
	if strings.TrimSpace(cfg.Console) != "" && len(serves) == 0 {
		serves = append(serves, Serve{Listen: defaultRelayListen, Dest: strings.TrimSpace(cfg.Console)})
	}
	if len(serves) == 0 {
		return nil, errors.New("反向连接缺少转发：至少一条 监听 → 目标")
	}
	for i := range serves {
		serves[i].Listen = strings.TrimSpace(serves[i].Listen)
		serves[i].Dest = strings.TrimSpace(serves[i].Dest)
		if serves[i].Listen == "" || serves[i].Dest == "" ||
			strings.ContainsAny(serves[i].Listen, "/?") || strings.ContainsAny(serves[i].Dest, "/?") {
			return nil, fmt.Errorf("反向连接第 %d 条转发的监听/目标不合法：只写 host:port", i+1)
		}
	}

	// Both ends pin the wrapper so the handshake needs no link copied off the
	// server; the wss scheme is what makes the dialer speak TLS, verified
	// against the system roots. The reconnect policy rides the URL like rem's
	// CLI does: never give up (the platform outlives any relay restart), 5s
	// to 60s backoff.
	dial, _ := url.Parse(server)
	q := dial.Query()
	if q.Get("wrapper") == "" {
		q.Set("wrapper", "raw")
	}
	q.Set("retry", "0")
	q.Set("retry-interval", "5")
	q.Set("retry-max-interval", "60")
	dial.RawQuery = q.Encode()
	var opt remrunner.Options
	args := []string{
		"-c", dial.String(),
		"-k", key,
		"-a", relayAlias,
		"--no-sub",
	}
	for _, sv := range serves {
		args = append(args, "-r", "port://"+sv.Listen+"/"+sv.Dest)
	}
	if err := opt.ParseArgs(args); err != nil {
		return nil, fmt.Errorf("反向连接参数：%w", err)
	}
	r, err := opt.Prepare()
	if err != nil {
		return nil, fmt.Errorf("反向连接准备：%w", err)
	}
	r.URLs.ConsoleURL = r.ConsoleURLs[0]
	con, err := remrunner.NewConsole(r, r.URLs)
	if err != nil {
		return nil, fmt.Errorf("反向连接服务：%w", err)
	}
	cfg.Serves = serves
	return &Relay{cfg: cfg, con: con, done: make(chan struct{})}, nil
}

// Run keeps the reverse connection open until ctx ends. rem reconnects on its
// own with backoff; this loop only tracks the chain's state for the console.
func (r *Relay) Run(ctx context.Context) {
	ctx, r.cancel = context.WithCancel(ctx)
	go func() {
		_ = r.con.Run()
		close(r.done)
	}()
	if probe := strings.TrimSpace(r.cfg.ProbeURL); probe != "" {
		r.probeLoop(ctx, strings.TrimRight(probe, "/")+"/healthz")
	}
	<-ctx.Done()
	_ = r.con.Close()
}

// Close releases the tunnel; the relay-side listener goes with it.
func (r *Relay) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	_ = r.con.Close()
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
	}
}

// Status reports the latest known chain state.
func (r *Relay) Status() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// probeLoop asks the public entry the same /healthz nodes reach. A platform
// that cannot verify the entry's certificate reports that instead of "up".
func (r *Relay) probeLoop(ctx context.Context, probe string) {
	client := &http.Client{
		Timeout:   4 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	}
	var wasUp bool
	check := func() {
		up, detail := false, ""
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
		if err != nil {
			detail = err.Error()
		} else if resp, derr := client.Do(req); derr != nil {
			detail = derr.Error()
		} else {
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != http.StatusOK {
				detail = fmt.Sprintf("公网入口响应 %d", resp.StatusCode)
			} else {
				up = true
			}
		}
		r.mu.Lock()
		r.state = State{Up: up, Since: r.state.Since, Detail: detail}
		if up && !wasUp {
			r.state.Since = time.Now()
		}
		r.mu.Unlock()
		if up != wasUp {
			wasUp = up
			if r.cfg.Logf != nil {
				if up {
					r.cfg.Logf("反向连接已打通：%s 经公网入口回到本平台", probe)
				} else {
					r.cfg.Logf("反向连接未打通：%s（%s）", probe, detail)
				}
			}
		}
	}
	check()
	tick := time.NewTicker(probeInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check()
		}
	}
}
