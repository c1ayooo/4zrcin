package enroll

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/noderes"
)

// Service is the record-keeping core of the node ingress (docs/14 §7): it
// owns the enrollment records, the node profiles and the two credential
// checks. Transport details (tickets, the tunnel, artifact downloads) build on
// what it exposes; they never write its file.
type Service struct {
	store *Store
	opt   Options

	mu     sync.Mutex
	queues map[string][]Directive // per-node queued directives, monotonically numbered
	seq    map[string]uint64      // last directive sequence per node
	wake   map[string]chan struct{}
	// shellPending pairs a queued shell operation with the caller waiting on
	// its answer; shellInFlight bounds how many one node may hold.
	shellPending  map[string]chan ShellResult
	shellInFlight map[string]int
	// scanProxy is the node default egress proxy the platform hands its
	// launchers (the operator's cyberhub.proxy) and scanProxyDigest its
	// fingerprint; nodeBusy answers whether a run is executing on a node.
	// All three come from the control plane after New (SetScanProxy,
	// SetNodeBusy), which is why they are not in Options.
	scanProxy       string
	scanProxyDigest string
	nodeBusy        func(nodeID string) bool
}

// Options is the platform-side configuration handed in by main. Everything the
// join response carries comes from here, so the launcher mirrors the control
// plane's own loopback addresses (docs/14 §7.1).
type Options struct {
	// PublicURL is the console's public base URL (no path, no query).
	PublicURL string
	// DataDir is the instance root's absolute path; the launcher mirrors it so
	// the case-directory contract holds on the node.
	DataDir string
	// ConsoleAddr and HubAddr are the loopback addresses as this process sees
	// them ("127.0.0.1:PORT"), not the public ones.
	ConsoleAddr string
	HubAddr     string
	// HubKey is the Hub's access key, carried into the launcher's
	// --server-url.
	HubKey string
	// IssueKey derives node credentials (the instance's tunnel key): rotating
	// it invalidates every existing node credential, which is the documented
	// consequence of rotating that key (docs/14 §7).
	IssueKey []byte
	// Now is the clock seam for tests.
	Now func() time.Time
	// Dist reports the artifacts this platform can currently serve. Nil means
	// "nothing to offer yet" (the bootstrap ticket fills it in).
	Dist func() []DistEntry
}

// DistEntry is one downloadable node artifact (docs/14 §7.1).
type DistEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Directive is one queued instruction for a launcher (docs/14 §7.2). The
// sequence number persists on the node side so redelivery is idempotent. A
// "shell" directive carries the operation in Shell; an older launcher that
// knows no such op logs it and moves on.
type Directive struct {
	Seq   uint64     `json:"seq"`
	Op    string     `json:"op"` // "uninstall" | "shell"
	Shell *ShellTask `json:"shell,omitempty"`
}

var nodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// NodeNameValid reports whether name is a legal operator-visible node name.
func NodeNameValid(name string) bool { return nodePattern.MatchString(name) }

// CreateOptions is one operator request to create an enrollment.
type CreateOptions struct {
	Name       string
	Recipe     string
	Deploy     string
	TTLMinutes int
}

