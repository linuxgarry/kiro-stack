package clash

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

// clientCacheKey uniquely identifies a cached http.Client for a (node,timeout)
// pair within a given generation of the proxies map.
type clientCacheKey struct {
	generation uint64
	node       string
	timeout    time.Duration
}

var (
	clientCacheMu sync.Mutex
	clientCache   = map[clientCacheKey]*http.Client{}
)

// invalidateClientCache drops every cached http.Client. Called when the
// subscription is reloaded or cleared.
func invalidateClientCache() {
	clientCacheMu.Lock()
	clientCache = map[clientCacheKey]*http.Client{}
	clientCacheMu.Unlock()
}

// ClientForNode returns an *http.Client that routes every request through
// the named Clash node. Returns (nil, error) if the node is not loaded.
//
// Note on chain dialing through the global jump: mihomo's ProxyAdapter
// interface does not expose a way to inject an upstream dialer at runtime
// from outside the Tunnel runtime. Per-node dial therefore goes directly
// from this VPS to the node's ingress. If your VPS cannot reach Clash
// nodes directly (DNS poisoning, ISP blocks), use a per-account ProxyURL
// instead, or run a separate mihomo container.
func ClientForNode(name string, timeout time.Duration) (*http.Client, error) {
	proxy := Default().Get(name)
	if proxy == nil {
		return nil, fmt.Errorf("clash node %q not loaded", name)
	}
	gen := Default().Generation()
	key := clientCacheKey{generation: gen, node: name, timeout: timeout}

	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()

	if c, ok := clientCache[key]; ok {
		return c, nil
	}
	c := buildClient(proxy, timeout)
	clientCache[key] = c
	return c, nil
}

// ClientForJumpOnly returns an *http.Client that uses ONLY the global jump
// host as the proxy (no Clash node). Useful for the connectivity-test path
// when an account has nothing else bound: the jump still gives a verifiable
// egress different from the VPS itself. Trojan jumps work here.
func ClientForJumpOnly(timeout time.Duration) (*http.Client, bool) {
	jump := Default().jumpProxy()
	if jump == nil {
		return nil, false
	}
	return buildClient(jump, timeout), true
}

func buildClient(proxy C.Proxy, timeout time.Duration) *http.Client {
	dialer := makeDialer(proxy)
	transport := &http.Transport{
		DialContext:         dialer,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// makeDialer returns a DialContext that tunnels through the given mihomo proxy.
func makeDialer(proxy C.Proxy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network == "udp" || network == "udp4" || network == "udp6" {
			return nil, fmt.Errorf("UDP dialing not supported via proxy %q", proxy.Name())
		}
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("split host/port: %w", err)
		}
		portNum, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("parse port: %w", err)
		}
		return proxy.DialContext(ctx, metadataFor(host, uint16(portNum)))
	}
}

// metadataFor builds a mihomo Metadata for an outbound TCP target.
func metadataFor(host string, port uint16) *C.Metadata {
	meta := &C.Metadata{
		NetWork: C.TCP,
		DstPort: port,
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		meta.DstIP = ip
	} else {
		meta.Host = host
	}
	return meta
}
