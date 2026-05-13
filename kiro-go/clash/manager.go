// Package clash integrates the mihomo (Clash.Meta) core as a library.
// It parses a Clash subscription (YAML) and lets callers dial through
// individual proxy nodes on a per-account basis.
package clash

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"kiro-api-proxy/config"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
	"gopkg.in/yaml.v3"
)

// subscriptionCachePath is derived from the active config path so the
// cache follows the Docker volume mount (CONFIG_PATH=/app/data/config.json
// => /app/data/clash-cache.yaml).
func subscriptionCachePath() string {
	if p := config.GetConfigPath(); p != "" {
		return filepath.Join(filepath.Dir(p), "clash-cache.yaml")
	}
	return "clash-cache.yaml"
}

// Manager is the process-wide Clash subscription manager.
type Manager struct {
	mu         sync.RWMutex
	proxies    map[string]C.Proxy // keyed by node name
	names      []string           // ordered (original subscription order)
	generation uint64             // incremented on every successful load
	lastErr    string
	lastFetch  int64
	// jump is the parsed global outbound proxy (http/https/socks5/trojan).
	// nil = no jump configured. Rebuilt by SetJump.
	jump        C.Proxy
	jumpRawURL  string
	jumpLastErr string
}

var (
	mgr     = &Manager{}
	fetchMu sync.Mutex // serialize fetches
)

// Default returns the process-wide manager singleton.
func Default() *Manager { return mgr }

// Init loads the subscription. It prefers the on-disk cache (so a browser
// refresh doesn't lose the node list across restarts) and then kicks off a
// background re-fetch of the live URL. Returns the number of nodes loaded
// from cache synchronously.
func Init() (loaded int, err error) {
	// Always install the jump proxy first so the subscription fetch can
	// chain through it. Empty URL is a no-op.
	if jump := config.GetGlobalOutboundProxy(); jump != "" {
		_ = mgr.SetJump(jump)
	}

	subURL := config.GetClashSubscriptionURL()
	if subURL == "" {
		return 0, nil
	}

	// Try cache first — fast and doesn't need the network.
	if cached, cerr := os.ReadFile(subscriptionCachePath()); cerr == nil && len(cached) > 0 {
		if proxies, names, perr := parseSubscription(cached, mgr.JumpURL()); perr == nil {
			mgr.commit(proxies, names, "")
			loaded = len(proxies)
		}
	}

	// Always try a live fetch in the background so the cache gets refreshed.
	// Startup doesn't block on this — if the VPS can't reach the subscription
	// URL right now, we still have the cache.
	go func() {
		if _, err := mgr.Load(subURL); err != nil {
			// Failure is already stored in lastErr via setError.
			_ = err
		}
	}()

	return loaded, nil
}

// Generation returns the current proxies-map generation (changes on each reload).
func (m *Manager) Generation() uint64 {
	return atomic.LoadUint64(&m.generation)
}

// Status is a snapshot for the admin UI.
type Status struct {
	SubscriptionURL string   `json:"subscriptionUrl"`
	Loaded          int      `json:"loaded"`
	Names           []string `json:"names"`
	LastFetch       int64    `json:"lastFetch"`
	LastError       string   `json:"lastError,omitempty"`
}

// Snapshot returns the current manager state.
func (m *Manager) Snapshot() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.names))
	copy(names, m.names)
	return Status{
		SubscriptionURL: config.GetClashSubscriptionURL(),
		Loaded:          len(m.proxies),
		Names:           names,
		LastFetch:       m.lastFetch,
		LastError:       m.lastErr,
	}
}

// Names returns the list of loaded node names.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.names))
	copy(out, m.names)
	return out
}

// Get returns the parsed proxy for a node name, or nil if not loaded.
func (m *Manager) Get(name string) C.Proxy {
	if name == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.proxies[name]
}

// Has reports whether the named node is loaded.
func (m *Manager) Has(name string) bool {
	return m.Get(name) != nil
}