// Created is what the create response carries: the plaintext token exists
// only here (docs/14 §7).
type Created struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Token          string    `json:"token"`
	InstallCommand string    `json:"install_command"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// New builds the service over an opened store.
func New(store *Store, opt Options) *Service {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if len(opt.IssueKey) == 0 {
		panic("enroll: IssueKey is required") // programming error, never a runtime condition
	}
	return &Service{
		store:  store,
		opt:    opt,
		queues: map[string][]Directive{},
		seq:    map[string]uint64{},
		wake:   map[string]chan struct{}{},
		// Until the control plane installs its default, the node default is
		// direct: the fingerprint of the empty proxy, not a zero value that
		// would read every reporting node as pending.
		scanProxyDigest: ScanProxyDigest(""),
		shellPending:    map[string]chan ShellResult{},
		shellInFlight:   map[string]int{},
	}
}

// PublicURL is the console's public base URL as configured.
func (s *Service) PublicURL() string { return s.opt.PublicURL }

// ScanProxyDigest fingerprints the default egress proxy a launcher has
// reconciled scan to. The heartbeat carries the fingerprint rather than the
// proxy: the value can hold credentials, and the platform only needs to know
// whether the node runs what it hands out now. The fingerprint of the empty
// proxy is a constant, so a launcher that reports at all is recognised.
func ScanProxyDigest(proxy string) string {
	sum := sha256.Sum256([]byte(proxy))
	return hex.EncodeToString(sum[:8])
}

// SetScanProxy installs the source of the node default egress proxy and
// re-evaluates every node against it: a node whose scan still runs the
// previous value stops taking work until its launcher reports the new one
// applied (docs/14 §7.6). A parked long-poll is woken so the launcher learns
// the change now, not at the end of its wait. Nil means no default (direct).
func (s *Service) SetScanProxy(source func() string) {
	proxy := ""
	if source != nil {
		proxy = source()
	}
	digest := ScanProxyDigest(proxy)
	s.mu.Lock()
	s.scanProxy, s.scanProxyDigest = proxy, digest
	s.mu.Unlock()
	// store.mu → s.mu is the lock order Heartbeat already uses; each lock is
	// taken and released on its own here, so the two never nest.
	s.store.mu.Lock()
	ids := make([]string, 0, len(s.store.mu.f.Nodes))
	for i := range s.store.mu.f.Nodes {
		applyScanProxy(&s.store.mu.f.Nodes[i], digest)
		ids = append(ids, s.store.mu.f.Nodes[i].ID)
	}
	s.store.mu.Unlock()
	s.mu.Lock()
	for _, id := range ids {
		s.wakeLocked(id)
	}
	s.mu.Unlock()
}

// SetNodeBusy installs the answer to "is a run executing on this node right
// now". The launcher reads it with the proxy: it restarts scan only when the
// node is idle, so a proxy change never interrupts a direction.
func (s *Service) SetNodeBusy(busy func(nodeID string) bool) {
	s.mu.Lock()
	s.nodeBusy = busy
	s.mu.Unlock()
}

// applyScanProxy records whether the node's scan runs the default the
// platform hands out now. A launcher that never reported (one that predates
// the fingerprint) is not managed and reads as applied.
func applyScanProxy(n *node, digest string) {
	n.ScanProxyApplied = n.ScanProxyDigest == "" || n.ScanProxyDigest == digest
}

func (s *Service) proxyDigest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanProxyDigest
}

func (s *Service) nodeIsBusy(nodeID string) bool {
	s.mu.Lock()
	busy := s.nodeBusy
	s.mu.Unlock()
	return busy != nil && busy(nodeID)
}

// NodeNameSuggestion returns the default name for an enrollment without one.
func NodeNameSuggestion(nodeID string) string {
	if len(nodeID) < 4 {
		return "node"
	}
	return "node-" + nodeID[:4]
}

// Create records a new enrollment and returns its plaintext token. The token's
// digest is stored; the caller must hand the plaintext to the operator and
// then forget it.
func (s *Service) Create(o CreateOptions) (Created, error) {
	recipe := strings.TrimSpace(o.Recipe)
	if recipe == "" {
		recipe = RecipeFull
	}
	if recipe != RecipeFull && recipe != RecipeCapture {
		return Created{}, fmt.Errorf("配方只有 %s 与 %s", RecipeFull, RecipeCapture)
	}
	deploy := strings.TrimSpace(o.Deploy)
	if deploy == "" {
		deploy = DeployCommand
	}
	if deploy != DeployCommand && deploy != DeploySSH {
		return Created{}, fmt.Errorf("部署方式只有 %s 与 %s", DeployCommand, DeploySSH)
	}
	name := strings.TrimSpace(o.Name)
	if name != "" && !NodeNameValid(name) {
		return Created{}, fmt.Errorf("节点名只允许小写字母、数字与连字符，2–32 位")
	}
	ttl := DefaultTokenTTL
	if o.TTLMinutes > 0 {
		ttl = time.Duration(o.TTLMinutes) * time.Minute
	}

	token, err := NewEnrollmentToken()
	if err != nil {
		return Created{}, err
	}
	now := s.opt.Now().UTC()

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	id, err := newID()
	if err != nil {
		return Created{}, err
	}
	rec := enrollment{
		ID:        id,
		TokenHash: HashCredential(token),
		Name:      name,
		Recipe:    recipe,
		Deploy:    deploy,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		State:     EnrollPending,
	}
	s.store.mu.f.Enrollments = append(s.store.mu.f.Enrollments, rec)
	if err := s.store.saveLocked(); err != nil {
		// Roll the in-memory append back so memory and disk agree.
		s.store.mu.f.Enrollments = s.store.mu.f.Enrollments[:len(s.store.mu.f.Enrollments)-1]
		return Created{}, err
	}
	return Created{
		ID:        rec.ID,
		Name:      rec.Name,
		Token:     token,
		ExpiresAt: rec.ExpiresAt,
		InstallCommand: fmt.Sprintf("curl -fsSL %s/join/%s/install.sh | sudo sh",
			strings.TrimRight(s.opt.PublicURL, "/"), token),
	}, nil
}

// EnrollmentView is the operator-facing projection of a record.
type EnrollmentView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Recipe    string    `json:"recipe"`
	Deploy    string    `json:"deploy"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	NodeID    string    `json:"node_id,omitempty"`
}

