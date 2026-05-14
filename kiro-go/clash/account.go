package clash

import (
	"errors"
	"fmt"
	"io"
	"kiro-api-proxy/config"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// PickAccountClient returns the short-lived HTTP client appropriate for the
// given account. Resolution order:
//  1. account.TunnelProxyURL (paid tunnel provider such as Luminati/Rola)
//  2. account.ProxyNode      (Clash node from loaded subscription)
//  3. account.ProxyURL       (legacy raw http/https/socks5 proxy)
//  4. global tunnel proxy
//  5. direct connection (honoring HTTPS_PROXY env if set)
//
// If ProxyNode is set but the node isn't currently loaded (e.g. after a
// subscription reload that dropped it), we fall back to ProxyURL → direct
// so that the account keeps working.
//
// If ProxyNode is loaded but fails at request time with a transport-level
// network error (EOF, timeout, connection reset, etc.), we retry once through
// account.ProxyURL when present, otherwise through the global jump. This keeps
// a bad Clash node from taking real Kiro calls down when the jump is healthy.
func PickAccountClient(account *config.Account) *http.Client {
	if ref := config.EffectiveTunnelProxyRef(account); ref.URL != "" && (account == nil || ref.IsAccount || strings.TrimSpace(account.ProxyNode) == "") {
		return withTunnelRefresh(config.GetAccountHTTPClient(ref.URL), ref)
	}
	if account != nil && account.ProxyNode != "" {
		if c, err := ClientForNode(account.ProxyNode, 30*time.Second); err == nil {
			return withRuntimeFallback(c, account, 30*time.Second, "clash:"+account.ProxyNode)
		}
	}
	var proxyURL string
	if account != nil {
		proxyURL = account.ProxyURL
	}
	if proxyURL == "" {
		proxyURL = config.GetGlobalTunnelProxy()
	}
	if proxyURL == config.GetGlobalTunnelProxy() && proxyURL != "" {
		ref := config.EffectiveTunnelProxyRef(account)
		return withTunnelRefresh(config.GetAccountHTTPClient(ref.URL), ref)
	}
	return config.GetAccountHTTPClient(proxyURL)
}

// PickAccountStreamClient is the long-timeout variant for streaming Kiro API
// calls. Same resolution order as PickAccountClient.
func PickAccountStreamClient(account *config.Account) *http.Client {
	if ref := config.EffectiveTunnelProxyRef(account); ref.URL != "" && (account == nil || ref.IsAccount || strings.TrimSpace(account.ProxyNode) == "") {
		return withTunnelRefresh(config.GetKiroStreamHTTPClient(ref.URL), ref)
	}
	if account != nil && account.ProxyNode != "" {
		if c, err := ClientForNode(account.ProxyNode, 5*time.Minute); err == nil {
			return withRuntimeFallback(c, account, 5*time.Minute, "clash:"+account.ProxyNode)
		}
	}
	var proxyURL string
	if account != nil {
		proxyURL = account.ProxyURL
	}
	if proxyURL == "" {
		proxyURL = config.GetGlobalTunnelProxy()
	}
	if proxyURL == config.GetGlobalTunnelProxy() && proxyURL != "" {
		ref := config.EffectiveTunnelProxyRef(account)
		return withTunnelRefresh(config.GetKiroStreamHTTPClient(ref.URL), ref)
	}
	return config.GetKiroStreamHTTPClient(proxyURL)
}

func withRuntimeFallback(primary *http.Client, account *config.Account, timeout time.Duration, primaryName string) *http.Client {
	if primary == nil || primary.Transport == nil {
		return primary
	}

	fallbackName, fallbackTransport := fallbackTransportFor(account, timeout)
	if fallbackTransport == nil {
		return primary
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &fallbackRoundTripper{
			primaryName:  primaryName,
			primary:      primary.Transport,
			fallbackName: fallbackName,
			fallback:     fallbackTransport,
		},
	}
}

func fallbackTransportFor(account *config.Account, timeout time.Duration) (string, http.RoundTripper) {
	if ref := config.EffectiveTunnelProxyRef(account); ref.URL != "" {
		c := withTunnelRefresh(config.GetHTTPClient(ref.URL, timeout, 50, 10), ref)
		if c != nil && c.Transport != nil {
			if ref.IsAccount {
				return "account tunnel", c.Transport
			}
			return "global tunnel", c.Transport
		}
	}
	if account != nil && strings.TrimSpace(account.ProxyURL) != "" {
		c := config.GetHTTPClient(account.ProxyURL, timeout, 50, 10)
		if c != nil && c.Transport != nil {
			return "account proxyUrl", c.Transport
		}
	}
	if c, ok := ClientForJumpOnly(timeout); ok && c != nil && c.Transport != nil {
		return "global jump", c.Transport
	}
	return "", nil
}

func withTunnelRefresh(client *http.Client, ref config.TunnelProxyRef) *http.Client {
	if client == nil || client.Transport == nil || ref.Scope == "" || ref.RawURL == "" {
		return client
	}
	return &http.Client{
		Timeout: client.Timeout,
		Transport: &tunnelRefreshRoundTripper{
			base: client.Transport,
			ref:  ref,
		},
		CheckRedirect: client.CheckRedirect,
		Jar:           client.Jar,
	}
}

type tunnelRefreshRoundTripper struct {
	base http.RoundTripper
	ref  config.TunnelProxyRef
}

func (rt *tunnelRefreshRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		config.RecordTunnelResult(rt.ref.Scope, rt.ref.RawURL, err)
		return resp, err
	}
	if resp != nil && (resp.StatusCode == http.StatusProxyAuthRequired || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) {
		config.RecordTunnelResult(rt.ref.Scope, rt.ref.RawURL, fmt.Errorf("HTTP %d", resp.StatusCode))
	} else {
		config.RecordTunnelResult(rt.ref.Scope, rt.ref.RawURL, nil)
	}
	return resp, err
}

type fallbackRoundTripper struct {
	primaryName  string
	primary      http.RoundTripper
	fallbackName string
	fallback     http.RoundTripper
}

func (rt *fallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.primary.RoundTrip(req)
	if err == nil || !shouldRetryWithFallback(err) {
		return resp, err
	}
	if req.Body != nil && req.GetBody == nil {
		return resp, err
	}

	retryReq := req.Clone(req.Context())
	if req.Body != nil {
		body, bodyErr := req.GetBody()
		if bodyErr != nil {
			return resp, err
		}
		retryReq.Body = body
	}
	fmt.Printf("[ProxyFallback] %s failed (%v); retrying via %s\n", rt.primaryName, err, rt.fallbackName)
	fallbackResp, fallbackErr := rt.fallback.RoundTrip(retryReq)
	if fallbackErr == nil && fallbackResp != nil {
		if fallbackResp.Header == nil {
			fallbackResp.Header = make(http.Header)
		}
		fallbackResp.Header.Set("X-Kiro-Proxy-Fallback", rt.fallbackName)
		fallbackResp.Header.Set("X-Kiro-Proxy-Primary-Error", err.Error())
	}
	return fallbackResp, fallbackErr
}

func shouldRetryWithFallback(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	retryMarkers := []string{
		"eof",
		"connection reset",
		"connection refused",
		"broken pipe",
		"deadline exceeded",
		"i/o timeout",
		"tls handshake timeout",
		"server misbehaving",
		"no such host",
	}
	for _, marker := range retryMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
