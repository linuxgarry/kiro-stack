// Package auth 提供认证相关功能的 HTTP 客户端
package auth

import (
	"kiro-api-proxy/clash"
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

// pickClientForAccount 按 ProxyNode → ProxyURL → 直连 的优先级返回客户端。
// ProxyNode/ProxyURL 都为空时走默认全局 httpClient。
func pickClientForAccount(account *config.Account) *http.Client {
	if account == nil {
		return httpClient
	}
	if account.ProxyNode == "" && account.ProxyURL == "" {
		return httpClient
	}
	return clash.PickAccountClient(account)
}