// ListEnrollments returns every record in creation order with the effective
// state (expiry applied on read).
func (s *Service) ListEnrollments() []EnrollmentView {
	now := s.opt.Now().UTC()
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	out := make([]EnrollmentView, 0, len(s.store.mu.f.Enrollments))
	for i := range s.store.mu.f.Enrollments {
		e := &s.store.mu.f.Enrollments[i]
		state := e.effectiveState(now)
		out = append(out, EnrollmentView{
			ID: e.ID, Name: e.Name, Recipe: e.Recipe, Deploy: e.Deploy,
			State: state, CreatedAt: e.CreatedAt, ExpiresAt: e.ExpiresAt, NodeID: e.NodeID,
		})
	}
	return out
}

// Sentinel revoke failures, so the HTTP layer can pick a status without
// matching on message text.
var (
	// ErrNoSuchEnrollment is an unknown record id (404).
	ErrNoSuchEnrollment = errors.New("没有这个接入记录")
	// ErrEnrollmentSettled is a record that can no longer be revoked: joined
	// nodes are destroyed instead, and an expired token admits nobody (409).
	ErrEnrollmentSettled = errors.New("接入记录已结算")
)

// RevokeEnrollment removes an unjoined invitation. A joined one cannot be
// revoked here: the node already holds a credential, and the operator action
// for that is destroy (docs/14 §7.3).
func (s *Service) RevokeEnrollment(id string) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for i := range s.store.mu.f.Enrollments {
		e := &s.store.mu.f.Enrollments[i]
		if e.ID != id {
			continue
		}
		switch e.State {
		case EnrollRevoked:
			return nil
		case EnrollJoined:
			return fmt.Errorf("%w：接入记录已经回连，改用销毁节点", ErrEnrollmentSettled)
		}
		if e.effectiveState(s.opt.Now().UTC()) == EnrollExpired {
			return fmt.Errorf("%w：接入令牌已经过期", ErrEnrollmentSettled)
		}
		e.State = EnrollRevoked
		e.RevokedReason = "operator"
		return s.store.saveLocked()
	}
	return ErrNoSuchEnrollment
}

// TokenCheck is what an unauthenticated caller may learn from an enrollment
// token (the bootstrap script and the launcher download): the recipe it was
// issued for and whether it still admits a host. Nothing about existing nodes.
type TokenCheck struct {
	Recipe    string
	Deploy    string
	ExpiresAt time.Time
}

// CheckToken validates an enrollment token without spending it.
func (s *Service) CheckToken(token string) (TokenCheck, bool) {
	cred, err := ParseCredential(token)
	if err != nil || cred.Kind != CredentialEnrollmentToken {
		return TokenCheck{}, false
	}
	now := s.opt.Now().UTC()
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	for i := range s.store.mu.f.Enrollments {
		e := &s.store.mu.f.Enrollments[i]
		if e.TokenHash != cred.SHA256 {
			continue
		}
		if e.effectiveState(now) != EnrollPending {
			return TokenCheck{}, false
		}
		return TokenCheck{Recipe: e.Recipe, Deploy: e.Deploy, ExpiresAt: e.ExpiresAt}, true
	}
	return TokenCheck{}, false
}

// JoinRequest is the node's join payload.
type JoinRequest struct {
	InstallID string `json:"install_id"`
}

// JoinResponse is exactly the join response shape of docs/14 §7.1. The
// launcher and the platform share these types.
type JoinResponse struct {
	NodeID         string       `json:"node_id"`
	NodeCredential string       `json:"node_credential"`
	PublicURL      string       `json:"public_url"`
	Tunnel         TunnelParams `json:"tunnel"`
	Hub            HubParams    `json:"hub"`
	ConsoleAddr    string       `json:"console_addr"`
	DataDir        string       `json:"data_dir"`
	Dist           []DistEntry  `json:"dist"`
}

// TunnelParams is what the launcher needs to open the tunnel (docs/14 §7.1).
type TunnelParams struct {
	Key  string `json:"key"`
	Path string `json:"path"`
}

// HubParams points the launcher's scan at the tunneled Hub.
type HubParams struct {
	Addr string `json:"addr"`
	Key  string `json:"key"`
}

// JoinOutcome classifies the four join results (docs/14 §7.1). The HTTP
// layer maps them to status codes; the service keeps them typed.
type JoinOutcome int

