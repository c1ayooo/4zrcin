package api

// The webshell ledger's HTTP surface (docs/18). The operator registers and
// operates shells here; an operation runs on the shell's node over the
// directive channel, so the traffic to the target leaves that host, not the
// console. Nothing in a view carries the credential: the sealed form stays in
// the ledger, and the plaintext is handed out only by the credential route,
// which exists for the harness tools that run a shell themselves.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/aop"
	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
	"github.com/c1ayooo/4zwebshell/webshell"
)

// ShellRunner runs one shell operation on a node over the directive channel.
// The hub supplies the enrollment service; a bare test server leaves it nil and
// directive-node operations answer 503 instead of pretending to have run
// somewhere. ShellDirectiveNode reports whether nodeID takes operations over
// that channel (a tunnel node); everything else goes over the node's own AOP
// host or nowhere.
type ShellRunner interface {
	RunShell(ctx context.Context, nodeID string, task enroll.ShellTask) (webshell.Result, error)
	ShellDirectiveNode(nodeID string) bool
}

// shellView is a ledger row as the API shows it. The sealed credential is not
// part of it: nothing outside the credential route sees even the ciphertext.
type shellView struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	URL           string     `json:"url"`
	Type          string     `json:"type"`
	Protocol      string     `json:"protocol"`
	PayloadSHA256 string     `json:"payload_sha256,omitempty"`
	NodeID        string     `json:"node_id,omitempty"`
	Target        string     `json:"target,omitempty"`
	Source        string     `json:"source"`
	RunID         string     `json:"run_id,omitempty"`
	Status        string     `json:"status"`
	Note          string     `json:"note,omitempty"`
	OSKind        string     `json:"os_kind,omitempty"`
	LastChecked   *time.Time `json:"last_checked,omitempty"`
	LastOutput    string     `json:"last_output,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func newShellView(sh model.Shell) shellView {
	return shellView{
		ID: sh.ID, Name: sh.Name, URL: sh.URL, Type: sh.Type, Protocol: sh.Protocol,
		PayloadSHA256: sh.PayloadSHA256, NodeID: sh.NodeID, Target: sh.Target,
		Source: sh.Source, RunID: sh.RunID, Status: sh.Status, Note: sh.Note,
		OSKind: sh.OSKind, LastChecked: sh.LastChecked, LastOutput: sh.LastOutput,
		CreatedAt: sh.CreatedAt, UpdatedAt: sh.UpdatedAt,
	}
}

func (s *Server) listShells(w http.ResponseWriter, _ *http.Request) {
	rows := s.Store.ListShells()
	out := make([]shellView, 0, len(rows))
	for _, sh := range rows {
		out = append(out, newShellView(sh))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"shells": out})
}

// shellInput is a registration. With no connection material the platform mints
// the shell — a fresh key, a fresh carrier and the file to deploy; with a key,
// a carrier or a password it adopts a shell already on the target.
type shellInput struct {
	URL      string `json:"url"`
	Type     string `json:"type,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Name     string `json:"name,omitempty"`
	NodeID   string `json:"node_id,omitempty"`
	Target   string `json:"target,omitempty"`
	Note     string `json:"note,omitempty"`
	Key      string `json:"key,omitempty"`
	Carrier  string `json:"carrier,omitempty"`
	Password string `json:"password,omitempty"`
}

// shellPayload is the deployed file of a minted shell. It carries the key, so
// it is shown once, on registration, and never cached.
type shellPayload struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
	SHA256   string `json:"sha256"`
}

func (s *Server) registerShell(w http.ResponseWriter, r *http.Request) {
	s.registerShellAs(w, r, model.ShellSourceOperator, "")
}

