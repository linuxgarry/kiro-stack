package config

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

type clientKey struct {
	proxy   string
	timeout time.Duration
	maxIdle int
	maxHost int
}

var clientCache sync.Map

// GetHTTPClient returns a cached HTTP client.
//
// If proxyURL is empty, the transport honors environment proxy settings
// (HTTPS_PROXY/HTTP_PROXY) — otherwise it forces traffic through the given
// proxy. Supported schemes: http, https, socks5.
func GetHTTPClient(proxyURL string, timeout time.Duration, maxIdle, maxHost int) *http.Client {
	key := clientKey{proxy: proxyURL, timeout: timeout, maxIdle: maxIdle, maxHost: maxHost}
	if v, ok := clientCache.Load(key); ok {
		return v.(*http.Client)
	}

	t := &http.Transport{
		MaxIdleConns:        maxIdle,
		MaxIdleConnsPerHost: maxHost,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	}

	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil && u.Scheme != "" && u.Host != "" {
			t.Proxy = http.ProxyURL(u)
		} else {
			// Malformed proxy: fall back to direct to avoid silent env-proxy use.
			t.Proxy = nil
		}
	} else {
		t.Proxy = http.ProxyFromEnvironment
	}

	c := &http.Client{Timeout: timeout, Transport: t}
	actual, _ := clientCache.LoadOrStore(key, c)
	return actual.(*http.Client)
}

// GetAccountHTTPClient is a convenience wrapper for account-scoped short-lived
// calls (auth refresh / usage-limit / model list). 30s timeout, modest pool.
func GetAccountHTTPClient(proxyURL string) *http.Client {
	return GetHTTPClient(proxyURL, 30*time.Second, 50, 10)
}

// GetKiroStreamHTTPClient is a convenience wrapper for the long-lived streaming
// Kiro API call. 5-minute timeout, larger idle pool.
func GetKiroStreamHTTPClient(proxyURL string) *http.Client {
	return GetHTTPClient(proxyURL, 5*time.Minute, 100, 20)
}
