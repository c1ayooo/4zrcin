// Package deploy runs SSH deployments of the node launcher (docs/14 §7.4).
//
// A job owns one enrollment for its whole life. The bootstrap script is
// rendered in memory, piped to the remote shell's stdin, and never written to
// the remote disk; the token therefore appears in no argv and no file on the
// host. Credentials live in the job's byte slices only, are never projected
// into a view, and are zeroed when the job ends.
package deploy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// Job states (docs/14 §7.4): the last two are terminal.
const (
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
)

// Step states, one per step in the view.
const (
	StepPending = "pending"
	StepActive  = "running"
	StepDone    = "done"
	StepFailed  = "failed"
)

// Step keys, in execution order.
const (
	stepConnect   = "connect"
	stepProbe     = "probe"
	stepBootstrap = "bootstrap"
	stepWait      = "wait"
	stepDone      = "done"
)

var stepOrder = []struct{ key, label string }{
	{stepConnect, "连接"},
	{stepProbe, "探测"},
	{stepBootstrap, "引导"},
	{stepWait, "等待回连"},
	{stepDone, "完成"},
}

// Defaults: the bootstrap downloads the scan binary on the host, which can take
// minutes on a home uplink, so the connect-back wait is generous on purpose.
const (
	DefaultTimeout = 25 * time.Minute
	DefaultWait    = 20 * time.Minute
	// maxLogLines bounds one job's log; the oldest lines are dropped first.
	maxLogLines = 600
	// maxLogLineLen truncates a single remote output line.
	maxLogLineLen = 4000
)

// Config assembles one deployment service.
type Config struct {
	// Enroll is the node record store the job creates its enrollment in.
	Enroll *enroll.Service
	// Dist renders the bootstrap script and knows whether the launcher is
	// distributable at all.
	Dist *enroll.Dist
	// DataDir roots the pinned host keys (ssh-host-keys.json). Empty keeps
	// the pins in memory, which is what tests want.
	DataDir string
	// Timeout bounds the whole job; Wait bounds the connect-back step.
	Timeout time.Duration
	Wait    time.Duration
	Now     func() time.Time
}

// Options is one deployment request. Secrets arrive as strings from JSON and
// are copied into the job's wipeable byte slices immediately.
type Options struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	Password     string `json:"password,omitempty"`
	PrivateKey   string `json:"private_key,omitempty"`
	Passphrase   string `json:"passphrase,omitempty"`
	SudoPassword string `json:"sudo_password,omitempty"`

	Name       string `json:"name,omitempty"`
	Recipe     string `json:"recipe,omitempty"`
	TTLMinutes int    `json:"ttl_minutes,omitempty"`
}

// StepView is one step of the deployment.
type StepView struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// LogLine is one line of remote output, attributed to the step that produced
// it. Secrets never reach this shape.
type LogLine struct {
	At   time.Time `json:"at"`
	Step string    `json:"step"`
	Text string    `json:"text"`
}