// registerShellAs is the registration both surfaces share: the operator's
// (source operator) and the harness tool's (source model, with the run).
func (s *Server) registerShellAs(w http.ResponseWriter, r *http.Request, source, runID string) {
	var in shellInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "登记请求 JSON 无效或过大")
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	in.Type = strings.ToLower(strings.TrimSpace(in.Type))
	if in.Type == "" {
		in.Type = model.ShellTypePHP
	}
	protocol := strings.ToLower(strings.TrimSpace(in.Protocol))
	if protocol == "" {
		protocol = model.ShellProtocolV2
	}
	if !s.ShellSealer.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "本实例未配置凭据封存，无法登记 webshell")
		return
	}

	var (
		cred   webshell.Credential
		minted *shellPayload
		sum    string
		err    error
	)
	switch {
	case strings.TrimSpace(in.Key) != "" || strings.TrimSpace(in.Carrier) != "" || strings.TrimSpace(in.Password) != "":
		cred, err = adoptCredential(in, protocol)
	default:
		var payload webshell.ShellPayload
		cred, payload, err = mintCredential(in.Type, protocol)
		if err == nil {
			minted = &shellPayload{Filename: payload.Filename, Content: payload.Content, SHA256: payload.SHA256}
			sum = payload.SHA256
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sealed, err := sealCredential(s.ShellSealer, cred)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "封存连接凭据失败："+err.Error())
		return
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "shell." + in.Type
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		target = in.URL
	}
	row, err := s.Store.CreateShell(model.Shell{
		Name: name, URL: in.URL, Type: in.Type, Protocol: protocol,
		Secret: sealed, Carrier: cred.Carrier, PayloadSHA256: sum,
		NodeID: strings.TrimSpace(in.NodeID), Target: target, Source: source, RunID: runID,
		Status: model.ShellStatusUnknown, Note: strings.TrimSpace(in.Note),
	})
	switch {
	case errors.Is(err, store.ErrShellDuplicate):
		writeErr(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if runID != "" {
		s.recordActionFor(runID, "shell_register", row.ID, row.URL)
	} else {
		s.recordAction("shell_register", row.ID, row.URL)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"shell": newShellView(row), "payload": minted})
}

// adoptCredential rebuilds the connection material of a shell already on the
// target from what the caller knows about it.
func adoptCredential(in shellInput, protocol string) (webshell.Credential, error) {
	switch protocol {
	case model.ShellProtocolV2:
		key, err := webshell.DecodeShellKey(strings.TrimSpace(in.Key))
		if err != nil {
			return webshell.Credential{}, errors.New("v2 密钥不是合法的 base64")
		}
		return webshell.Credential{Type: in.Type, Protocol: protocol, Key: key, Carrier: strings.TrimSpace(in.Carrier)}, nil
	case model.ShellProtocolLegacy:
		return webshell.Credential{Type: in.Type, Protocol: protocol, Password: strings.TrimSpace(in.Password)}, nil
	}
	return webshell.Credential{}, fmt.Errorf("协议只能是 v2 或 legacy（收到 %q）", protocol)
}

// mintCredential builds a new v2 shell. The legacy protocol is plaintext and
// exists only to adopt a shell that is already on the target: nothing mints
// one.
func mintCredential(shellType, protocol string) (webshell.Credential, webshell.ShellPayload, error) {
	switch protocol {
	case model.ShellProtocolV2:
		return webshell.NewV2Credential(shellType)
	case model.ShellProtocolLegacy:
		return webshell.Credential{}, webshell.ShellPayload{}, errors.New("legacy 协议只用于接入已有的壳：请提供连接口令（新壳请用 v2）")
	}
	return webshell.Credential{}, webshell.ShellPayload{}, fmt.Errorf("协议只能是 v2 或 legacy（收到 %q）", protocol)
}

// sealCredential stores the whole connection material as one sealed blob, so a
// v2 key and a legacy password are sealed and opened the same way.
func sealCredential(s webshell.Sealer, cred webshell.Credential) (string, error) {
	raw, err := json.Marshal(cred)
	if err != nil {
		return "", err
	}
	return s.Seal(raw)
}

func openCredential(s webshell.Sealer, sh model.Shell) (webshell.Credential, error) {
	raw, err := s.Open(sh.Secret)
	if err != nil {
		return webshell.Credential{}, fmt.Errorf("解封 %s 的连接凭据失败：实例密钥可能已轮换，请重新登记", sh.URL)
	}
	var cred webshell.Credential
	if err := json.Unmarshal(raw, &cred); err != nil || cred.Type != sh.Type || cred.Protocol != sh.Protocol {
		return webshell.Credential{}, fmt.Errorf("台账里 %s 的连接凭据与登记不一致", sh.URL)
	}
	return cred, nil
}