const (
	JoinOK           JoinOutcome = iota + 1 // fresh or idempotent: credential issued
	JoinSpent                               // 410: spent, revoked or expired
	JoinHostMismatch                        // 409: token bound to another install
)

// JoinErr is returned with the outcome so the HTTP layer can say why.
type JoinErr struct {
	Outcome JoinOutcome
	Reason  string
}

func (e *JoinErr) Error() string { return e.Reason }

// Join exchanges a valid enrollment token for the node's credential.
//
// First call binds the token to (node id, install id) and issues the derived
// credential; the same token and the same install id return the same
// credential, which makes install retries safe against a platform restart.
func (s *Service) Join(token, installID string) (JoinResponse, error) {
	cred, err := ParseCredential(token)
	if err != nil || cred.Kind != CredentialEnrollmentToken {
		return JoinResponse{}, &JoinErr{Outcome: JoinSpent, Reason: "接入令牌无效"}
	}
	installID = strings.TrimSpace(installID)
	if installID == "" {
		return JoinResponse{}, &JoinErr{Outcome: JoinHostMismatch, Reason: "缺少安装标识"}
	}
	now := s.opt.Now().UTC()

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	f := &s.store.mu.f
	var rec *enrollment
	for i := range f.Enrollments {
		if f.Enrollments[i].TokenHash == cred.SHA256 {
			rec = &f.Enrollments[i]
			break
		}
	}
	if rec == nil {
		return JoinResponse{}, &JoinErr{Outcome: JoinSpent, Reason: "接入令牌无效"}
	}
	switch rec.effectiveState(now) {
	case EnrollJoined, EnrollRevoked, EnrollExpired:
		return JoinResponse{}, &JoinErr{Outcome: JoinSpent, Reason: "接入令牌已作废"}
	}
	if rec.NodeID != "" {
		if rec.InstallID != installID {
			return JoinResponse{}, &JoinErr{Outcome: JoinHostMismatch, Reason: "该接入令牌已绑定另一台主机"}
		}
		// Idempotent retry: same token, same host, same credential.
		return s.joinResponseLocked(f.lookupNode(rec.NodeID), rec), nil
	}

	// First join: bind the token to this host.
	nodeID, err := newID()
	if err != nil {
		return JoinResponse{}, err
	}
	credential := NodeCredential(s.opt.IssueKey, nodeID, installID)
	name := rec.Name
	if name == "" {
		name = NodeNameSuggestion(nodeID)
	}
	rec.NodeID = nodeID
	rec.InstallID = installID
	f.Nodes = append(f.Nodes, node{
		ID:           nodeID,
		Name:         name,
		EnrollmentID: rec.ID,
		Recipe:       rec.Recipe,
		Deploy:       rec.Deploy,
		CredSHA256:   HashCredential(credential),
		State:        NodeJoining,
		InstallID:    installID,
		JoinedAt:     now,
	})
	if err := s.store.saveLocked(); err != nil {
		return JoinResponse{}, err
	}
	n := f.lookupNode(nodeID)
	return s.joinResponseLocked(n, rec), nil
}

func (f *file) lookupNode(id string) *node {
	for i := range f.Nodes {
		if f.Nodes[i].ID == id {
			return &f.Nodes[i]
		}
	}
	return nil
}

func (s *Service) joinResponseLocked(n *node, rec *enrollment) JoinResponse {
	if n == nil {
		return JoinResponse{}
	}
	credential := NodeCredential(s.opt.IssueKey, n.ID, n.InstallID)
	dist := []DistEntry{}
	if s.opt.Dist != nil {
		dist = s.opt.Dist()
	}
	return JoinResponse{
		NodeID:         n.ID,
		NodeCredential: credential,
		PublicURL:      strings.TrimRight(s.opt.PublicURL, "/"),
		Tunnel:         TunnelParams{Key: RemTunnelKey(s.opt.IssueKey), Path: "/tunnel"},
		Hub:            HubParams{Addr: s.opt.HubAddr, Key: s.opt.HubKey},
		ConsoleAddr:    s.opt.ConsoleAddr,
		DataDir:        s.opt.DataDir,
		Dist:           dist,
	}
}

// AuthenticateNode resolves a node credential to its record. The node id in
// the credential is only a lookup hint; the digest decides.
func (s *Service) AuthenticateNode(presented string) (NodeView, bool) {
	cred, err := ParseCredential(presented)
	if err != nil || cred.Kind != CredentialNode {
		return NodeView{}, false
	}
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	n := s.store.mu.f.lookupNode(cred.NodeID)
	if n == nil || n.CredSHA256 != cred.SHA256 {
		return NodeView{}, false
	}
	if n.State == NodeDestroyed {
		return NodeView{ID: n.ID, State: NodeDestroyed}, false
	}
	return s.viewLocked(n), true
}

