package clash

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
)

// parseJumpURL turns a single URL string into a mihomo C.Proxy.
// Supported schemes:
//   - http://[user:pass@]host:port
//   - https://[user:pass@]host:port
//   - socks5://[user:pass@]host:port
//   - socks5h://[user:pass@]host:port
//   - trojan://password@host:port[?sni=...&skip-cert-verify=true&alpn=h2,http/1.1]
//
// Returns (nil, nil) for an empty input — meaning "no jump host configured".
func parseJumpURL(raw string) (C.Proxy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse jump URL: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("jump URL missing host")
	}
	host := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		switch u.Scheme {
		case "http":
			portStr = "80"
		case "https":
			portStr = "443"
		default:
			return nil, fmt.Errorf("jump URL must include explicit port for scheme %q", u.Scheme)
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("parse jump port: %w", err)
	}

	cfg := map[string]any{
		"name":   "__jump__",
		"server": host,
		"port":   port,
	}

	switch strings.ToLower(u.Scheme) {
	case "http":
		cfg["type"] = "http"
	case "https":
		cfg["type"] = "http"
		cfg["tls"] = true
	case "socks5", "socks5h":
		cfg["type"] = "socks5"
	case "trojan":
		cfg["type"] = "trojan"
		// password lives in the userinfo portion: trojan://password@host:port
		// (some providers also encode it as user:password — we accept either).
		if u.User != nil {
			if pw, hasPw := u.User.Password(); hasPw {
				cfg["password"] = pw
			} else {
				cfg["password"] = u.User.Username()
			}
		}
		if cfg["password"] == "" || cfg["password"] == nil {
			return nil, fmt.Errorf("trojan jump URL must include the password before @")
		}
		// SNI defaults to the host if the query doesn't override.
		if sni := u.Query().Get("sni"); sni != "" {
			cfg["sni"] = sni
		} else {
			cfg["sni"] = host
		}
		if skip := u.Query().Get("skip-cert-verify"); skip == "true" || skip == "1" {
			cfg["skip-cert-verify"] = true
		}
		if alpn := u.Query().Get("alpn"); alpn != "" {
			cfg["alpn"] = strings.Split(alpn, ",")
		}
	default:
		return nil, fmt.Errorf("unsupported jump scheme %q (expected http/https/socks5/trojan)", u.Scheme)
	}

	// Common credential extraction for http/socks5 (trojan handled above).
	if cfg["type"] != "trojan" && u.User != nil {
		if user := u.User.Username(); user != "" {
			cfg["username"] = user
		}
		if pw, has := u.User.Password(); has {
			cfg["password"] = pw
		}
	}

	p, err := adapter.ParseProxy(cfg)
	if err != nil {
		return nil, fmt.Errorf("build jump proxy: %w", err)
	}
	return p, nil
}
