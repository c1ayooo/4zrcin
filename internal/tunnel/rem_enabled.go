//go:build remtunnel

package tunnel

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/rem/agent"
	"github.com/chainreactors/rem/protocol/core"
	"github.com/chainreactors/rem/protocol/message"
	_ "github.com/chainreactors/rem/protocol/serve/portforward"
	_ "github.com/chainreactors/rem/protocol/serve/raw"
	_ "github.com/chainreactors/rem/protocol/tunnel/websocket"
	_ "github.com/chainreactors/rem/protocol/wrapper"
	remrunner "github.com/chainreactors/rem/runner"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// Start runs the platform's rem server on loopback and returns the tunnel
// surface (docs/14 §7.1). The server trusts nothing a client says about its
// own topology: tickets bind one node id to one handshake, and the policy
// hooks fence the serves and dials to the two loopback addresses this
// instance actually needs.
func Start(svc *enroll.Service, cfg Config) (Ingress, error) {
	if svc == nil {
		return nil, errors.New("隧道入口缺少接入面")
	}
	if strings.TrimSpace(cfg.Key) == "" {
		return nil, errors.New("隧道入口缺少实例密钥")
	}
	// rem pads the key with PKCS7 before using it as an AES key: only a string
	// that pads to 16/24/32 bytes works, i.e. at most 31 chars. Reject the rest
	// here instead of failing every handshake later.
	if len(cfg.Key) > 31 {
		return nil, fmt.Errorf("隧道登录键长度 %d 超过 31：rem 会把它填充成非法 AES 密钥", len(cfg.Key))
	}
	if strings.TrimSpace(cfg.HubAddr) == "" || strings.TrimSpace(cfg.ConsoleAddr) == "" {
		return nil, errors.New("隧道入口需要 Hub 与控制台的回环地址")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	port := cfg.ListenPort
	if port == 0 {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("分配隧道端口：%w", err)
		}
		port = probe.Addr().(*net.TCPAddr).Port
		probe.Close() //nolint:errcheck
	}

	in := &ingress{
		svc:      svc,
		cfg:      cfg,
		port:     port,
		tickets:  map[string]ticketState{},
		expected: map[string]time.Time{},
		serves:   map[string]int{},
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	in.proxy = httputil.NewSingleHostReverseProxy(target)
	// The rem server mounts its handler at exactly /tunnel; the public path
	// carries the ticket, which must never reach the rem server. Go's
	// ReverseProxy passes WebSocket upgrades through to the backend.
	in.proxy.Director = func(r *http.Request) {
		r.URL.Scheme = "http"
		r.URL.Host = target.Host
		r.URL.Path = "/tunnel"
		r.URL.RawQuery = ""
		r.Host = target.Host
	}

	if err := in.startRemServer(); err != nil {
		return nil, err
	}
	return in, nil
}

type ingress struct {
	svc   *enroll.Service
	cfg   Config
	port  int
	proxy *httputil.ReverseProxy
	con   *remrunner.Console

	mu       sync.Mutex
	tickets  map[string]ticketState
	expected map[string]time.Time // node id → a ticket was just redeemed (one handshake)
	serves   map[string]int       // per-login serve counter (main + forks)
}

type ticketState struct {
	nodeID string
	exp    time.Time
}

// startRemServer assembles the rem server console from the same option surface
// the CLI uses and waits until its listener accepts connections.
func (in *ingress) startRemServer() error {
	var opt remrunner.Options
	args := []string{
		// ?wrapper=raw disables the random per-connection wrapper negotiation:
		// the launcher cannot copy a link off this server, and TLS on the
		// public leg plus rem's ticket/login are the transport facts that
		// matter (the stream wrappers are not a security boundary, docs/14
		// fact 14). Both ends must agree, so the ticket URL carries it too.
		"-s", fmt.Sprintf("ws://127.0.0.1:%d/tunnel?wrapper=raw", in.port),
		"-i", "127.0.0.1",
		"-k", in.cfg.Key,
		"--no-sub", // never open a public subscription/topology server
		"-q",
	}
	if err := opt.ParseArgs(args); err != nil {
		return fmt.Errorf("隧道参数：%w", err)
	}
	r, err := opt.Prepare()
	if err != nil {
		return fmt.Errorf("隧道准备：%w", err)
	}
	r.Policy = &remrunner.Policy{
		OnLogin:   in.onLogin,
		OnControl: in.onControl,
		OnDial:    in.onDial,
	}
	r.URLs.ConsoleURL = r.ConsoleURLs[0]
	con, err := remrunner.NewConsole(r, r.URLs)
	if err != nil {
		return fmt.Errorf("隧道服务：%w", err)
	}
	in.con = con

	runErr := make(chan error, 1)
	go func() { runErr <- con.Run() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-runErr:
			_ = con.Close()
			return fmt.Errorf("隧道服务启动失败：%w", err)
		default:
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", in.port), 200*time.Millisecond)
		if err == nil {
			conn.Close() //nolint:errcheck
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = con.Close()
	return fmt.Errorf("隧道服务在 5 秒内没有开始监听 127.0.0.1:%d", in.port)
}

// onLogin binds one handshake to the node id its ticket was issued for. A
// client cannot pick a different identity, and half-channel logins (which the
// launcher never uses) are refused outright.
func (in *ingress) onLogin(login *message.Login) error {
	if login.ChannelRole != "" {
		return errors.New("不接受半信道登录")
	}
	nodeID := strings.TrimSpace(login.Agent)
	if nodeID == "" {
		return errors.New("缺少节点标识")
	}
	now := in.cfg.Now()
	in.mu.Lock()
	exp, ok := in.expected[nodeID]
	if ok {
		delete(in.expected, nodeID)
	}
	in.serves[nodeID] = 0
	in.mu.Unlock()
	if !ok || now.After(exp) {
		return errors.New("没有有效的接入票据")
	}
	// The tunnel is up: from this moment the enrollment token is spent.
	in.svc.MarkJoined(nodeID)
	return nil
}

// onControl fences the serve topology. It runs for the initial control and,
// through the agent's ControlPolicy, for every forked extra serve: a node may
// only listen on its own loopback and only make the platform dial the Hub or
// the console API.
func (in *ingress) onControl(login *message.Login, local, remote *core.URL, inboundSide string) error {
	if inboundSide != core.SideLocal {
		return fmt.Errorf("隧道只允许节点侧监听（收到 %q）", inboundSide)
	}
	if local == nil {
		return errors.New("缺少本端监听地址")
	}
	host, _, err := net.SplitHostPort(local.Host)
	if err != nil || !isLoopbackHost(host) {
		return fmt.Errorf("节点监听必须在本机回环：%q", local.Host)
	}
	if local.Scheme != core.PortForwardServe && local.Scheme != core.RawServe {
		return fmt.Errorf("只允许端口转发，收到 %q", local.Scheme)
	}
	// The self-contained form carries the destination in the path (the
	// platform dials it); the paired form carries it in the remote URL.
	dest := local.PathString()
	if dest == "" && remote != nil {
		dest = remote.Host
	}
	if !in.allowedDest(dest) {
		return fmt.Errorf("目的地址 %q 不在白名单", dest)
	}
	nodeID := strings.TrimSpace(login.Agent)
	in.mu.Lock()
	in.serves[nodeID]++
	count := in.serves[nodeID]
	in.mu.Unlock()
	if count > 2 {
		return fmt.Errorf("每个节点最多两条转发（第 %d 条被拒）", count)
	}
	return nil
}

// onDial is the second fence: even a control the hooks missed cannot make the
// platform dial anything but its own two loopback services.
func (in *ingress) onDial(network, addr string) error {
	if in.allowedDest(addr) {
		return nil
	}
	return fmt.Errorf("平台不向 %q 拨号", addr)
}

func (in *ingress) allowedDest(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	return addr == in.cfg.HubAddr || addr == in.cfg.ConsoleAddr
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// issue mints a one-time ticket for a node.
func (in *ingress) issue(nodeID string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成票据：%w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(b[:])
	in.mu.Lock()
	in.tickets[ticket] = ticketState{nodeID: nodeID, exp: in.cfg.Now().Add(TicketTTL)}
	in.mu.Unlock()
	return ticket, nil
}

// redeem consumes a ticket and returns the node it was issued for.
func (in *ingress) redeem(ticket string) (string, int) {
	in.mu.Lock()
	defer in.mu.Unlock()
	st, ok := in.tickets[ticket]
	if !ok {
		return "", http.StatusUnauthorized
	}
	delete(in.tickets, ticket)
	if in.cfg.Now().After(st.exp) {
		return "", http.StatusGone
	}
	return st.nodeID, 0
}

// TicketHandler serves POST /node/ticket: a joined node exchanges its
// credential for one 60-second ticket (docs/14 §7.1).
func (in *ingress) TicketHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "只接受 POST")
			return
		}
		presented, ok := bearer(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "缺少节点凭据")
			return
		}
		view, ok := in.svc.AuthenticateNode(presented)
		if !ok {
			status := http.StatusUnauthorized
			if view.State == enroll.NodeDestroyed {
				status = http.StatusGone
			}
			writeError(w, status, "节点凭据无效")
			return
		}
		if view.State == enroll.NodeDestroying {
			// A destroying node gets no new tunnel: the old one is closed by
			// the destroy path, and a new one would race the uninstall.
			writeError(w, http.StatusGone, "节点正在销毁")
			return
		}
		ticket, err := in.issue(view.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"ticket":     ticket,
			"url":        in.tunnelURL(ticket),
			"expires_in": int(TicketTTL / time.Second),
		})
	})
}