func (s *Server) updateShell(w http.ResponseWriter, r *http.Request) {
	// Pointers tell "left alone" from "cleared".
	var in struct {
		Name   *string `json:"name"`
		NodeID *string `json:"node_id"`
		Target *string `json:"target"`
		Note   *string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "更新请求 JSON 无效或过大")
		return
	}
	row, err := s.Store.UpdateShell(r.PathValue("id"), func(sh *model.Shell) {
		if in.Name != nil {
			sh.Name = strings.TrimSpace(*in.Name)
		}
		if in.NodeID != nil {
			sh.NodeID = strings.TrimSpace(*in.NodeID)
		}
		if in.Target != nil {
			sh.Target = strings.TrimSpace(*in.Target)
		}
		if in.Note != nil {
			sh.Note = strings.TrimSpace(*in.Note)
		}
	})
	switch {
	case errors.Is(err, store.ErrShellMissing):
		writeErr(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, newShellView(row))
}

// forgetShell removes the row from the ledger. It does not touch the target:
// the file there is removed by the shell's own self-delete (the remove
// operation), and a forgotten shell left on the target is the operator's call.
func (s *Server) forgetShell(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteShell(id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.recordAction("shell_forget", id, "")
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// runShellOp executes one operation on the shell's node. An operation that ran
// — including one the shell failed — answers 200 with its result and error; a
// request that no node took answers with an error status and changes nothing.
func (s *Server) runShellOp(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.Store.GetShell(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, store.ErrShellMissing.Error())
		return
	}
	var in struct {
		NodeID string      `json:"node_id,omitempty"`
		Op     webshell.Op `json:"op"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "操作请求 JSON 无效或过大")
		return
	}
	if err := webshell.Validate(in.Op); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	nodeID := strings.TrimSpace(in.NodeID)
	if nodeID == "" {
		nodeID = sh.NodeID
	}
	if nodeID == "" {
		// No binding names the console's own host — the default vantage.
		nodeID = s.LocalNode
	}
	if nodeID == "" {
		writeErr(w, http.StatusBadRequest, "这个 shell 没有绑定节点且本机宿主未配置：登记时选择执行节点，或在请求里指定 node_id")
		return
	}
	cred, err := openCredential(s.ShellSealer, sh)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	switch {
	case nodeID == s.LocalNode:
		s.runCommandShellOpHTTP(w, r, sh, cred, in.Op, nodeID)
		return
	case s.ShellRunner != nil && s.ShellRunner.ShellDirectiveNode(nodeID):
		// A tunnel node: the operation goes over the directive channel.
	case s.HasNode(nodeID):
		// A direct AOP node: the same command channel as the local host — the
		// case directory is shared with it, so the ticket file is reachable.
		s.runCommandShellOpHTTP(w, r, sh, cred, in.Op, nodeID)
		return
	default:
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("节点 %s 不存在，不能执行 shell 操作", nodeID))
		return
	}
	if s.ShellRunner == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Sprintf("节点 %s 不是本机，而本实例没有节点指令通道（docs/18）：本机操作请留空 node_id", nodeID))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), enroll.ShellWait)
	defer cancel()
	res, err := s.ShellRunner.RunShell(ctx, nodeID, enroll.ShellTask{URL: sh.URL, Credential: cred, Op: in.Op})
	if err != nil && nodeNeverRan(err) {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.recordShellOutcome(sh.ID, in.Op, res, err)
	row, _ := s.Store.GetShell(sh.ID)
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"result": res, "shell": newShellView(row)}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// nodeNeverRan reports an error that means no node executed the operation.
func nodeNeverRan(err error) bool {
	return errors.Is(err, enroll.ErrShellOffline) || errors.Is(err, enroll.ErrShellBusy) ||
		errors.Is(err, enroll.ErrShellTimeout) || errors.Is(err, enroll.ErrNoSuchNode) ||
		errors.Is(err, context.Canceled)
}

// recordShellOutcome keeps the ledger current after an operation. Only what
// the operation proves is written: a ping proves liveness, an exec that
// answered proves the shell is alive and keeps its output, a removal proves
// the file is gone. A failed exec proves nothing about liveness — the command
// may have failed on a healthy shell — and leaves the status alone.
func (s *Server) recordShellOutcome(id string, op webshell.Op, res webshell.Result, err error) {
	if _, werr := s.Store.UpdateShell(id, func(sh *model.Shell) {
		now := time.Now()
		sh.LastChecked = &now
		switch op.Kind {
		case webshell.OpPing:
			if res.Alive {
				sh.Status = model.ShellStatusAlive
			} else {
				sh.Status = model.ShellStatusDead
			}
		case webshell.OpExec:
			sh.LastOutput = webshell.TruncateOutput(res.Output, model.MaxShellOutput)
			if err == nil {
				sh.Status = model.ShellStatusAlive
			}
		case webshell.OpRemove:
			switch {
			case res.Removed:
				sh.Status = model.ShellStatusRemoved
			case err != nil:
				sh.Status = model.ShellStatusRemoveFailed
				note := res.Output
				if strings.TrimSpace(note) == "" {
					note = res.Message
				}
				sh.LastOutput = webshell.TruncateOutput(note, model.MaxShellOutput)
			}
		}
	}); werr != nil {
		// The operation ran and its result was returned; say so when the
		// ledger could not keep it, instead of dropping it quietly.
		log.Printf("webshell %s：操作结果写回台账失败：%v", id, werr)
	}
}

// registerShellFromSession is the participant face's registration: the run is
// the source, taken from the authenticated identity, so the ledger says a
// model registered the shell.
func (s *Server) registerShellFromSession(w http.ResponseWriter, r *http.Request) {
	s.registerShellAs(w, r, model.ShellSourceModel, s.caller(r).runID)
}

// recordShellObserved is the participant face's receipt: the session's tool
// ran the operation itself and reports what it saw. The status derivation is
// the operator path's, so a shell cannot read alive on one face and dead on the
// other.
func (s *Server) recordShellObserved(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.Store.GetShell(id); !ok {
		writeErr(w, http.StatusNotFound, store.ErrShellMissing.Error())
		return
	}
	var in struct {
		Op     webshell.Op     `json:"op"`
		Result webshell.Result `json:"result"`
		Error  string          `json:"error,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "观察结果 JSON 无效或过大")
		return
	}
	if err := webshell.Validate(in.Op); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var runErr error
	if msg := strings.TrimSpace(in.Error); msg != "" {
		runErr = errors.New(msg)
	}
	s.recordShellOutcome(id, in.Op, in.Result, runErr)
	row, _ := s.Store.GetShell(id)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, newShellView(row))
}