// commit replaces the in-memory proxies map and bumps the generation.
// Callers must not hold the manager mutex.
//
// We also push the proxy set into mihomo's global tunnel.Proxies registry
// so that `dialer-proxy: __kiro_jump__` lookups resolve. Mihomo's
// proxydialer.NewByName resolves names against `tunnel.Proxies()` at dial
// time — without this call, every chain dial fails with
// "proxyName[__kiro_jump__] not found".
func (m *Manager) commit(proxies map[string]C.Proxy, names []string, lastErr string) {
	m.mu.Lock()
	m.proxies = proxies
	m.names = names
	m.lastFetch = time.Now().Unix()
	m.lastErr = lastErr
	atomic.AddUint64(&m.generation, 1)
	jump := m.jump
	m.mu.Unlock()

	// Build the registry mihomo will see. We include both real subscription
	// nodes AND the synthetic jump (so the by-name lookup finds it). The
	// jump is keyed under jumpProxyName but excluded from `names` returned
	// to the UI.
	registry := make(map[string]C.Proxy, len(proxies)+1)
	for k, v := range proxies {
		registry[k] = v
	}
	if jump != nil {
		registry[jumpProxyName] = jump
	}
	tunnel.UpdateProxies(registry, nil)

	invalidateClientCache()
}

// Load fetches the subscription, parses it, and replaces the in-memory
// proxies map. On success the raw YAML is also written to the on-disk
// cache so a later restart/refresh can recover without network.
func (m *Manager) Load(subURL string) (int, error) {
	if subURL == "" {
		return 0, fmt.Errorf("empty subscription URL")
	}
	fetchMu.Lock()
	defer fetchMu.Unlock()

	raw, err := fetchSubscription(subURL)
	if err != nil {
		m.setError(fmt.Sprintf("fetch failed: %v", err))
		return 0, err
	}

	proxies, names, perr := parseSubscription(raw, m.JumpURL())
	if perr != nil {
		m.setError(fmt.Sprintf("parse failed: %v", perr))
		return 0, perr
	}

	m.commit(proxies, names, "")

	// Best-effort cache write; don't fail the load on a broken disk.
	if cachePath := subscriptionCachePath(); cachePath != "" {
		_ = os.WriteFile(cachePath, raw, 0600)
	}

	return len(proxies), nil
}

// Clear drops the in-memory proxies, invalidates caches, and removes the
// on-disk subscription cache.
func (m *Manager) Clear() {
	m.commit(nil, nil, "")
	if cachePath := subscriptionCachePath(); cachePath != "" {
		_ = os.Remove(cachePath)
	}
}

// jumpProxy returns the configured global jump proxy, or nil.
// Caller must NOT hold the manager mutex.
func (m *Manager) jumpProxy() C.Proxy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jump
}

// SetJump installs a new global outbound (jump) proxy from a URL string.
// An empty string clears the jump.
//
// Because the jump is baked into each node's `dialer-proxy` field at
// parse time, changing the jump requires re-parsing the subscription.
// We re-parse from the on-disk cache (no network round-trip) so the
// chain takes effect immediately.
func (m *Manager) SetJump(rawURL string) error {
	p, err := parseJumpURL(rawURL)
	if err != nil {
		m.mu.Lock()
		m.jumpLastErr = err.Error()
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	m.jump = p
	m.jumpRawURL = strings.TrimSpace(rawURL)
	m.jumpLastErr = ""
	atomic.AddUint64(&m.generation, 1)
	m.mu.Unlock()

	// Re-parse the cached subscription with the new jump so every node
	// gets `dialer-proxy: __kiro_jump__` re-stamped (or stripped, when
	// rawURL is "").
	if cached, cerr := os.ReadFile(subscriptionCachePath()); cerr == nil && len(cached) > 0 {
		if proxies, names, perr := parseSubscription(cached, m.JumpURL()); perr == nil {
			m.commit(proxies, names, "")
		}
	}

	// Per-node clients embed the dialer chain at construction time; bumping
	// generation alone won't be enough because old keys are still valid.
	// Drop everything so the new jump takes effect immediately.
	invalidateClientCache()
	return nil
}

// JumpURL returns the raw URL last passed to SetJump (or empty string).
func (m *Manager) JumpURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jumpRawURL
}

// JumpError returns the parse error from the most recent SetJump call.
func (m *Manager) JumpError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jumpLastErr
}

func (m *Manager) setError(msg string) {
	m.mu.Lock()
	m.lastErr = msg
	m.lastFetch = time.Now().Unix()
	m.mu.Unlock()
}

