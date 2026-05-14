package clash

import (
	"context"
	"encoding/json"
	"fmt"
	"kiro-api-proxy/config"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var dohClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// Bootstrap DoH without asking the potentially polluted system DNS
			// where the DoH provider itself lives. The URL hostname is kept as
			// cloudflare-dns.com / dns.google, so TLS SNI and certificate checks
			// still validate against the provider name.
			switch strings.ToLower(host) {
			case "cloudflare-dns.com":
				addr = net.JoinHostPort("1.1.1.1", port)
			case "dns.google":
				addr = net.JoinHostPort("8.8.8.8", port)
			case "dns.alidns.com":
				addr = net.JoinHostPort("223.5.5.5", port)
			case "doh.pub":
				addr = net.JoinHostPort("1.12.12.12", port)
			}
			d := &net.Dialer{Timeout: 6 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
	},
}

type dohAnswer struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	TTL  int    `json:"TTL"`
	Data string `json:"data"`
}

type dohResponse struct {
	Status int         `json:"Status"`
	Answer []dohAnswer `json:"Answer"`
}

type cleanDNSResult struct {
	IP       string
	Provider string
}

// hardenProxyDNS rewrites polluted hostname servers to clean DoH-resolved IPs.
//
// Some VPS networks poison subscription node domains to 127.0.0.1, which makes
// mihomo dial localhost and fail before the actual proxy protocol starts. We
// avoid runtime system DNS by resolving node hostnames during subscription load
// through public DNS-over-HTTPS and storing the clean IP in the node's server
// field. The original hostname is preserved for TLS/SNI-style fields so TLS
// proxy protocols continue to verify against the intended name.
func hardenProxyDNS(nodeCfg map[string]any) {
	rawServer, _ := nodeCfg["server"].(string)
	host := strings.TrimSpace(rawServer)
	if host == "" {
		return
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if isBadProxyIP(ip) {
			fmt.Printf("[DNSGuard] proxy %q has unusable IP server %s\n", nodeName(nodeCfg), host)
		}
		return
	}

	if overrideIP, ok := dnsOverrideFor(host); ok {
		nodeCfg["server"] = overrideIP
		preserveOriginalServerName(nodeCfg, host)
		fmt.Printf("[DNSGuard] proxy %q server %s -> %s via override\n", nodeName(nodeCfg), host, overrideIP)
		return
	}

	strategy := config.GetDNSStrategy()
	if strategy == "off" {
		return
	}

	clean, err := resolveCleanHost(host, strategy)
	if err != nil {
		if pollutedBySystemDNS(host) {
			fmt.Printf("[DNSGuard] proxy %q server %s appears polluted but DoH failed: %v\n", nodeName(nodeCfg), host, err)
		}
		return
	}

	nodeCfg["server"] = clean.IP
	preserveOriginalServerName(nodeCfg, host)
	fmt.Printf("[DNSGuard] proxy %q server %s -> %s via %s\n", nodeName(nodeCfg), host, clean.IP, clean.Provider)
}

func nodeName(nodeCfg map[string]any) string {
	if name, _ := nodeCfg["name"].(string); name != "" {
		return name
	}
	return "(unnamed)"
}

func preserveOriginalServerName(nodeCfg map[string]any, host string) {
	nodeCfg["kiro-original-server"] = host
	proxyType, _ := nodeCfg["type"].(string)
	proxyType = strings.ToLower(proxyType)

	switch proxyType {
	case "trojan", "vless", "vmess", "hysteria2", "tuic":
		if _, ok := nodeCfg["sni"]; !ok {
			nodeCfg["sni"] = host
		}
		if _, ok := nodeCfg["servername"]; !ok {
			nodeCfg["servername"] = host
		}
		if _, ok := nodeCfg["server-name"]; !ok {
			nodeCfg["server-name"] = host
		}
	}
}

func pollutedBySystemDNS(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return false
	}
	for _, ip := range addrs {
		if isBadProxyIP(ip) {
			return true
		}
	}
	return false
}

func resolveCleanHost(host, strategy string) (cleanDNSResult, error) {
	if strategy == "system" {
		return resolveSystemHost(host)
	}

	providers := dnsProvidersForStrategy(strategy)
	var lastErr error
	for _, p := range providers {
		ips, err := queryDoH(p.base, host, "A")
		if err != nil {
			lastErr = err
			continue
		}
		for _, ip := range ips {
			if !isBadProxyIP(ip) {
				return cleanDNSResult{IP: ip.String(), Provider: p.name}, nil
			}
		}
		lastErr = fmt.Errorf("%s returned no usable A record", p.name)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable A record")
	}
	return cleanDNSResult{}, lastErr
}

type dnsProvider struct {
	name string
	base string
}

func dnsProvidersForStrategy(strategy string) []dnsProvider {
	global := []dnsProvider{
		{name: "cloudflare", base: "https://cloudflare-dns.com/dns-query"},
		{name: "google", base: "https://dns.google/resolve"},
	}
	china := []dnsProvider{
		{name: "alidns", base: "https://dns.alidns.com/resolve"},
		{name: "dnspod", base: "https://doh.pub/dns-query"},
	}
	switch strategy {
	case "global":
		return global
	case "china":
		return china
	default:
		return append(global, china...)
	}
}

func resolveSystemHost(host string) (cleanDNSResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return cleanDNSResult{}, err
	}
	for _, ip := range addrs {
		if !isBadProxyIP(ip) {
			return cleanDNSResult{IP: ip.String(), Provider: "system"}, nil
		}
	}
	return cleanDNSResult{}, fmt.Errorf("system DNS returned no usable A record")
}

func dnsOverrideFor(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return "", false
	}
	overrides := config.GetDNSOverrides()
	if ip, ok := overrides[host]; ok {
		return ip, true
	}
	for pattern, ip := range overrides {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if !strings.HasPrefix(pattern, "*.") {
			continue
		}
		suffix := strings.TrimPrefix(pattern, "*")
		if strings.HasSuffix(host, suffix) {
			return ip, true
		}
	}
	return "", false
}

func queryDoH(baseURL, host, recordType string) ([]netip.Addr, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("name", host)
	q.Set("type", recordType)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	req.Header.Set("User-Agent", "kiro-stack-dns-guard/1.0")

	resp, err := dohClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("DoH HTTP %d", resp.StatusCode)
	}

	var out dohResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Status != 0 {
		return nil, fmt.Errorf("DoH status %d", out.Status)
	}
	ips := make([]netip.Addr, 0, len(out.Answer))
	for _, ans := range out.Answer {
		if ans.Type != 1 {
			continue
		}
		ip, err := netip.ParseAddr(ans.Data)
		if err != nil {
			continue
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

func isBadProxyIP(ip netip.Addr) bool {
	return !ip.IsValid() ||
		ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsPrivate()
}
