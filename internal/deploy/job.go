package deploy

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// job is one live deployment: the SSH session, its steps, its log and the
// wipeable copies of the credentials.
type job struct {
	svc *Service

	id           string
	enrollmentID string
	host         string
	port         int
	user         string

	// Secrets. They never leave this struct: the view has no field for them,
	// and they are zeroed when the job ends.
	password     []byte
	privateKey   []byte
	passphrase   []byte
	sudoPassword []byte
	script       []byte

	createdAt time.Time
	timeout   time.Duration
	wait      time.Duration

	mu         sync.Mutex
	state      string
	steps      []stepState
	logs       []LogLine
	errText    string
	os         string
	arch       string
	nodeID     string
	finishedAt *time.Time
}

type stepState struct {
	key    string
	label  string
	state  string
	detail string
}

func newJob(s *Service, o Options, host string, port int, user, enrollmentID, script string) *job {
	id, err := newJobID()
	if err != nil {
		// crypto/rand failing means the process has bigger problems; the id
		// still has to be unique enough for the in-memory map.
		id = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	j := &job{
		svc:          s,
		id:           id,
		enrollmentID: enrollmentID,
		host:         host,
		port:         port,
		user:         user,
		password:     []byte(o.Password),
		privateKey:   []byte(o.PrivateKey),
		passphrase:   []byte(o.Passphrase),
		sudoPassword: []byte(o.SudoPassword),
		script:       []byte(script),
		createdAt:    s.cfg.Now().UTC(),
		timeout:      s.cfg.Timeout,
		wait:         s.cfg.Wait,
		state:        StateRunning,
	}
	for _, st := range stepOrder {
		j.steps = append(j.steps, stepState{key: st.key, label: st.label, state: StepPending})
	}
	return j
}

func (j *job) view() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	steps := make([]StepView, 0, len(j.steps))
	for _, s := range j.steps {
		steps = append(steps, StepView{Key: s.key, Label: s.label, State: s.state, Detail: s.detail})
	}
	logs := append([]LogLine(nil), j.logs...)
	return JobView{
		ID: j.id, EnrollmentID: j.enrollmentID, Host: j.host, Port: j.port, User: j.user,
		State: j.state, Steps: steps, Log: logs, Error: j.errText,
		OS: j.os, Arch: j.arch, NodeID: j.nodeID,
		CreatedAt: j.createdAt, FinishedAt: j.finishedAt,
	}
}

// step moves one step to a state and optional detail; the previous running
// step of the same key is replaced.
func (j *job) step(key, state, detail string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.steps {
		if j.steps[i].key == key {
			j.steps[i].state = state
			if detail != "" {
				j.steps[i].detail = detail
			}
			return
		}
	}
}

func (j *job) log(step, text string) {
	text = strings.TrimRight(text, "\r\n")
	if len(text) > maxLogLineLen {
		text = text[:maxLogLineLen] + "…"
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.logs = append(j.logs, LogLine{At: j.svc.cfg.Now().UTC(), Step: step, Text: text})
	if len(j.logs) > maxLogLines {
		j.logs = append([]LogLine(nil), j.logs[len(j.logs)-maxLogLines:]...)
	}
}

// finish ends the job: terminal state, secret wipe, guard release.
func (j *job) finish(state, errText string) {
	j.mu.Lock()
	if j.state == StateRunning {
		j.state = state
		j.errText = errText
		now := j.svc.cfg.Now().UTC()
		j.finishedAt = &now
	}
	j.mu.Unlock()
	j.wipeSecrets()
}

// fail marks the current step failed and the job failed.
func (j *job) fail(step string, err error) {
	msg := err.Error()
	j.step(step, StepFailed, msg)
	j.log(step, "失败："+msg)
	j.finish(StateFailed, msg)
}

func (j *job) wipeSecrets() {
	for _, b := range [][]byte{j.password, j.privateKey, j.passphrase, j.sudoPassword, j.script} {
		for i := range b {
			b[i] = 0
		}
	}
}

func (j *job) run() {
	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			j.fail(stepConnect, fmt.Errorf("部署任务异常：%v", r))
		}
	}()

	j.step(stepConnect, StepActive, "")
	j.log(stepConnect, "连接 "+j.addr()+"（用户 "+j.user+"）")
	client, err := j.connect(ctx)
	if err != nil {
		j.fail(stepConnect, err)
		return
	}
	defer client.Close() //nolint:errcheck
	j.step(stepConnect, StepDone, j.addr())

	j.step(stepProbe, StepActive, "")
	info, err := j.probe(ctx, client)
	if err != nil {
		j.fail(stepProbe, err)
		return
	}
	detail := strings.Join(info.facts, " · ")
	j.step(stepProbe, StepDone, detail)

	j.step(stepBootstrap, StepActive, "")
	if err := j.bootstrap(ctx, client, info); err != nil {
		j.fail(stepBootstrap, err)
		return
	}
	j.step(stepBootstrap, StepDone, "引导脚本已经由 stdin 执行")

	j.step(stepWait, StepActive, "")
	if err := j.waitOnline(ctx); err != nil {
		j.fail(stepWait, err)
		return
	}
	j.step(stepWait, StepDone, "节点已回连")
	j.step(stepDone, StepDone, "")
	j.finish(StateSucceeded, "")
}

