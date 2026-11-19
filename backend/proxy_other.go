//go:build !windows && !darwin

package main

// readSystemProxy 在其他平台没有统一的系统代理来源：直连（环境变量仍由
// proxyForRequest 优先处理）。
func readSystemProxy() proxyConfig { return proxyConfig{} }