// NodeView is the operator/launcher-facing projection of a node profile.
type NodeView struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	State        string     `json:"state"`
	Recipe       string     `json:"recipe"`
	Deploy       string     `json:"deploy"`
	Hostname     string     `json:"hostname,omitempty"`
	OS           string     `json:"os,omitempty"`
	Arch         string     `json:"arch,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	DestroyedAt  *time.Time `json:"destroyed_at,omitempty"`
	NodeBinPath  string     `json:"node_bin_path,omitempty"`
	Online       bool       `json:"online"`
	DispatchesOK bool       `json:"dispatch_ready"`
	LastError    string     `json:"last_error,omitempty"`
	// Resources is the node's latest host sample; nil until one arrives.
	Resources *noderes.Snapshot `json:"resources,omitempty"`
	// Health is derived at read time from the snapshot plus the tunnel and
	// restart facts; empty for an offline node (离线 is its own state).
	Health          Health `json:"health,omitempty"`
	LauncherVersion string `json:"launcher_version,omitempty"`
	UptimeSeconds   int64  `json:"uptime_seconds,omitempty"`
	ScanRestarts    int    `json:"scan_restarts,omitempty"`
	// The node's own mirror ports; 0 until a heartbeat carries them.
	MirrorHubPort     int `json:"mirror_hub_port,omitempty"`
	MirrorConsolePort int `json:"mirror_console_port,omitempty"`
	// ProxyPending: the operator changed the node default egress proxy and
	// this node's scan still runs the previous one. It takes no work until
	// its launcher reports the new one applied (docs/14 §7.6). DispatchesOK
	// deliberately stays untouched: a shell on the node does not egress
	// through scan.
	ProxyPending bool `json:"proxy_pending,omitempty"`
}

// Live says the node's launcher is reachable and the node is not going away.
// A shell task runs on the launcher and never egresses through scan, so an
// egress change does not stop it the way it stops dispatch (DispatchesOK).
func (v NodeView) Live() bool {
	return v.Online && v.State != NodeDestroying && v.State != NodeDestroyed
}

// Health thresholds (docs/14 §7.3). Constants, not settings: an operator who
// needs different numbers is telling us the defaults are wrong, not asking
// for a config surface.
const (
	// DiskFreeAbsFloor and DiskFreeRatioFloor: below either, the data volume
	// cannot take another case — abnormal, not degraded.
	DiskFreeAbsFloor   uint64 = 5 << 30 // 5 GiB
	DiskFreeRatioFloor        = 0.10
	// MemRatioCeiling: the host is close to swapping — degraded.
	MemRatioCeiling = 0.85
	// ScanRSSCeiling: one scan process holding this much is a leak or a
	// runaway scan — degraded.
	ScanRSSCeiling uint64 = 4 << 30 // 4 GiB
	// ScanRestartsCeiling within the online period: the scan keeps dying —
	// degraded.
	ScanRestartsCeiling = 3
)

// Health is the derived three-state the cards render.
type Health struct {
	State   string   `json:"state"` // "" (unknown/offline), healthy, degraded, abnormal
	Reasons []string `json:"reasons,omitempty"`
}

// deriveHealth scores the latest sample plus the live facts. Only the tunnel
// and restart facts can degrade without a sample; everything else needs the
// launcher's numbers.
// DeriveHealth scores the latest sample plus the live facts. Exported for the
// local node row: the control plane computes its own card from the same rules.
func DeriveHealth(online bool, tunnelUp bool, scanRestarts int, res *noderes.Snapshot) Health {
	if !online {
		return Health{State: ""}
	}
	h := Health{State: "healthy"}
	degrade := func(reason string) {
		if h.State == "healthy" {
			h.State = "degraded"
		}
		h.Reasons = append(h.Reasons, reason)
	}
	abnormal := func(reason string) {
		h.State = "abnormal"
		h.Reasons = append(h.Reasons, reason)
	}
	if !tunnelUp {
		degrade("隧道未连接")
	}
	if scanRestarts >= ScanRestartsCeiling {
		degrade(fmt.Sprintf("scan 已重启 %d 次（阈值 %d）", scanRestarts, ScanRestartsCeiling))
	}
	if res == nil {
		if h.State == "healthy" {
			h.State = "" // no sample yet: unknown rather than a green lie
		}
		return h
	}
	if res.DiskTotal > 0 {
		ratio := float64(res.DiskFree) / float64(res.DiskTotal)
		if res.DiskFree < DiskFreeAbsFloor || ratio < DiskFreeRatioFloor {
			abnormal(fmt.Sprintf("数据目录磁盘剩余 %s（%.0f%%）", humanBytes(res.DiskFree), ratio*100))
		}
	}
	if res.MemTotal > 0 {
		if ratio := float64(res.MemUsed) / float64(res.MemTotal); ratio > MemRatioCeiling {
			degrade(fmt.Sprintf("内存占用 %.0f%%", ratio*100))
		}
	}
	if res.ScanRSS > ScanRSSCeiling {
		degrade(fmt.Sprintf("scan 进程内存 %s", humanBytes(res.ScanRSS)))
	}
	if h.State == "healthy" && len(h.Reasons) > 0 {
		h.State = "degraded"
	}
	return h
}

// humanBytes keeps the reason lines short: one decimal at GiB, integer at MiB.
func humanBytes(v uint64) string {
	switch {
	case v >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(v)/(1<<30))
	case v >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(v)/(1<<20))
	default:
		return fmt.Sprintf("%d B", v)
	}
}

// OnlineWindow is how fresh the last long-poll/heartbeat must be for a node to
// read as online (docs/14 §7).
const OnlineWindow = 90 * time.Second

func (s *Service) viewLocked(n *node) NodeView {
	online := false
	// A node is live while its launcher answers; it takes work only when it
	// is also running the egress the platform hands out (docs/14 §7.6).
	live := false
	proxyPending := n.ScanProxyDigest != "" && !n.ScanProxyApplied
	if n.State != NodeDestroyed && n.State != NodeDestroying && n.LastSeenAt != nil {
		online = s.opt.Now().UTC().Sub(*n.LastSeenAt) <= OnlineWindow
		live = online && n.State != NodeDestroying && n.State != NodeDestroyed
	}
	state := n.State
	if state != NodeDestroying && state != NodeDestroyed {
		if online {
			state = NodeOnline
		} else {
			state = NodeOffline
		}
	}
	tunnelUp := true
	if n.TunnelUp != nil {
		tunnelUp = *n.TunnelUp
	}
	return NodeView{
		ID: n.ID, Name: n.Name, State: state, Recipe: n.Recipe, Deploy: n.Deploy,
		Hostname: n.Hostname, OS: n.OS, Arch: n.Arch,
		LastSeenAt: n.LastSeenAt, DestroyedAt: n.DestroyedAt, NodeBinPath: n.NodeBinPath,
		Online: online,
		// The file contract is in place (docs/14 §8): a joined node takes
		// intents once it reports live; a destroying or destroyed one never
		// does.
		DispatchesOK:      live && !proxyPending,
		LastError:         n.LastError,
		Resources:         n.Resources,
		LauncherVersion:   n.LauncherVersion,
		UptimeSeconds:     n.UptimeSeconds,
		ScanRestarts:      n.ScanRestarts,
		MirrorHubPort:     n.MirrorHubPort,
		MirrorConsolePort: n.MirrorConsolePort,
		ProxyPending:      proxyPending,
		// 离线 has its own state; health only means something while online.
		Health: DeriveHealth(online, tunnelUp, n.ScanRestarts, n.Resources),
	}
}

// Nodes lists every node profile (newest last).
func (s *Service) Nodes() []NodeView {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	out := make([]NodeView, 0, len(s.store.mu.f.Nodes))
	for i := range s.store.mu.f.Nodes {
		out = append(out, s.viewLocked(&s.store.mu.f.Nodes[i]))
	}
	return out
}

// NodeByID resolves one node profile.
func (s *Service) NodeByID(id string) (NodeView, bool) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	n := s.store.mu.f.lookupNode(id)
	if n == nil {
		return NodeView{}, false
	}
	return s.viewLocked(n), true
}

// Report updates the liveness bookkeeping from a node heartbeat.
type Report struct {
	Hostname    string `json:"hostname,omitempty"`
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
	NodeBinPath string `json:"node_bin_path,omitempty"`
	TunnelUp    *bool  `json:"tunnel_up,omitempty"`
	Event       string `json:"event,omitempty"`
	Error       string `json:"error,omitempty"`
	// Resources is the launcher's host sample (docs/14 §7.3): CPU/memory/disk
	// plus the scan process's RSS. nil on kernels that cannot read counters.
	Resources *noderes.Snapshot `json:"resources,omitempty"`
	// LauncherVersion is the reporting launcher's build id; the card shows it
	// so a stale launcher is visible next to the platform's version.
	LauncherVersion string `json:"launcher_version,omitempty"`
	// UptimeSeconds is how long this launcher process has been running.
	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`
	// ScanRestarts is how many times this launcher process has restarted the
	// scan; the health rule reads it as "the scan keeps dying".
	ScanRestarts int `json:"scan_restarts,omitempty"`
	// MirrorHubPort / MirrorConsolePort are the loopback ports this node
	// opened for its tunnel mirrors (docs/14 §7.2). The platform points the
	// session's API base at the console one, so a node never needs the
	// platform's own port numbers free on its host.
	MirrorHubPort     int `json:"mirror_hub_port,omitempty"`
	MirrorConsolePort int `json:"mirror_console_port,omitempty"`
	// ScanProxyDigest is the fingerprint (ScanProxyDigest) of the default
	// egress proxy the launcher has reconciled scan to. A launcher that
	// predates the field sends nothing and is never held for a proxy change.
	ScanProxyDigest string `json:"scan_proxy_digest,omitempty"`
}

