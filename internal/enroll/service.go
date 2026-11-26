package enroll

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Service is the record-keeping core of the node ingress (docs/15 §3): it
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
}

// Options is the platform-side configuration handed in by main. Everything the
// join response carries comes from here, so the launcher mirrors the control
// plane's own loopback addresses (docs/15 §3.3).
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
	// consequence of rotating that key (docs/15 §6).
	IssueKey []byte
	// Now is the clock seam for tests.
	Now func() time.Time
	// Dist reports the artifacts this platform can currently serve. Nil means
	// "nothing to offer yet" (the bootstrap ticket fills it in).
	Dist func() []DistEntry
}

// DistEntry is one downloadable node artifact (docs/15 §3.3).
type DistEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Directive is one queued instruction for a launcher (docs/15 §4.4). The
// sequence number persists on the node side so redelivery is idempotent.
type Directive struct {
	Seq uint64 `json:"seq"`
	Op  string `json:"op"` // "uninstall"
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
// only here (docs/15 §3.2).
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
	}
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
// for that is destroy (docs/15 §5).
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

// JoinRequest is the node's join payload.
type JoinRequest struct {
	InstallID string `json:"install_id"`
}

// JoinResponse is exactly the join response shape of docs/15 §3.3. The
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

// TunnelParams is what the launcher needs to open the tunnel (docs/15 §3.4).
type TunnelParams struct {
	Key  string `json:"key"`
	Path string `json:"path"`
}

// HubParams points the launcher's scan at the tunneled Hub.
type HubParams struct {
	Addr string `json:"addr"`
	Key  string `json:"key"`
}

// JoinOutcome classifies the four join results (docs/15 §3.3). The HTTP
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
		Tunnel:         TunnelParams{Key: hex.EncodeToString(s.opt.IssueKey), Path: "/tunnel"},
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
}

// OnlineWindow is how fresh the last long-poll/heartbeat must be for a node to
// read as online (docs/15 §3.2).
const OnlineWindow = 90 * time.Second

func (s *Service) viewLocked(n *node) NodeView {
	online := false
	if n.State != NodeDestroyed && n.State != NodeDestroying && n.LastSeenAt != nil {
		online = s.opt.Now().UTC().Sub(*n.LastSeenAt) <= OnlineWindow
	}
	state := n.State
	if state != NodeDestroying && state != NodeDestroyed {
		if online {
			state = NodeOnline
		} else {
			state = NodeOffline
		}
	}
	return NodeView{
		ID: n.ID, Name: n.Name, State: state, Recipe: n.Recipe, Deploy: n.Deploy,
		Hostname: n.Hostname, OS: n.OS, Arch: n.Arch,
		LastSeenAt: n.LastSeenAt, DestroyedAt: n.DestroyedAt, NodeBinPath: n.NodeBinPath,
		Online: online,
		// The file contract lands in ticket C1; until then a joined tunnel
		// node must say plainly that it cannot take intents (docs/15 §3.6).
		DispatchesOK: false,
		LastError:    n.LastError,
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
}

// Heartbeat applies one report and returns the node's fresh view. The first
// heartbeat moves a joining node out of the joining state.
func (s *Service) Heartbeat(nodeID string, rep Report) (NodeView, error) {
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

// NextResult is the long-poll answer: the node's state plus queued directives.
type NextResult struct {
	State      NodeView    `json:"state"`
	Directives []Directive `json:"directives"`
}

// Next long-polls for directives until wait elapses or something changes.
// The state always comes back, so the launcher learns about revocation and
// destruction on the same channel it uses for instructions (docs/15 §4.4).
func (s *Service) Next(nodeID string, wait time.Duration) NextResult {
	deadline := time.Now().Add(wait)
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
		s.mu.Unlock()
		if len(pending) > 0 || time.Now().After(deadline) {
			// Consume what we hand out: the node persists the sequence and
			// redelivery of an old directive is a no-op on its side.
			if len(pending) > 0 {
				s.mu.Lock()
				s.queues[nodeID] = nil
				s.mu.Unlock()
			}
			return NextResult{State: view, Directives: pending}
		}
		select {
		case <-wake:
		case <-time.After(minDuration(time.Second, time.Until(deadline))):
		}
	}
}

// Enqueue adds one directive for a node and wakes its long-poll.
func (s *Service) Enqueue(nodeID, op string) (Directive, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq[nodeID]++
	d := Directive{Seq: s.seq[nodeID], Op: op}
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
// docs/15 §3.2).
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成节点标识：%w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
