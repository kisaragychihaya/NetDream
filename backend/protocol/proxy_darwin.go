//go:build darwin

package protocol

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// scutilLine 匹配 `scutil --proxy` 输出里的 "Key : Value" 行。
var scutilLine = regexp.MustCompile(`^\s*([0-9A-Za-z]+)\s*:\s*(.*?)\s*$`)

// scutilTimeout 给 scutil 的时限：它是子进程，绝不能让它卡住请求路径。
const scutilTimeout = 2 * time.Second

// readSystemProxy 解析 `scutil --proxy`：HTTPEnable/HTTPProxy/HTTPPort 及
// HTTPS、SOCKS 对应三段，外加 ExceptionsList 例外数组。
func readSystemProxy() proxyConfig {
	ctx, cancel := context.WithTimeout(context.Background(), scutilTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return proxyConfig{}
	}
	return parseSCUtilProxy(string(out))
}

// parseSCUtilProxy 把 scutil 输出解析成 proxyConfig；ExceptionsList 数组
// 的成员以 "N : value" 行跟在 "ExceptionsList : <array> {" 之后。
func parseSCUtilProxy(out string) proxyConfig {
	cfg := proxyConfig{}
	enabled := map[string]bool{}
	hosts := map[string]string{}
	ports := map[string]int{}
	inExceptions := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "}") {
			inExceptions = false
			continue
		}
		m := scutilLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, value := m[1], m[2]
		if key == "ExceptionsList" {
			inExceptions = true
			continue
		}
		if inExceptions {
			if _, err := strconv.Atoi(key); err == nil && value != "" {
				cfg.Bypass = append(cfg.Bypass, value)
			}
			continue
		}
		switch key {
		case "HTTPEnable":
			enabled["http"] = value == "1"
		case "HTTPProxy":
			hosts["http"] = value
		case "HTTPPort":
			ports["http"], _ = strconv.Atoi(value)
		case "HTTPSEnable":
			enabled["https"] = value == "1"
		case "HTTPSProxy":
			hosts["https"] = value
		case "HTTPSPort":
			ports["https"], _ = strconv.Atoi(value)
		case "SOCKSEnable":
			enabled["socks"] = value == "1"
		case "SOCKSProxy":
			hosts["socks"] = value
		case "SOCKSPort":
			ports["socks"], _ = strconv.Atoi(value)
		}
	}
	if enabled["http"] && hosts["http"] != "" {
		cfg.HTTP = joinHostPort(hosts["http"], ports["http"], 80)
		cfg.HTTPS = cfg.HTTP
	}
	if enabled["https"] && hosts["https"] != "" {
		cfg.HTTPS = joinHostPort(hosts["https"], ports["https"], 443)
	}
	if enabled["socks"] && hosts["socks"] != "" {
		cfg.SOCKS = joinHostPort(hosts["socks"], ports["socks"], 1080)
	}
	return cfg
}