// Heartbeat applies one report and returns the node's fresh view. The first
// heartbeat moves a joining node out of the joining state.
func (s *Service) Heartbeat(nodeID string, rep Report) (NodeView, error) {
	digest := s.proxyDigest()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	n := s.store.mu.f.lookupNode(nodeID)
	if n == nil {
		return NodeView{}, errors.New("节点不存在")
	}
	if n.State == NodeDestroyed {
		return NodeView{}, errors.New("节点已销毁")
	}
	now := s.opt.Now().UTC()
	n.LastSeenAt = &now
	if rep.Hostname != "" {
		n.Hostname = rep.Hostname
	}
	if rep.OS != "" {
		n.OS = rep.OS
	}
	if rep.Arch != "" {
		n.Arch = rep.Arch
	}
	if rep.NodeBinPath != "" {
		n.NodeBinPath = rep.NodeBinPath
	}
	n.LastError = rep.Error
	if rep.Resources != nil {
		n.Resources = rep.Resources
	}
	if rep.LauncherVersion != "" {
		n.LauncherVersion = rep.LauncherVersion
	}
	if rep.UptimeSeconds > 0 {
		n.UptimeSeconds = rep.UptimeSeconds
	}
	// Mirror the launcher's current counter unconditionally: a restarted
	// launcher reports 0 and the stale count must not keep the node degraded.
	n.ScanRestarts = rep.ScanRestarts
	if rep.TunnelUp != nil {
		up := *rep.TunnelUp
		n.TunnelUp = &up
	}
	if rep.MirrorHubPort > 0 {
		n.MirrorHubPort = rep.MirrorHubPort
	}
	if rep.MirrorConsolePort > 0 {
		n.MirrorConsolePort = rep.MirrorConsolePort
	}
	if rep.ScanProxyDigest != "" {
		n.ScanProxyDigest = rep.ScanProxyDigest
		applyScanProxy(n, digest)
	}
	if rep.Event == "uninstalled" {
		// The launcher's receipt (docs/14 §7.3): the node is gone for good.
		n.State = NodeDestroyed
		n.DestroyedAt = &now
	}
	if err := s.store.saveLocked(); err != nil {
		return NodeView{}, err
	}
	s.wakeLocked(nodeID)
	return s.viewLocked(n), nil
}

