package clash

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
)

// jumpProxyName is the synthetic node name we use when injecting the jump
// into the subscription's proxies list. It must match the value placed in
// every other node's `dialer-proxy` field.
const jumpProxyName = "__kiro_jump__"

// parseJumpURL turns a single URL string into a mihomo C.Proxy.
//
// Supported schemes:
//   - http://[user:pass@]host:port
//   - https://[user:pass@]host:port
//   - socks5://[user:pass@]host:port
//   - socks5h://[user:pass@]host:port
//   - trojan://password@host:port[?sni=...&skip-cert-verify=true&alpn=h2,http/1.1]
//   - ss://base64(method:password)@host:port[#name]            (SIP002)
//   - ss://base64(method:password@host:port)[#name]            (legacy single-blob form)
//   - ss://method:password@host:port                            (plaintext form)
//   - vmess://base64(JSON)                                      (V2RayN format)
//
// Returns (nil, nil) for an empty input — meaning "no jump host configured".
func parseJumpURL(raw string) (C.Proxy, error) {
	cfg, err := jumpConfigFor(raw, jumpProxyName)
	if err != nil || cfg == nil {
		return nil, err
	}
	p, err := adapter.ParseProxy(cfg)
	if err != nil {
		return nil, fmt.Errorf("build jump proxy: %w", err)
	}
	return p, nil
}

// jumpConfigFor returns the mihomo node config (a map suitable to feed back
// into adapter.ParseProxy or to splice into a Clash YAML's `proxies:` list)
// derived from `raw`, with the `name` field set to `name`. Returns (nil, nil)
// for empty input.
//
// Splitting parse logic out from `parseJumpURL` lets us reuse the resulting
// config map both for direct dialing and for chain injection into the
// subscription (`dialer-proxy: __kiro_jump__`).
func jumpConfigFor(raw, name string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	scheme := schemeOf(raw)
	var (
		cfg map[string]any
		err error
	)
	switch scheme {
	case "http", "https", "socks5", "socks5h", "trojan":
		cfg, err = stdJumpConfig(raw, name)
	case "ss":
		cfg, err = ssJumpConfig(raw, name)
	case "vmess":
		cfg, err = vmessJumpConfig(raw, name)
	default:
		return nil, fmt.Errorf("unsupported jump scheme %q (expected http/https/socks5/trojan/ss/vmess)", scheme)
	}
	if err != nil || cfg == nil {
		return cfg, err
	}
	hardenProxyDNS(cfg)
	return cfg, nil
}

func schemeOf(raw string) string {
	idx := strings.Index(raw, "://")
	if idx <= 0 {
		return ""
	}
	return strings.ToLower(raw[:idx])
}

// stdJumpConfig handles http/https/socks5/trojan whose syntax fits net/url.
func stdJumpConfig(raw, name string) (map[string]any, error) {
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
		"name":   name,
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
	}

	if cfg["type"] != "trojan" && u.User != nil {
		if user := u.User.Username(); user != "" {
			cfg["username"] = user
		}
		if pw, has := u.User.Password(); has {
			cfg["password"] = pw
		}
	}
	return cfg, nil
}

