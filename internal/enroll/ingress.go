package enroll

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Ingress is the public routing shell behind /join/, /node/ and /tunnel/
// (docs/14 §7.1). Later tickets register their handlers here; none of them
// touch the console login gate. With --public-url unset the whole shell is
// never mounted, and every route answers 404.
type Ingress struct {
	svc *Service

	mu       sync.Mutex
	handlers map[string]http.Handler // exact sub-path → handler, registered by later tickets
	prefixes map[string]http.Handler // longest-prefix handlers (e.g. /tunnel/)
	limiter  *failLimiter
}

// NewIngress builds the shell over a service.
func NewIngress(svc *Service) *Ingress {
	return &Ingress{
		svc:      svc,
		handlers: map[string]http.Handler{},
		prefixes: map[string]http.Handler{},
		limiter:  newFailLimiter(20, time.Minute),
	}
}

// Register mounts one handler at an exact path (e.g. "/join/install.sh").
// Registration is boot-time: the map is only read inside ServeHTTP.
func (in *Ingress) Register(path string, h http.Handler) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.handlers[path] = h
}

// RegisterPrefix mounts a handler for a path prefix (e.g. "/tunnel/"): the
// longest registered prefix wins after exact matches fail.
func (in *Ingress) RegisterPrefix(prefix string, h http.Handler) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.prefixes[prefix] = h
}

// ServeHTTP dispatches the node ingress. Unknown sub-routes answer 404 so a
// half-implemented surface never looks alive.
func (in *Ingress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/node/join":
		in.handleJoin(w, r)
		return
	case "/node/next":
		in.handleNext(w, r)
		return
	case "/node/report":
		in.handleReport(w, r)
		return
	case "/node/result":
		in.handleShellResult(w, r)
		return
	}
	in.mu.Lock()
	h := in.handlers[r.URL.Path]
	if h == nil {
		best := ""
		for prefix, candidate := range in.prefixes {
			if strings.HasPrefix(r.URL.Path, prefix) && len(prefix) > len(best) {
				best, h = prefix, candidate
			}
		}
	}
	in.mu.Unlock()
	if h != nil {
		h.ServeHTTP(w, r)
		return
	}
	writeErr(w, http.StatusNotFound, "not found")
}

// handleJoin exchanges an enrollment token for the node credential.
func (in *Ingress) handleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	if !in.allow(w, r, "") {
		return
	}
	token, ok := bearer(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "缺少接入令牌")
		return
	}
	var req JoinRequest
	if r.Body != nil {
		// A missing body is a valid request for older launchers; decode only
		// when there is something to decode.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
			return
		}
	}
	resp, err := in.svc.Join(token, req.InstallID)
	if err != nil {
		var je *JoinErr
		if asJoinErr(err, &je) {
			switch je.Outcome {
			case JoinHostMismatch:
				in.limiter.fail(clientIP(r), time.Now())
				writeErr(w, http.StatusConflict, je.Reason)
			default:
				in.limiter.fail(clientIP(r), time.Now())
				writeErr(w, http.StatusGone, je.Reason)
			}
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleNext serves the launcher's long-poll. The node credential decides the
// node; the wait is capped so a hung launcher cannot pin resources.
func (in *Ingress) handleNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 GET")
		return
	}
	nodeID, status := in.authNode(w, r)
	if status != 0 {
		return
	}
	wait := 30 * time.Second
	if raw := r.URL.Query().Get("wait"); raw != "" {
		if d, err := time.ParseDuration(raw + "s"); err == nil && d >= 0 && d <= 60*time.Second {
			wait = d
		}
	}
	writeJSON(w, http.StatusOK, in.svc.Next(nodeID, wait))
}

// handleReport applies a heartbeat / diagnostics report.
func (in *Ingress) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	nodeID, status := in.authNode(w, r)
	if status != 0 {
		return
	}
	var rep Report
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&rep); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	view, err := in.svc.Heartbeat(nodeID, rep)
	if err != nil {
		writeErr(w, http.StatusGone, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// authNode authenticates a node credential. It writes the failure response
// itself and returns a non-zero status when the request must stop: 401 for a
// credential nobody issued, 410 for one whose node is destroyed (the launcher
// treats 410 as "uninstall").
func (in *Ingress) authNode(w http.ResponseWriter, r *http.Request) (string, int) {
	if !in.allow(w, r, "") {
		return "", http.StatusTooManyRequests
	}
	presented, ok := bearer(r)
	if !ok {
		in.limiter.fail(clientIP(r), time.Now())
		writeErr(w, http.StatusUnauthorized, "缺少节点凭据")
		return "", http.StatusUnauthorized
	}
	view, ok := in.svc.AuthenticateNode(presented)
	if !ok {
		if view.State == NodeDestroyed {
			in.limiter.fail(clientIP(r), time.Now())
			writeErr(w, http.StatusGone, "节点已销毁")
			return "", http.StatusGone
		}
		in.limiter.fail(clientIP(r), time.Now())
		writeErr(w, http.StatusUnauthorized, "节点凭据无效")
		return "", http.StatusUnauthorized
	}
	return view.ID, 0
}

// allow applies the per-IP failure window. It writes 429 and returns false
// when the caller is over the limit.
func (in *Ingress) allow(w http.ResponseWriter, r *http.Request, _ string) bool {
	if retry := in.limiter.retryAfter(clientIP(r), time.Now()); retry > 0 {
		w.Header().Set("Retry-After", retry.String())
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，稍后再试")
		return false
	}
	return true
}

// bearer extracts the Authorization: Bearer value.
func bearer(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", false
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

// clientIP is the rate-limit key. It uses the socket address, not X-Forwarded
// headers: the reverse proxy in front may rewrite them, and a forged header
// must not be able to scoot around the limit.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// failLimiter is a small fixed-window failure counter: enough to keep a
// public endpoint from being ground down, without a dependency.
type failLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	state  map[string]failState
}

type failState struct {
	start time.Time
	count int
}

func newFailLimiter(limit int, window time.Duration) *failLimiter {
	return &failLimiter{limit: limit, window: window, state: map[string]failState{}}
}

// retryAfter returns how long the caller must wait, or 0 when allowed.
func (l *failLimiter) retryAfter(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.state[key]
	if !ok || now.Sub(st.start) >= l.window {
		return 0
	}
	if st.count < l.limit {
		return 0
	}
	return st.start.Add(l.window).Sub(now)
}

func (l *failLimiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.state[key]
	if !ok || now.Sub(st.start) >= l.window {
		l.state[key] = failState{start: now, count: 1}
		return
	}
	st.count++
	l.state[key] = st
}

// writeJSON and writeErr mirror the console's helpers: one JSON shape, a
// Content-Type the launcher can rely on, no caching of credential responses.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// asJoinErr is errors.As specialised to the join error.
func asJoinErr(err error, target **JoinErr) bool {
	je, ok := err.(*JoinErr)
	if ok {
		*target = je
	}
	return ok
}