// MarkJoined spends the enrollment once the node's tunnel is up (called by the
// tunnel ingress when the first tunnel session is established).
func (s *Service) MarkJoined(nodeID string) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	n := s.store.mu.f.lookupNode(nodeID)
	if n == nil {
		return
	}
	for i := range s.store.mu.f.Enrollments {
		e := &s.store.mu.f.Enrollments[i]
		if e.ID != n.EnrollmentID || e.State != EnrollPending {
			continue
		}
		e.State = EnrollJoined
		now := s.opt.Now().UTC()
		e.JoinedAt = now
		_ = s.store.saveLocked() // best effort: a failed save must not drop the tunnel
		return
	}
}

// ErrNoSuchNode is an unknown node id (404 at the operator surface).
var ErrNoSuchNode = errors.New("没有这个节点")

// Destroy starts the teardown of one node (docs/14 §7.3): the record moves to
// destroying, the tunnel is closed by the caller, and the uninstall directive
// is queued. It is idempotent: destroying a destroying node re-queues nothing
// new in terms of state.
func (s *Service) Destroy(nodeID string) (NodeView, error) {
	now := s.opt.Now().UTC()
	s.store.mu.Lock()
	n := s.store.mu.f.lookupNode(nodeID)
	if n == nil {
		s.store.mu.Unlock()
		return NodeView{}, ErrNoSuchNode
	}
	if n.State == NodeDestroyed {
		view := s.viewLocked(n)
		s.store.mu.Unlock()
		return view, nil
	}
	if n.State != NodeDestroying {
		n.State = NodeDestroying
		n.DestroyReqAt = &now
		if err := s.store.saveLocked(); err != nil {
			s.store.mu.Unlock()
			return NodeView{}, err
		}
	}
	view := s.viewLocked(n)
	s.store.mu.Unlock()
	if _, err := s.Enqueue(nodeID, "uninstall"); err != nil {
		return NodeView{}, err
	}
	return view, nil
}

