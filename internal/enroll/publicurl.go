package enroll

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePublicURL checks the --public-url value (docs/15 §3.1): an absolute
// http(s) URL with no path, query or fragment. A non-loopback, non-private
// host must be https — the tunnel and the credentials cross this URL, and the
// rem stream wrappers are not a security boundary (spec fact 14).
func ValidatePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("公网地址为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("公网地址无法解析：%w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("公网地址只支持 http 或 https，收到 %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("公网地址缺少主机")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("公网地址只写到主机与端口，不要带路径、查询或片段")
	}
	if u.Scheme == "http" && !isLocalHost(u.Host) {
		return "", errors.New("公网接入需要 TLS：在前面套 TLS 反代并把 /tunnel 的 WebSocket 升级头透传，或改用 https 地址；回环与私网地址才允许 http")
	}
	u.Path = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// isLocalHost reports whether host (host or host:port) resolves to loopback or
// a private range, where plain http is acceptable for a lab deployment.
func isLocalHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return false
}

// TunnelKeySize is the entropy of the instance tunnel key (docs/15 §3.1).
const TunnelKeySize = 32

// EnsureTunnelKey loads <data>/tunnel.key, creating it with 0600 on first use.
// The key is also the issue key for derived node credentials, so rotating it
// invalidates every existing node credential — the documented consequence.
func EnsureTunnelKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != TunnelKeySize {
			return nil, fmt.Errorf("%s 长度是 %d 字节，应为 %d；不会自动覆盖，请人工处理", path, len(raw), TunnelKeySize)
		}
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取 %s：%w", path, err)
	}
	key := make([]byte, TunnelKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成隧道密钥：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建 %s 目录：%w", filepath.Dir(path), err)
	}
	// O_EXCL: a concurrent process that created it first wins; reload instead
	// of overwriting a key other nodes may already hold.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return EnsureTunnelKey(path)
		}
		return nil, fmt.Errorf("写 %s：%w", path, err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close() //nolint:errcheck
		return nil, fmt.Errorf("写 %s：%w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("写 %s：%w", path, err)
	}
	return key, nil
}
