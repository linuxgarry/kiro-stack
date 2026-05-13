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

// parseJumpURL turns a single URL string into a mihomo C.Proxy.
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	scheme := schemeOf(raw)
	switch scheme {
	case "http", "https", "socks5", "socks5h", "trojan":
		return parseStdJump(raw)
	case "ss":
		return parseSsJump(raw)
	case "vmess":
		return parseVmessJump(raw)
	default:
		return nil, fmt.Errorf("unsupported jump scheme %q (expected http/https/socks5/trojan/ss/vmess)", scheme)
	}
}

func schemeOf(raw string) string {
	idx := strings.Index(raw, "://")
	if idx <= 0 {
		return ""
	}
	return strings.ToLower(raw[:idx])
}

// parseStdJump handles http/https/socks5/trojan whose syntax fits net/url.
func parseStdJump(raw string) (C.Proxy, error) {
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

	p, err := adapter.ParseProxy(cfg)
	if err != nil {
		return nil, fmt.Errorf("build jump proxy: %w", err)
	}
	return p, nil
}

// parseSsJump handles ss:// in three observed forms.
func parseSsJump(raw string) (C.Proxy, error) {
	body := strings.TrimPrefix(raw, "ss://")
	body = strings.TrimPrefix(body, "ss:")
	// Strip URL fragment (the human-readable name).
	if i := strings.Index(body, "#"); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("ss URL is empty")
	}

	var cipher, password, host string
	var port int

	// Form A: ss://base64(method:password)@host:port    (SIP002)
	// Form B: ss://method:password@host:port            (plaintext)
	// Form C: ss://base64(method:password@host:port)    (legacy)
	if at := strings.LastIndex(body, "@"); at > 0 {
		userPart := body[:at]
		hostPart := body[at+1:]

		// Try base64 decode first; fall back to plaintext.
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
		// Form C: whole thing is base64-encoded "method:password@host:port"
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

	cfg := map[string]any{
		"name":     "__jump__",
		"type":     "ss",
		"server":   host,
		"port":     port,
		"cipher":   cipher,
		"password": password,
	}
	p, err := adapter.ParseProxy(cfg)
	if err != nil {
		return nil, fmt.Errorf("build ss jump: %w", err)
	}
	return p, nil
}

// parseVmessJump handles vmess://base64(JSON) in V2RayN format.
func parseVmessJump(raw string) (C.Proxy, error) {
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
		"name":    "__jump__",
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
	// WebSocket transport
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

	p, err := adapter.ParseProxy(cfg)
	if err != nil {
		return nil, fmt.Errorf("build vmess jump: %w", err)
	}
	return p, nil
}

// decodeFlexBase64 tries standard, URL, and raw variants. Some providers
// pad inconsistently; we add padding before each attempt.
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