// tunnelURL is the dial URL the launcher uses: PublicURL's host and path
// prefix, a websocket scheme, the ticket in the path, and the wrapper mode both
// ends agreed on.
//
// The port is explicit even when PublicURL omits it. rem's console URL parser
// fills a portless address with its own default tunnel port (34996), so
// "wss://host/tunnel/..." would dial host:34996 instead of 443. The wss scheme
// (rather than ?tls=1) is what makes the client speak TLS: the websocket dialer
// owns that leg, while rem's ?tls hook wraps the connection above the HTTP
// upgrade, which only works for raw carriers.
func (in *ingress) tunnelURL(ticket string) string {
	base := strings.TrimRight(in.cfg.PublicURL, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base + "/tunnel/" + ticket + "?wrapper=raw"
	}
	scheme, port := "ws", "80"
	if u.Scheme == "https" {
		scheme, port = "wss", "443"
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), port)
	}
	return scheme + "://" + host + strings.TrimRight(u.Path, "/") + "/tunnel/" + ticket + "?wrapper=raw"
}

// TunnelHandler serves GET /tunnel/{ticket}: the node's websocket upgrade,
// authenticated by the ticket and forwarded to the rem server.
func (in *ingress) TunnelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "只接受 GET")
			return
		}
		ticket := strings.TrimPrefix(r.URL.Path, "/tunnel/")
		if ticket == "" || strings.Contains(ticket, "/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		nodeID, status := in.redeem(ticket)
		if status != 0 {
			writeError(w, status, "票据无效或已使用")
			return
		}
		// The next handshake from this node is the one this ticket bought.
		in.mu.Lock()
		in.expected[nodeID] = in.cfg.Now().Add(TicketTTL)
		in.mu.Unlock()
		in.proxy.ServeHTTP(w, r)
	})
}

// Session reports the node's live tunnel from the rem agent registry.
func (in *ingress) Session(nodeID string) (SessionInfo, bool) {
	a, ok := agent.Agents.Get(nodeID)
	if !ok || a == nil || a.IsClosed() {
		return SessionInfo{}, false
	}
	inB, outB, start, _ := a.TrafficStats()
	return SessionInfo{Up: true, BytesIn: inB, BytesOut: outB, Since: start}, true
}

// Close tears a node's tunnel down (destroy).
func (in *ingress) Close(nodeID string) error {
	a, ok := agent.Agents.Get(nodeID)
	if !ok || a == nil {
		return nil
	}
	a.Close(errors.New("节点已销毁"))
	return nil
}

// Port is the loopback port the rem server listens on.
func (in *ingress) Port() int { return in.port }

func bearer(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
