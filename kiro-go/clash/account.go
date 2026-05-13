package clash

import (
	"kiro-api-proxy/config"
	"net/http"
	"time"
)

// PickAccountClient returns the short-lived HTTP client appropriate for the
// given account. Resolution order:
//  1. account.ProxyNode (Clash node from loaded subscription)
//  2. account.ProxyURL  (raw http/https/socks5 proxy)
//  3. direct connection (honoring HTTPS_PROXY env if set)
//
// If ProxyNode is set but the node isn't currently loaded (e.g. after a
// subscription reload that dropped it), we fall back to ProxyURL → direct
// so that the account keeps working.
func PickAccountClient(account *config.Account) *http.Client {
	if account != nil && account.ProxyNode != "" {
		if c, err := ClientForNode(account.ProxyNode, 30*time.Second); err == nil {
			return c
		}
	}
	var proxyURL string
	if account != nil {
		proxyURL = account.ProxyURL
	}
	return config.GetAccountHTTPClient(proxyURL)
}

// PickAccountStreamClient is the long-timeout variant for streaming Kiro API
// calls. Same resolution order as PickAccountClient.
func PickAccountStreamClient(account *config.Account) *http.Client {
	if account != nil && account.ProxyNode != "" {
		if c, err := ClientForNode(account.ProxyNode, 5*time.Minute); err == nil {
			return c
		}
	}
	var proxyURL string
	if account != nil {
		proxyURL = account.ProxyURL
	}
	return config.GetKiroStreamHTTPClient(proxyURL)
}
