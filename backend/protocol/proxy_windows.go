//go:build windows

package protocol

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// WinINET 的系统代理存在这里，也就是「设置 → 网络和 Internet → 代理」
// 开关背后那份配置。
const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// readSystemProxy 读 WinINET 注册表项。ProxyServer 两种写法：一个
// host:port 管全部协议，或 "http=…;https=…;socks=…" 按协议分别指定；
// ProxyOverride 是分号分隔的例外列表（支持 * 与 <local>）。
func readSystemProxy() proxyConfig {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return proxyConfig{}
	}
	defer key.Close()

	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return proxyConfig{}
	}
	raw, _, err := key.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(raw) == "" {
		return proxyConfig{}
	}
	cfg := parseProxyServer(raw)
	if override, _, err := key.GetStringValue("ProxyOverride"); err == nil {
		cfg.Bypass = strings.Split(override, ";")
	}
	return cfg
}

// parseProxyServer 解析 ProxyServer：单一 host:port 对全部协议生效；
// 带 "http=" 的按协议取，没写到的协议回落到 http 那一项（只写
// "http=127.0.0.1:7890" 的用户几乎总是想全走代理）。
func parseProxyServer(raw string) proxyConfig {
	raw = strings.TrimSpace(raw)
	cfg := proxyConfig{HTTP: raw, HTTPS: raw}
	if !strings.Contains(raw, "=") {
		return cfg
	}
	cfg = proxyConfig{}
	for _, part := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[1]) == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "http":
			cfg.HTTP = strings.TrimSpace(kv[1])
		case "https":
			cfg.HTTPS = strings.TrimSpace(kv[1])
		case "socks":
			cfg.SOCKS = strings.TrimSpace(kv[1])
		}
	}
	if cfg.HTTPS == "" {
		cfg.HTTPS = cfg.HTTP
	}
	return cfg
}