// ssJumpConfig handles ss:// in three observed forms.
func ssJumpConfig(raw, name string) (map[string]any, error) {
	body := strings.TrimPrefix(raw, "ss://")
	body = strings.TrimPrefix(body, "ss:")
	if i := strings.Index(body, "#"); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("ss URL is empty")
	}

	var cipher, password, host string
	var port int

	if at := strings.LastIndex(body, "@"); at > 0 {
		userPart := body[:at]
		hostPart := body[at+1:]

		if dec, err := decodeFlexBase64(userPart); err == nil && strings.Contains(dec, ":") {
			parts := strings.SplitN(dec, ":", 2)
			cipher, password = parts[0], parts[1]
		} else if strings.Contains(userPart, ":") {
			parts := strings.SplitN(userPart, ":", 2)
			cipher, password = parts[0], parts[1]
		} else {
			return nil, fmt.Errorf("ss URL userinfo unrecognized")
		}

		h, p, err := splitHostPort(hostPart)
		if err != nil {
			return nil, fmt.Errorf("ss URL host: %w", err)
		}
		host, port = h, p
	} else {
		dec, err := decodeFlexBase64(body)
		if err != nil {
			return nil, fmt.Errorf("ss URL base64 decode: %w", err)
		}
		atIdx := strings.LastIndex(dec, "@")
		colonIdx := strings.Index(dec, ":")
		if atIdx < 0 || colonIdx < 0 || colonIdx > atIdx {
			return nil, fmt.Errorf("ss URL legacy form malformed: %q", dec)
		}
		cipher = dec[:colonIdx]
		password = dec[colonIdx+1 : atIdx]
		h, p, err := splitHostPort(dec[atIdx+1:])
		if err != nil {
			return nil, fmt.Errorf("ss URL legacy host: %w", err)
		}
		host, port = h, p
	}

	return map[string]any{
		"name":     name,
		"type":     "ss",
		"server":   host,
		"port":     port,
		"cipher":   cipher,
		"password": password,
	}, nil
}

// vmessJumpConfig handles vmess://base64(JSON) in V2RayN format.
func vmessJumpConfig(raw, name string) (map[string]any, error) {
	body := strings.TrimPrefix(raw, "vmess://")
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("vmess URL is empty")
	}
	dec, err := decodeFlexBase64(body)
	if err != nil {
		return nil, fmt.Errorf("vmess base64 decode: %w", err)
	}

	var v map[string]any
	if err := json.Unmarshal([]byte(dec), &v); err != nil {
		return nil, fmt.Errorf("vmess JSON: %w", err)
	}

	server, _ := v["add"].(string)
	if server == "" {
		return nil, fmt.Errorf("vmess JSON missing 'add' (server)")
	}
	port, err := coerceInt(v["port"])
	if err != nil {
		return nil, fmt.Errorf("vmess port: %w", err)
	}
	uuid, _ := v["id"].(string)
	if uuid == "" {
		return nil, fmt.Errorf("vmess JSON missing 'id' (uuid)")
	}
	aid, _ := coerceInt(v["aid"])
	cipher, _ := v["scy"].(string)
	if cipher == "" {
		cipher = "auto"
	}
	network, _ := v["net"].(string)
	if network == "" {
		network = "tcp"
	}

	cfg := map[string]any{
		"name":    name,
		"type":    "vmess",
		"server":  server,
		"port":    port,
		"uuid":    uuid,
		"alterId": aid,
		"cipher":  cipher,
		"network": network,
	}
	if tls, _ := v["tls"].(string); tls == "tls" {
		cfg["tls"] = true
		if sni, _ := v["sni"].(string); sni != "" {
			cfg["servername"] = sni
		} else if hostHeader, _ := v["host"].(string); hostHeader != "" {
			cfg["servername"] = hostHeader
		}
	}
	if network == "ws" {
		wsOpts := map[string]any{}
		if path, _ := v["path"].(string); path != "" {
			wsOpts["path"] = path
		}
		if hostHeader, _ := v["host"].(string); hostHeader != "" {
			wsOpts["headers"] = map[string]any{"Host": hostHeader}
		}
		cfg["ws-opts"] = wsOpts
	}
	return cfg, nil
}

func decodeFlexBase64(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty")
	}
	candidates := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	pad := s + strings.Repeat("=", (4-len(s)%4)%4)
	for _, dec := range candidates {
		if b, err := dec(pad); err == nil {
			return string(b), nil
		}
		if b, err := dec(s); err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("not base64")
}

func splitHostPort(hp string) (string, int, error) {
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("missing port: %q", hp)
	}
	host := hp[:i]
	port, err := strconv.Atoi(hp[i+1:])
	if err != nil {
		return "", 0, fmt.Errorf("port %q: %w", hp[i+1:], err)
	}
	return host, port, nil
}

func coerceInt(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case string:
		return strconv.Atoi(x)
	default:
		return 0, fmt.Errorf("cannot coerce %T to int", v)
	}
}
