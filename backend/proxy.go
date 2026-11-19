package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 系统代理跟随：HTTP_PROXY 等环境变量优先（Go 默认行为），未命中再读
// 操作系统设置里的代理（Windows 注册表 / macOS scutil），都没有才直连。
// WebSocket 握手同样经 proxyForRequest；TCP 裸连接没有代理概念，保持直连。

// proxyConfig 是一份解析好的系统代理设置：按协议给出代理地址
//（host:port），Bypass 是不走代理的主机列表（支持 * 通配、<local> 与 CIDR）。
type proxyConfig struct {
	HTTP   string
	HTTPS  string
	SOCKS  string
	Bypass []string
}

// proxyForRequest 是 http.Transport.Proxy 的实现：环境变量优先，
// 然后才轮到系统代理。
func proxyForRequest(req *http.Request) (*url.URL, error) {
	viaEnv, err := http.ProxyFromEnvironment(req)
	if err != nil {
		return nil, err
	}
	if viaEnv != nil {
		return viaEnv, nil
	}
	return systemProxyURL(req), nil
}

// proxiedTransport clones the default transport and points its proxy lookup
// at proxyForRequest.
func proxiedTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxyForRequest
	return transport
}

// systemProxyURL 把系统代理设置换算成请求的代理 URL；被例外列表或
// 回环地址命中时返回 nil（直连）。只配置了 socks 时按 socks5 使用。
func systemProxyURL(req *http.Request) *url.URL {
	host := req.URL.Hostname()
	if host == "" || hostBypassed(host, cachedSystemProxy().Bypass) {
		return nil
	}
	cfg := cachedSystemProxy()
	addr, scheme := cfg.HTTP, "http"
	switch req.URL.Scheme {
	case "https":
		if cfg.HTTPS != "" {
			addr = cfg.HTTPS
		}
	}
	if addr == "" && cfg.SOCKS != "" {
		addr, scheme = cfg.SOCKS, "socks5"
	}
	if addr == "" {
		return nil
	}
	u, err := url.Parse(scheme + "://" + addr)
	if err != nil {
		return nil
	}
	return u
}

// proxyForSchemeHost 按协议与主机查代理（h2c 的 DialTLS、CONNECT 隧道用）。
func proxyForSchemeHost(scheme, host string) *url.URL {
	req := &http.Request{URL: &url.URL{Scheme: scheme, Host: host}}
	u, _ := proxyForRequest(req)
	return u
}

// hostBypassed 判断 host 是否应直连：回环地址与 localhost 恒直连，
// 再按系统的例外列表匹配（* 通配、前导点后缀、<local>、CIDR）。
func hostBypassed(host string, patterns []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return true
		}
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(pattern)), ".")
		if pattern == "" {
			continue
		}
		if pattern == "<local>" {
			// 不带点的主机名（内网短主机名）不走代理。
			if !strings.Contains(host, ".") {
				return true
			}
			continue
		}
		if bypassPatternMatches(pattern, host) {
			return true
		}
	}
	return false
}

// bypassPatternMatches 支持三种写法：精确主机、* 通配（头/尾/中间）、
// CIDR（如 macOS 例外列表里的 169.254/16）。
func bypassPatternMatches(pattern, host string) bool {
	if strings.Contains(pattern, "/") {
		if _, ipNet, err := net.ParseCIDR(pattern); err == nil {
			if ip := net.ParseIP(host); ip != nil {
				return ipNet.Contains(ip)
			}
			return false
		}
		// "169.254/16" 这类省零写法：补齐成 a.b.0.0/n 再试一次。
		parts := strings.SplitN(pattern, "/", 2)
		if padded := padIPv4(parts[0]); padded != "" {
			if _, ipNet, err := net.ParseCIDR(padded + "/" + parts[1]); err == nil {
				if ip := net.ParseIP(host); ip != nil {
					return ipNet.Contains(ip)
				}
			}
		}
		return false
	}
	if strings.HasPrefix(pattern, ".") {
		// 前导点：后缀匹配，".suffix.edu" 命中 suffix.edu 自身与所有子域。
		return host == pattern[1:] || strings.HasSuffix(host, pattern)
	}
	if i := strings.IndexByte(pattern, '*'); i >= 0 {
		return strings.HasPrefix(host, pattern[:i]) && strings.HasSuffix(host, pattern[i+1:]) && len(host) >= len(pattern)-1
	}
	return pattern == host
}

// padIPv4 把 "169.254" 补成 "169.254.0.0" 供 ParseCIDR 使用。
func padIPv4(prefix string) string {
	octets := strings.Split(prefix, ".")
	if len(octets) >= 4 || len(octets) < 1 {
		return ""
	}
	for len(octets) < 4 {
		octets = append(octets, "0")
	}
	return strings.Join(octets, ".")
}

var (
	systemProxyMu     sync.Mutex
	systemProxyCached proxyConfig
	systemProxyAt     time.Time
)

// cachedSystemProxy 每 5 秒重新读一次系统代理设置：改代理不用重启应用，
// 又不会每个请求都去读注册表 / 跑 scutil。
func cachedSystemProxy() proxyConfig {
	systemProxyMu.Lock()
	defer systemProxyMu.Unlock()
	if time.Since(systemProxyAt) < 5*time.Second {
		return systemProxyCached
	}
	systemProxyCached = readSystemProxy()
	systemProxyAt = time.Now()
	return systemProxyCached
}

// joinHostPort 组装代理地址，缺省端口按协议补 80/443。
func joinHostPort(host string, port, defaultPort int) string {
	if port <= 0 {
		port = defaultPort
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
}

// dialViaProxy 向 http(s) 代理发 CONNECT 建一条到 addr 的隧道（h2c 与代理
// 配合时用；代理 URL 带 userinfo 时做 Basic 认证）。
func dialViaProxy(network string, proxy *url.URL, addr string) (net.Conn, error) {
	conn, err := net.DialTimeout(network, proxy.Host, defaultTimeout)
	if err != nil {
		return nil, err
	}
	connect := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if proxy.User != nil {
		credential := base64.StdEncoding.EncodeToString([]byte(proxy.User.String()))
		connect.Header.Set("Proxy-Authorization", "Basic "+credential)
	}
	if err := connect.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), connect)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT to %s failed: %s", addr, resp.Status)
	}
	return conn, nil
}