// connect dials the host and completes the SSH handshake. Host keys are pinned
// on first use (TOFU) and a mismatch aborts the job before any credential is
// offered.
func (j *job) connect(ctx context.Context) (*ssh.Client, error) {
	methods, err := j.authMethods()
	if err != nil {
		return nil, err
	}
	addr := j.addr()
	cfg := &ssh.ClientConfig{
		User:            j.user,
		Auth:            methods,
		HostKeyCallback: j.hostKeyCallback(),
		Timeout:         15 * time.Second,
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败：%w", addr, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("SSH 握手失败：%w", err)
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func (j *job) hostKeyCallback() ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		sum := sha256.Sum256(key.Marshal())
		fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
		known, ok := j.svc.pinFor(j.addr())
		if !ok {
			if err := j.svc.rememberPin(j.addr(), fp); err != nil {
				return err
			}
			j.log(stepConnect, "首次连接，已记录主机密钥 "+fp)
			return nil
		}
		if known != fp {
			return fmt.Errorf("主机密钥与记录不一致（记录 %s，收到 %s）；如果主机密钥没有更换过，请检查是否有人拦截连接", known, fp)
		}
		return nil
	}
}

func (j *job) authMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if len(j.password) > 0 {
		methods = append(methods, ssh.Password(string(j.password)))
	}
	if len(j.privateKey) > 0 {
		signer, err := parseSigner(j.privateKey, j.passphrase)
		if err != nil {
			return nil, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("需要密码或私钥")
	}
	return methods, nil
}

func parseSigner(key, passphrase []byte) (ssh.Signer, error) {
	var (
		signer ssh.Signer
		err    error
	)
	if len(passphrase) > 0 {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
	} else {
		signer, err = ssh.ParsePrivateKey(key)
	}
	if err != nil {
		return nil, fmt.Errorf("私钥无法使用：%w", err)
	}
	return signer, nil
}

// hostInfo is what the probe learned. facts becomes the step's detail line.
type hostInfo struct {
	root      bool
	systemd   bool
	installed bool
	facts     []string
}

func (j *job) probe(ctx context.Context, client *ssh.Client) (hostInfo, error) {
	var info hostInfo

	uname, err := j.exec(ctx, client, "uname -s -m")
	if err != nil {
		return info, fmt.Errorf("探测系统失败：%w", err)
	}
	uname = strings.TrimSpace(uname)
	if uname == "" {
		return info, fmt.Errorf("探测系统失败：uname 没有输出")
	}
	j.log(stepProbe, "系统："+uname)
	info.facts = append(info.facts, uname)
	fields := strings.Fields(uname)
	if len(fields) > 0 {
		info.facts = append(info.facts, fields[0])
	}

	uid, err := j.exec(ctx, client, "id -u")
	if err != nil {
		return info, fmt.Errorf("探测用户失败：%w", err)
	}
	uid = strings.TrimSpace(uid)
	info.root = uid == "0"
	if info.root {
		j.log(stepProbe, "以 root 登录，引导脚本直接执行")
	} else {
		j.log(stepProbe, "以非 root 用户登录（uid "+uid+"），引导脚本经 sudo 执行")
		info.facts = append(info.facts, "sudo")
	}

	sys, err := j.exec(ctx, client, "if [ -d /run/systemd/system ]; then echo systemd; else echo nosystemd; fi")
	if err != nil {
		return info, fmt.Errorf("探测 systemd 失败：%w", err)
	}
	info.systemd = strings.TrimSpace(sys) == "systemd"
	if info.systemd {
		j.log(stepProbe, "systemd 可用：安装后注册为系统服务")
	} else {
		j.log(stepProbe, "没有 systemd：安装后以前台进程运行")
	}

	node, err := j.exec(ctx, client, "if [ -f /opt/zrcin-node/etc/node.json ]; then echo installed; else echo fresh; fi")
	if err != nil {
		return info, fmt.Errorf("探测既有安装失败：%w", err)
	}
	info.installed = strings.TrimSpace(node) == "installed"
	if info.installed {
		j.log(stepProbe, "发现既有安装：安装器会走修复路径")
		info.facts = append(info.facts, "已有安装")
	}
	return info, nil
}