// shellCredential hands out the plaintext connection material of one shell.
// It exists for the harness tools, which run a shell themselves; the response
// is never cached.
func (s *Server) shellCredential(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.Store.GetShell(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, store.ErrShellMissing.Error())
		return
	}
	cred, err := openCredential(s.ShellSealer, sh)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, http.StatusOK, cred)
}

// ---- local vantage: the console's own host, through the AOP direct-command
// channel — the same operator-driven, non-model path browser control uses
// (docs/18 §3.1). The console writes a one-time task file into the case
// directory and sends "!<zrcin> shell run --task …" on a dedicated control
// session; the executor runs the operation in the scan process, burns the
// ticket, and answers with one receipt JSON. The credential travels in the
// task file only: 0600 inside the case directory, deleted before the command
// returns, never in argv and never in a log line.
// ----

// shellSessionHost is the slice of an AOP host the local shell path needs. The
// indirection exists so the session bookkeeping is testable without a harness
// host.
type shellSessionHost interface {
	NodeID(ctx context.Context, agent string) (string, error)
	OpenSession(ctx context.Context, nodeID, title, workDir string) (string, error)
	Watch(ctx context.Context, sessionID, afterCursor string) error
	Command(ctx context.Context, sessionID, line string) (string, error)
	Cursor() string
	CloseSession(ctx context.Context, sessionID, reason string) error
	Close() error
}