// JobView is the operator-facing projection of a job. It deliberately cannot
// carry credentials, the bootstrap script or the enrollment token.
type JobView struct {
	ID           string     `json:"id"`
	EnrollmentID string     `json:"enrollment_id"`
	Host         string     `json:"host"`
	Port         int        `json:"port"`
	User         string     `json:"user"`
	State        string     `json:"state"`
	Steps        []StepView `json:"steps"`
	Log          []LogLine  `json:"log"`
	Error        string     `json:"error,omitempty"`
	OS           string     `json:"os,omitempty"`
	Arch         string     `json:"arch,omitempty"`
	NodeID       string     `json:"node_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// Service owns the live jobs and the pinned SSH host keys.
type Service struct {
	cfg Config

	// onFinish is the operator-facing hook (IM notify): exactly one call per
	// job, after its terminal state is set. It must not block the job.
	onFinish func(JobView)

	mu   sync.Mutex
	jobs map[string]*job
	pins map[string]string // host:port → SHA256 fingerprint
}

// New builds the service and loads the pinned host keys.
func New(cfg Config) (*Service, error) {
	if cfg.Enroll == nil || cfg.Dist == nil {
		return nil, errors.New("部署服务需要接入记录与产物分发")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Wait <= 0 {
		cfg.Wait = DefaultWait
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Service{
		cfg:  cfg,
		jobs: map[string]*job{},
		pins: map[string]string{},
	}
	if err := s.loadPins(); err != nil {
		return nil, err
	}
	return s, nil
}

// Start validates the request, opens the enrollment, renders the bootstrap
// script and returns the job before it runs. Validation failures create no
// record.
//
// A job owns a fresh enrollment for its whole life: the plaintext token exists
// only in the job's memory, so there is never a shared record that two jobs
// could race over. Retrying is a new invitation (the rail revokes the previous
// one when it is still pending).
func (s *Service) Start(o Options) (JobView, error) {
	host := strings.TrimSpace(o.Host)
	if host == "" {
		return JobView{}, errors.New("需要主机地址")
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return JobView{}, errors.New("主机地址不能含空白")
	}
	port := o.Port
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return JobView{}, fmt.Errorf("端口 %d 不在 1–65535", port)
	}
	user := strings.TrimSpace(o.User)
	if user == "" {
		user = "root"
	}
	if !validUser(user) {
		return JobView{}, errors.New("用户名不合法")
	}
	if o.Password == "" && o.PrivateKey == "" {
		return JobView{}, errors.New("需要密码或私钥")
	}
	if len(o.Password) > 512 || len(o.PrivateKey) > 32<<10 || len(o.Passphrase) > 512 || len(o.SudoPassword) > 512 {
		return JobView{}, errors.New("凭据字段过长")
	}
	if strings.ContainsAny(o.SudoPassword, "\r\n") {
		return JobView{}, errors.New("sudo 口令不能含换行")
	}
	if o.PrivateKey != "" {
		if _, err := parseSigner([]byte(o.PrivateKey), []byte(o.Passphrase)); err != nil {
			return JobView{}, err
		}
	}

	created, err := s.cfg.Enroll.Create(enroll.CreateOptions{
		Name: o.Name, Recipe: o.Recipe, Deploy: enroll.DeploySSH, TTLMinutes: o.TTLMinutes,
	})
	if err != nil {
		return JobView{}, err
	}
	script, err := s.cfg.Dist.Script(s.cfg.Enroll.PublicURL(), created.Token)
	if err != nil {
		_ = s.cfg.Enroll.RevokeEnrollment(created.ID)
		return JobView{}, err
	}

	j := newJob(s, o, host, port, user, created.ID, script)
	s.mu.Lock()
	s.jobs[j.id] = j
	s.mu.Unlock()
	go j.run()

	return j.view(), nil
}

// SetOnFinish registers the job-finished hook (IM notify). One call per
// terminal job; nil views are never passed.
func (s *Service) SetOnFinish(fn func(JobView)) {
	s.mu.Lock()
	s.onFinish = fn
	s.mu.Unlock()
}

// onFinishHook snapshots the hook under the same lock the setter uses.
func (s *Service) onFinishHook() func(JobView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onFinish
}

// Get reports one job.
func (s *Service) Get(id string) (JobView, bool) {
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j == nil {
		return JobView{}, false
	}
	return j.view(), true
}

func validUser(user string) bool {
	if len(user) > 64 {
		return false
	}
	for _, r := range user {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '@':
		default:
			return false
		}
	}
	return true
}

func newJobID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ---- host key pins ----

func pinsPath(dataDir string) string {
	return filepath.Join(dataDir, "ssh-host-keys.json")
}

func (s *Service) loadPins() error {
	if strings.TrimSpace(s.cfg.DataDir) == "" {
		return nil
	}
	raw, err := os.ReadFile(pinsPath(s.cfg.DataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 SSH 主机密钥记录：%w", err)
	}
	var file struct {
		Hosts map[string]string `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("SSH 主机密钥记录无法解析：%w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range file.Hosts {
		s.pins[k] = v
	}
	return nil
}

// pinFor returns the fingerprint recorded for addr.
func (s *Service) pinFor(addr string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp, ok := s.pins[addr]
	return fp, ok
}

// rememberPin records the first fingerprint seen for addr. A host is only
// trusted on first use: a changed key is a hard failure, not a silent update.
func (s *Service) rememberPin(addr, fp string) error {
	s.mu.Lock()
	if known, ok := s.pins[addr]; ok {
		s.mu.Unlock()
		if known != fp {
			return fmt.Errorf("主机密钥不匹配：此前记录 %s，本次收到 %s", known, fp)
		}
		return nil
	}
	s.pins[addr] = fp
	dataDir := s.cfg.DataDir
	snapshot := make(map[string]string, len(s.pins))
	for k, v := range s.pins {
		snapshot[k] = v
	}
	s.mu.Unlock()

	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	raw, err := json.Marshal(struct {
		Hosts map[string]string `json:"hosts"`
	}{Hosts: snapshot})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataDir, ".ssh-host-keys-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) //nolint:errcheck // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, pinsPath(dataDir))
}

func (j *job) addr() string {
	return net.JoinHostPort(j.host, fmt.Sprint(j.port))
}