// exec runs one short command and returns its output. The session is closed
// when ctx ends so a hung command cannot outlive the job.
func (j *job) exec(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close() //nolint:errcheck
	done := make(chan struct{}, 1)
	var (
		out []byte
		run error
	)
	go func() {
		out, run = session.Output(cmd)
		done <- struct{}{}
	}()
	select {
	case <-done:
		if run != nil {
			return string(out), fmt.Errorf("%s：%w（输出 %s）", cmd, run, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	case <-ctx.Done():
		session.Close() //nolint:errcheck
		return "", ctx.Err()
	}
}

// bootstrap pipes the rendered script to the remote shell. A non-root login
// goes through sudo; with a sudo password the first stdin line answers the
// prompt and the rest is the script, so the token stays out of argv.
func (j *job) bootstrap(ctx context.Context, client *ssh.Client, info hostInfo) error {
	cmd := "sh"
	if !info.root {
		if len(j.sudoPassword) > 0 {
			cmd = "sudo -S sh"
		} else {
			cmd = "sudo -n sh"
		}
	}
	j.log(stepBootstrap, "在远端执行 "+cmd+"，脚本经 stdin 送入（不落盘、不进 argv）")

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close() //nolint:errcheck
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	var streams sync.WaitGroup
	streams.Add(2)
	go func() { defer streams.Done(); j.stream(stepBootstrap, stdout) }()
	go func() { defer streams.Done(); j.stream(stepBootstrap, stderr) }()

	var stdin io.Reader = strings.NewReader(string(j.script))
	if !info.root && len(j.sudoPassword) > 0 {
		stdin = io.MultiReader(strings.NewReader(string(j.sudoPassword)+"\n"), stdin)
	}
	session.Stdin = stdin

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()
	select {
	case err := <-done:
		streams.Wait()
		if err != nil {
			return fmt.Errorf("引导脚本失败：%w", err)
		}
		return nil
	case <-ctx.Done():
		session.Close() //nolint:errcheck
		streams.Wait()
		return ctx.Err()
	}
}

// stream copies remote output into the job log line by line.
func (j *job) stream(step string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		j.log(step, line)
	}
}

// pollInterval is how often the connect-back wait re-reads the node records.
var pollInterval = time.Second

// waitOnline holds the job open until the new node reports in through its
// tunnel. The enrollment's state is the authority: the token is spent exactly
// when the tunnel handshake succeeds.
func (j *job) waitOnline(ctx context.Context) error {
	deadline := time.Now().Add(j.wait)
	nextNote := time.Now().Add(15 * time.Second)
	for {
		nodeID, settled := j.enrollmentNode()
		if settled {
			return fmt.Errorf("接入记录在节点回连前已失效（令牌过期或被撤销）；重新发起部署即可")
		}
		if nodeID != "" {
			if view, ok := j.svc.cfg.Enroll.NodeView(nodeID); ok && view.Online {
				j.mu.Lock()
				j.nodeID = nodeID
				j.mu.Unlock()
				j.log(stepWait, "节点已上线，隧道已建立（"+nodeID+"）")
				return nil
			}
		}
		if time.Now().After(deadline) {
			if nodeID != "" {
				return fmt.Errorf("等待回连超时：节点已经换取凭据（%s），但没有在 %s 内上线；检查主机的出站访问与启动器日志", nodeID, j.wait)
			}
			return fmt.Errorf("等待回连超时（%s）：引导脚本已执行，但平台没有收到节点回连；检查主机能否访问平台公网地址", j.wait)
		}
		if time.Now().After(nextNote) {
			j.log(stepWait, "仍在等待节点回连…")
			nextNote = time.Now().Add(15 * time.Second)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待回连被取消：%w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// enrollmentNode reports the node id this job's enrollment produced, and
// whether the record can no longer join (expired or revoked).
func (j *job) enrollmentNode() (nodeID string, settled bool) {
	for _, e := range j.svc.cfg.Enroll.ListEnrollments() {
		if e.ID != j.enrollmentID {
			continue
		}
		if e.NodeID != "" {
			return e.NodeID, false
		}
		switch e.State {
		case enroll.EnrollRevoked, enroll.EnrollExpired:
			return "", true
		}
		return "", false
	}
	return "", true
}