// newShellSessionHost dials the host. Tests replace it.
var newShellSessionHost = func(ctx context.Context, base, token string) (shellSessionHost, error) {
	conn, err := aop.New(base, token).Dial(ctx)
	if err != nil {
		return nil, err
	}
	return aopShellHost{conn: conn, client: aop.New(base, token)}, nil
}

// aopShellHost adapts the AOP client to shellSessionHost.
type aopShellHost struct {
	conn   *aop.Conn
	client *aop.Client
}

func (h aopShellHost) NodeID(ctx context.Context, agent string) (string, error) {
	return h.client.NodeID(ctx, agent)
}

func (h aopShellHost) OpenSession(ctx context.Context, nodeID, title, workDir string) (string, error) {
	session, err := h.conn.OpenSession(ctx, nodeID, title, workDir)
	if err != nil {
		return "", err
	}
	return session.Id, nil
}

func (h aopShellHost) Watch(ctx context.Context, sessionID, afterCursor string) error {
	return h.conn.Watch(ctx, sessionID, afterCursor)
}

func (h aopShellHost) Command(ctx context.Context, sessionID, line string) (string, error) {
	return h.conn.Command(ctx, sessionID, line)
}

func (h aopShellHost) Cursor() string { return h.conn.Cursor() }

func (h aopShellHost) CloseSession(ctx context.Context, sessionID, reason string) error {
	return h.conn.CloseSession(ctx, sessionID, reason)
}

func (h aopShellHost) Close() error { return h.conn.Close() }

// shellLocalMu serialises local shell operations: the control session is
// dedicated to one command at a time (aop.Command's contract).
var shellLocalMu sync.Mutex

// shellControl is the persisted control session: the endpoint it belongs to,
// the session id and the watch cursor. Like the browser control marker it
// lives in the case directory and survives a console restart.
type shellControl struct {
	Endpoint string `json:"endpoint"`
	Session  string `json:"session"`
	Cursor   string `json:"cursor,omitempty"`
}

// shellControlPath is per node: every AOP host keeps its own control session.
func (s *Server) shellControlPath(nodeID string) string {
	return filepath.Join(s.CaseRoot, ".shells", "control-session-"+nodeID+".json")
}

func (s *Server) loadShellControl(nodeID string) shellControl {
	var control shellControl
	if raw, err := os.ReadFile(s.shellControlPath(nodeID)); err == nil {
		_ = json.Unmarshal(raw, &control)
	}
	return control
}

