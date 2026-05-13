// Package auth 提供认证相关功能的 HTTP 客户端
package auth

import (
	"kiro-api-proxy/config"
	"net/http"
	"time"
)

// 默认全局 HTTP 客户端（不走代理，兼顾旧调用点）
// 用于 auth 模块中不依赖特定账号的 HTTP 请求（OIDC client 注册、设备授权等）
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	},
}

// pickClient 返回一个 HTTP 客户端：如果提供了 proxyURL 则走代理，
// 否则回落到默认 httpClient。结果会按 proxyURL 缓存复用。
func pickClient(proxyURL string) *http.Client {
	if proxyURL == "" {
		return httpClient
	}
	return config.GetAccountHTTPClient(proxyURL)
}
