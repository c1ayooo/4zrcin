package notify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 摘要模式：realtime 即时逐条发送；digest 攒批到期合成一条。
const (
	ModeRealtime = "realtime"
	ModeDigest   = "digest"
)

// DefaultDigestMinutes 是摘要周期的缺省值（分钟）。
const DefaultDigestMinutes = 30

// Channel 是一个通知通道：连接参数 + 独立勾选的通知内容类型。
// 存进 notify.json 的是它本身；Config 里的凭据明文落盘（与 4zreco 的 JSONB
// 同语义），API 回显时经 MaskConfig 掩码。
type ChannelConfig struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`
	Enabled bool           `json:"enabled"`
	Config  map[string]any `json:"config"`
	// Events 是本通道勾选接收的通知内容类型；空=全部接收。
	Events []string `json:"events"`
	// MinSeverity 是最低级别门槛，仅对携带级别的事件（vuln_recorded）生效。
	MinSeverity string    `json:"min_severity,omitempty"`
	Mode        string    `json:"mode"`         // realtime|digest
	RatePerMin  int       `json:"rate_per_min"` // 0=不限流
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Filter 把通道配置换算成匹配用的过滤条件。
func (c ChannelConfig) Filter() Filter {
	return Filter{Events: c.Events, MinSeverity: c.MinSeverity}
}

// File 是 notify.json 的落盘结构。
type File struct {
	Enabled       bool            `json:"enabled"` // 全局总开关
	BaseURL       string          `json:"base_url,omitempty"`
	DigestMinutes int             `json:"digest_minutes"`
	Channels      []ChannelConfig `json:"channels"`
}

// Store 是 notify.json 的带锁读写器，模式与 internal/settings 的 Store 一致：
// 缺文件即零配置启动，写盘走临时文件+改名保证原子性。
type Store struct {
	mu   sync.RWMutex
	path string
	f    File
}

// OpenStore 读取（或初始化）notify.json。
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.f = File{Enabled: true, DigestMinutes: DefaultDigestMinutes}
			if err := s.persist(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.f); err != nil {
		// 配置损坏不阻断启动：通知是副产物，宁可空配也不让 hub 起不来。
		s.f = File{Enabled: true, DigestMinutes: DefaultDigestMinutes}
	}
	if s.f.DigestMinutes <= 0 {
		s.f.DigestMinutes = DefaultDigestMinutes
	}
	return s, nil
}

// View 返回配置的深拷贝，供 API 序列化与引擎读取，避免共享可变状态。
func (s *Store) View() File {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.f
	out.Channels = make([]ChannelConfig, len(s.f.Channels))
	for i, c := range s.f.Channels {
		out.Channels[i] = cloneChannel(c)
	}
	return out
}

// Save 用传入的 File 整体替换配置并落盘。写路径的合法性校验（kind、事件类型、
// 凭据形状）由 API 层完成，这里只负责持久化。
func (s *Store) Save(f File) error {
	if f.DigestMinutes <= 0 {
		f.DigestMinutes = DefaultDigestMinutes
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f = f
	return s.persist()
}

// EnabledChannels 返回启用通道的拷贝，供引擎 fan-out。
func (s *Store) EnabledChannels() []ChannelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ChannelConfig
	for _, c := range s.f.Channels {
		if c.Enabled {
			out = append(out, cloneChannel(c))
		}
	}
	return out
}

// persist 要求调用方已持有写锁。
func (s *Store) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cloneChannel(c ChannelConfig) ChannelConfig {
	out := c
	out.Config = make(map[string]any, len(c.Config))
	for k, v := range c.Config {
		out.Config[k] = v
	}
	out.Events = append([]string(nil), c.Events...)
	return out
}

// NewID 生成通道/投递 id：毫秒时间戳 + 纳秒低位，够用且不引入额外依赖。
func NewID() string {
	return fmt.Sprintf("n%d%04x", time.Now().UnixMilli(), time.Now().UnixNano()%0xffff)
}