func (s *Server) saveShellControl(nodeID string, control shellControl) {
	if err := os.MkdirAll(filepath.Dir(s.shellControlPath(nodeID)), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(control)
	if err == nil {
		_ = os.WriteFile(s.shellControlPath(nodeID), raw, 0o600)
	}
}

func (s *Server) clearShellControl(nodeID string) {
	_ = os.Remove(s.shellControlPath(nodeID))
}

// runCommandShellOpHTTP is the command-channel branch of
// POST /api/shells/{id}/op (the console's own host and direct AOP nodes): task
// file, control session, receipt, ledger.
func (s *Server) runCommandShellOpHTTP(w http.ResponseWriter, r *http.Request, sh model.Shell, cred webshell.Credential, op webshell.Op, nodeID string) {
	exe, err := os.Executable()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法定位 zrcin 可执行文件："+err.Error())
		return
	}
	dir := filepath.Join(s.CaseRoot, ".shells")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, "创建任务目录失败："+err.Error())
		return
	}
	reqID := store.NewID("shreq")
	taskPath := filepath.Join(dir, reqID+".json")
	task := enroll.ShellTask{RequestID: reqID, URL: sh.URL, Credential: cred, Op: op}
	if raw, err := json.Marshal(task); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	} else if err := os.WriteFile(taskPath, raw, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, "写入任务文件失败："+err.Error())
		return
	}
	// The executor burns the ticket before running; this removal only covers
	// the console failing before the command even reaches the host.
	defer func() { _ = os.Remove(taskPath) }()

	ctx, cancel := context.WithTimeout(r.Context(), enroll.ShellWait)
	defer cancel()
	receipt, err := s.runCommandShellOp(ctx, nodeID, exe, taskPath)
	if err != nil {
		// A transport failure means the operation may or may not have reached
		// the shell; nothing was recorded and the caller decides.
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if receipt.Result == nil {
		writeErr(w, http.StatusBadGateway, "本机回执缺少操作结果"+firstLine(receipt.Error))
		return
	}
	var runErr error
	if msg := strings.TrimSpace(receipt.Error); msg != "" {
		runErr = errors.New(msg)
	}
	s.recordShellOutcome(sh.ID, op, *receipt.Result, runErr)
	row, _ := s.Store.GetShell(sh.ID)
	out := map[string]any{"result": *receipt.Result, "shell": newShellView(row)}
	if runErr != nil {
		out["error"] = runErr.Error()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// runLocalShellOp sends one command on the control session and returns the
// parsed receipt. The session is reopened once when a stale marker points at a
// session the host no longer knows; a command failure is final (the operation
// may have run, so no automatic retry).
// runCommandShellOp sends one command on the node's control session and returns
// the parsed receipt. The session is reopened once when a stale marker points
// at a session the host no longer knows; a command failure is final (the
// operation may have run, so no automatic retry). The executor binary is the
// node's own $ZRCIN_BIN when the session exports one, and the console's own
// path otherwise.
func (s *Server) runCommandShellOp(ctx context.Context, nodeID, exe, taskPath string) (enroll.ShellResult, error) {
	if err := s.checkNode(nodeID); err != nil {
		return enroll.ShellResult{}, err
	}
	tn, ok := s.Exec.(executor.TerminalNode)
	if !ok {
		return enroll.ShellResult{}, errors.New("执行器没有节点宿主通道")
	}
	base, token, agent, ok := tn.TerminalEndpoint(nodeID)
	if !ok {
		return enroll.ShellResult{}, fmt.Errorf("节点 %s 的宿主不可用", nodeID)
	}
	shellLocalMu.Lock()
	defer shellLocalMu.Unlock()
	line := `!"${ZRCIN_BIN:-` + quoteNative(exe) + `}" shell run --task ` + quoteNative(taskPath)
	for attempt := 0; ; attempt++ {
		control := s.loadShellControl(nodeID)
		host, err := newShellSessionHost(ctx, base, token)
		if err != nil {
			return enroll.ShellResult{}, fmt.Errorf("本机宿主连接失败：%w", err)
		}
		if control.Session == "" {
			agentID, agentErr := host.NodeID(ctx, agent)
			if agentErr == nil {
				control.Session, agentErr = host.OpenSession(ctx, agentID, "4zrcin webshell control", s.CaseRoot)
			}
			if agentErr != nil {
				_ = host.Close()
				return enroll.ShellResult{}, fmt.Errorf("本机宿主会话建立失败：%w", agentErr)
			}
			control.Endpoint = base
			s.saveShellControl(nodeID, control)
		}
		if err := host.Watch(ctx, control.Session, control.Cursor); err != nil {
			// The host may have restarted under an old session id. Drop the
			// marker and reopen once; a failure on the fresh session is final.
			_ = host.CloseSession(ctx, control.Session, "webshell control reopen")
			_ = host.Close()
			s.clearShellControl(nodeID)
			if attempt == 0 {
				continue
			}
			return enroll.ShellResult{}, fmt.Errorf("本机宿主会话不可用：%w", err)
		}
		output, err := host.Command(ctx, control.Session, line)
		if err != nil {
			// The operation may have run: no retry. The next call starts from
			// a fresh session.
			s.clearShellControl(nodeID)
			_ = host.Close()
			return enroll.ShellResult{}, fmt.Errorf("本机命令未确认（操作可能已执行）：%w", err)
		}
		control.Cursor = host.Cursor()
		s.saveShellControl(nodeID, control)
		_ = host.Close()
		var receipt enroll.ShellResult
		if perr := json.Unmarshal([]byte(output), &receipt); perr != nil {
			return enroll.ShellResult{}, fmt.Errorf("本机回执无法解析：%v；输出：%s", perr, webshell.TruncateOutput(strings.TrimSpace(output), 2048))
		}
		return receipt, nil
	}
}
