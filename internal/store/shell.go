package store

// The webshell ledger (docs/18). A shell is control state in the same class as
// assets and operator requests: durable project state that the commander is
// never woken for (flushNoRev) and that never enters the content digest. The
// store holds the credential sealed (webshell.Sealer) and never sees it in
// plaintext; the views built on top of it never return the ciphertext.

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/model"
)

var (
	// ErrShellMissing reports an id that names no row; the API layer maps it.
	ErrShellMissing = errors.New("webshell 不存在")
	// ErrShellDuplicate reports a second live row for an address already in
	// the ledger. One URL serves one shell; two rows would leave "which
	// credential does this address take" ambiguous.
	ErrShellDuplicate = errors.New("该地址已在台账中")
)

// maxShellURL bounds one address.
const maxShellURL = 2048

// validateShell checks one row the way every write path sees it. Type,
// protocol, status and source are closed sets; a v2 row without a sealed key
// and a carrier could never be operated, so it is refused here rather than
// failing at the first command.
func validateShell(sh model.Shell) error {
	u, err := url.Parse(sh.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(sh.URL) > maxShellURL {
		return fmt.Errorf("webshell 地址必须是 http(s) 绝对地址且不超过 %d 字节", maxShellURL)
	}
	if !model.IsShellType(sh.Type) {
		return fmt.Errorf("webshell 类型只能是 php/jsp/aspx（收到 %q）", sh.Type)
	}
	if !model.IsShellProtocol(sh.Protocol) {
		return fmt.Errorf("webshell 协议只能是 v2/legacy（收到 %q）", sh.Protocol)
	}
	if strings.TrimSpace(sh.Secret) == "" {
		return fmt.Errorf("webshell %s 缺少封存凭据", sh.URL)
	}
	if sh.Protocol == model.ShellProtocolV2 && strings.TrimSpace(sh.Carrier) == "" {
		return fmt.Errorf("v2 webshell %s 缺少通信载体", sh.URL)
	}
	switch sh.Source {
	case model.ShellSourceOperator, model.ShellSourceModel:
	default:
		return fmt.Errorf("webshell 来源只能是 operator/model（收到 %q）", sh.Source)
	}
	if !model.IsShellStatus(sh.Status) {
		return fmt.Errorf("webshell 状态只能是 unknown/alive/dead/removed/residual/remove_failed（收到 %q）", sh.Status)
	}
	if len(sh.LastOutput) > model.MaxShellOutput {
		return fmt.Errorf("webshell 输出超过 %d 字节，请先截断", model.MaxShellOutput)
	}
	return nil
}

// CreateShell adds one row. A removed shell frees its address: a new shell may
// be deployed at the same path with a new key.
func (s *Store) CreateShell(sh model.Shell) (model.Shell, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh.URL = strings.TrimSpace(sh.URL)
	if err := validateShell(sh); err != nil {
		return model.Shell{}, err
	}
	for i := range s.db.Shells {
		existing := &s.db.Shells[i]
		if existing.URL == sh.URL && existing.Status != model.ShellStatusRemoved {
			return model.Shell{}, fmt.Errorf("%w：%s（%s）", ErrShellDuplicate, sh.URL, existing.ID)
		}
	}
	now := time.Now()
	sh.ID = s.uniqueIDLocked("sh")
	sh.CreatedAt, sh.UpdatedAt = now, now
	s.db.Shells = append(s.db.Shells, sh)
	if err := s.flushNoRev("shell"); err != nil {
		return model.Shell{}, err
	}
	return detachShell(sh), nil
}

// detachShell gives a handed-out row its own LastChecked: a caller editing
// what it was given must not reach the live ledger.
func detachShell(sh model.Shell) model.Shell {
	if sh.LastChecked != nil {
		checked := *sh.LastChecked
		sh.LastChecked = &checked
	}
	return sh
}

// UpdateShell mutates one row through fn. An unknown id is an error, not a
// silent no-op: a liveness flip that "succeeded" against nothing would hide
// its own failure. The mutation may not change the row's identity (url, type,
// protocol — a different file or wire protocol is a new shell) and the result
// is re-validated before it can commit; an invalid mutation restores the row.
func (s *Store) UpdateShell(id string, fn func(*model.Shell)) (model.Shell, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.db.Shells {
		if s.db.Shells[i].ID != id {
			continue
		}
		before := s.db.Shells[i]
		fn(&s.db.Shells[i])
		s.db.Shells[i].UpdatedAt = time.Now()
		after := s.db.Shells[i]
		if after.URL != before.URL || after.Type != before.Type || after.Protocol != before.Protocol {
			s.db.Shells[i] = before
			return model.Shell{}, fmt.Errorf("webshell %s 的身份字段（url/type/protocol）不可改，请删除后重新登记", id)
		}
		if err := validateShell(after); err != nil {
			s.db.Shells[i] = before
			return model.Shell{}, err
		}
		if err := s.flushNoRev("shell"); err != nil {
			return model.Shell{}, err
		}
		return detachShell(after), nil
	}
	return model.Shell{}, fmt.Errorf("%w：%s", ErrShellMissing, id)
}

// DeleteShell removes one row from the ledger. It only forgets the shell: the
// file on the target is removed by the shell's own self-delete channel.
func (s *Store) DeleteShell(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.db.Shells {
		if s.db.Shells[i].ID != id {
			continue
		}
		s.db.Shells = append(s.db.Shells[:i], s.db.Shells[i+1:]...)
		return s.flushNoRev("shell")
	}
	return fmt.Errorf("%w：%s", ErrShellMissing, id)
}

// ListShells returns every row in registration order, so a reload does not
// reshuffle the table the operator is reading.
func (s *Store) ListShells() []model.Shell {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Shell, len(s.db.Shells))
	for i, sh := range s.db.Shells {
		out[i] = detachShell(sh)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// GetShell resolves one row by id.
func (s *Store) GetShell(id string) (model.Shell, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sh := range s.db.Shells {
		if sh.ID == id {
			return detachShell(sh), true
		}
	}
	return model.Shell{}, false
}