// fetchSubscription downloads the subscription URL with a Clash-like UA.
// If a jump host is configured, the fetch goes through it (mihomo dialer,
// so trojan etc. work). Otherwise it dials directly.
func fetchSubscription(subURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		return nil, err
	}
	// Many providers gate subscription output on this UA. ClashMeta is the most
	// permissive value.
	req.Header.Set("User-Agent", "ClashMeta/1.19.24")

	transport := &http.Transport{
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	}
	if jump := mgr.jumpProxy(); jump != nil {
		// makeDialer with a nil "node" but jump as the only hop is exactly
		// the same as dialing through jump directly — reuse the helper.
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network == "udp" || network == "udp4" || network == "udp6" {
				return nil, fmt.Errorf("UDP not supported")
			}
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return nil, err
			}
			return jump.DialContext(ctx, metadataFor(host, uint16(port)))
		}
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d from subscription", resp.StatusCode)
	}
	// Cap at 8 MiB to avoid accidental hangs on huge responses.
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// parseSubscription tries to interpret the raw bytes as either:
//   - Clash YAML config with a "proxies:" list
//   - base64-encoded YAML (some providers wrap it once)
//
// If `jumpRawURL` is non-empty and parses, parseSubscription prepends a
// synthetic `__kiro_jump__` proxy to the proxies list and stamps every
// real node with `dialer-proxy: __kiro_jump__`. mihomo's adapter layer
// then chains: dial(jump) → tunnel-to-node → node-handshake → target.
//
// The jump itself is excluded from the returned `names` slice so the UI
// dropdown doesn't show it as a selectable account binding.
func parseSubscription(raw []byte, jumpRawURL string) (map[string]C.Proxy, []string, error) {
	body := bytes_trimSpace(raw)
	if looksLikeBase64(body) {
		if dec, err := base64.StdEncoding.DecodeString(string(body)); err == nil {
			body = bytes_trimSpace(dec)
		} else if dec, err := base64.RawStdEncoding.DecodeString(string(body)); err == nil {
			body = bytes_trimSpace(dec)
		}
	}

	var cfg struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return nil, nil, fmt.Errorf("yaml: %w", err)
	}
	if len(cfg.Proxies) == 0 {
		return nil, nil, fmt.Errorf("no proxies in subscription")
	}

	// If a jump is configured, splice it into the proxies list first.
	// adapter.ParseProxy resolves dialer-proxy by name lookup at construction
	// time within mihomo's adapter cache, so the jump must be parsed BEFORE
	// any node that references it.
	jumpEnabled := false
	if jumpRawURL != "" {
		jumpCfg, err := jumpConfigFor(jumpRawURL, jumpProxyName)
		if err != nil {
			// Fall through with jump disabled — better to load the subscription
			// without a chain than to fail completely.
			jumpEnabled = false
		} else if jumpCfg != nil {
			if _, err := adapter.ParseProxy(jumpCfg); err == nil {
				jumpEnabled = true
			}
		}
	}

	out := make(map[string]C.Proxy, len(cfg.Proxies))
	names := make([]string, 0, len(cfg.Proxies))
	for _, pm := range cfg.Proxies {
		// Don't let a subscription accidentally collide with our reserved name.
		if n, _ := pm["name"].(string); n == jumpProxyName {
			continue
		}
		// Stamp dialer-proxy onto a *copy* of the node config so we don't
		// mutate the caller's map.
		nodeCfg := pm
		if jumpEnabled {
			nodeCfg = make(map[string]any, len(pm)+1)
			for k, v := range pm {
				nodeCfg[k] = v
			}
			nodeCfg["dialer-proxy"] = jumpProxyName
		}
		hardenProxyDNS(nodeCfg)
		p, err := adapter.ParseProxy(nodeCfg)
		if err != nil {
			// Skip unsupported / malformed nodes; do not abort the whole load.
			continue
		}
		if _, dup := out[p.Name()]; dup {
			continue
		}
		out[p.Name()] = p
		names = append(names, p.Name())
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no parsable proxies in subscription")
	}
	return out, names, nil
}

// looksLikeBase64 is a cheap heuristic: printable-only, >= 64 chars, no YAML
// hints (colons or newlines beyond the trailing ones).
func looksLikeBase64(b []byte) bool {
	if len(b) < 64 {
		return false
	}
	s := string(b)
	if strings.Contains(s, ":") || strings.Contains(s, "\n") {
		return false
	}
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=' {
			continue
		}
		return false
	}
	return true
}

func bytes_trimSpace(b []byte) []byte {
	s, e := 0, len(b)
	for s < e && (b[s] == ' ' || b[s] == '\t' || b[s] == '\n' || b[s] == '\r') {
		s++
	}
	for e > s && (b[e-1] == ' ' || b[e-1] == '\t' || b[e-1] == '\n' || b[e-1] == '\r') {
		e--
	}
	return b[s:e]
}