// MarkDestroyed finalises a destroying node on the launcher's receipt, or on
// the operator's "forget" for a node that will never report again.
func (s *Service) MarkDestroyed(nodeID string) (NodeView, error) {
	now := s.opt.Now().UTC()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	n := s.store.mu.f.lookupNode(nodeID)
	if n == nil {
		return NodeView{}, ErrNoSuchNode
	}
	if n.State == NodeDestroyed {
		return s.viewLocked(n), nil
	}
	n.State = NodeDestroyed
	n.DestroyedAt = &now
	if err := s.store.saveLocked(); err != nil {
		return NodeView{}, err
	}
	return s.viewLocked(n), nil
}

// NodeView reports one node.
func (s *Service) NodeView(nodeID string) (NodeView, bool) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	n := s.store.mu.f.lookupNode(nodeID)
	if n == nil {
		return NodeView{}, false
	}
	return s.viewLocked(n), true
}

// NextResult is the long-poll answer: the node's state plus queued directives,
// the node default egress proxy and whether the node is busy.
type NextResult struct {
	State      NodeView    `json:"state"`
	Directives []Directive `json:"directives"`
	// ScanProxy is the default egress proxy the launcher starts scan with
	// (CYBER_PROXY); empty means direct. It rides every answer so a launcher
	// that missed one still converges.
	ScanProxy string `json:"scan_proxy,omitempty"`
	// Busy says a run is executing on the node right now: the launcher must
	// not restart scan for a proxy change until it reads false.
	Busy bool `json:"busy,omitempty"`
}

// Next long-polls for directives until wait elapses or something changes.
// The state always comes back, so the launcher learns about revocation and
// destruction on the same channel it uses for instructions (docs/14 §7.2).
func (s *Service) Next(nodeID string, wait time.Duration) NextResult {
	deadline := time.Now().Add(wait)
	// The default egress proxy as this call began: a change during the wait is
	// an answer in its own right, even with no directive queued.
	s.mu.Lock()
	initial := s.scanProxy
	s.mu.Unlock()
	for {
		s.store.mu.Lock()
		n := s.store.mu.f.lookupNode(nodeID)
		if n == nil {
			s.store.mu.Unlock()
			return NextResult{}
		}
		view := s.viewLocked(n)
		s.store.mu.Unlock()

		s.mu.Lock()
		pending := append([]Directive(nil), s.queues[nodeID]...)
		wake := s.wake[nodeID]
		if wake == nil {
			wake = make(chan struct{})
			s.wake[nodeID] = wake
		}
		proxy := s.scanProxy
		s.mu.Unlock()
		if len(pending) > 0 || proxy != initial || time.Now().After(deadline) {
			// Consume what we hand out: the node persists the sequence and
			// redelivery of an old directive is a no-op on its side.
			if len(pending) > 0 {
				s.mu.Lock()
				s.queues[nodeID] = nil
				s.mu.Unlock()
			}
			// Busy is asked at answer time, not at the top of the call: a poll
			// that parked for most of its wait must not answer with a
			// half-minute-old view of the node.
			return NextResult{State: view, Directives: pending, ScanProxy: proxy, Busy: s.nodeIsBusy(nodeID)}
		}
		select {
		case <-wake:
		case <-time.After(minDuration(time.Second, time.Until(deadline))):
		}
	}
}

// Enqueue adds one directive for a node and wakes its long-poll.
func (s *Service) Enqueue(nodeID, op string) (Directive, error) {
	return s.enqueue(nodeID, Directive{Op: op})
}

func (s *Service) enqueue(nodeID string, d Directive) (Directive, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq[nodeID]++
	d.Seq = s.seq[nodeID]
	s.queues[nodeID] = append(s.queues[nodeID], d)
	s.wakeLocked(nodeID)
	return d, nil
}

func (s *Service) wakeLocked(nodeID string) {
	if ch := s.wake[nodeID]; ch != nil {
		close(ch)
		delete(s.wake, nodeID)
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if b < a {
		return b
	}
	return a
}

// newID draws 16 random bytes as lower-case hex (the node id shape of
// docs/14 §7).
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成节点标识：%w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
