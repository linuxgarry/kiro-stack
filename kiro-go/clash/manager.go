// Package clash integrates the mihomo (Clash.Meta) core as a library.
// It parses a Clash subscription (YAML) and lets callers dial through
// individual proxy nodes on a per-account basis.
package clash

import (
	"encoding/base64"
	"fmt"
	"io"
	"kiro-api-proxy/config"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
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
	subURL := config.GetClashSubscriptionURL()
	if subURL == "" {
		return 0, nil
	}

	// Try cache first — fast and doesn't need the network.
	if cached, cerr := os.ReadFile(subscriptionCachePath()); cerr == nil && len(cached) > 0 {
		if proxies, names, perr := parseSubscription(cached); perr == nil {
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
func (m *Manager) commit(proxies map[string]C.Proxy, names []string, lastErr string) {
	m.mu.Lock()
	m.proxies = proxies
	m.names = names
	m.lastFetch = time.Now().Unix()
	m.lastErr = lastErr
	atomic.AddUint64(&m.generation, 1)
	m.mu.Unlock()
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

	proxies, names, perr := parseSubscription(raw)
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

func (m *Manager) setError(msg string) {
	m.mu.Lock()
	m.lastErr = msg
	m.lastFetch = time.Now().Unix()
	m.mu.Unlock()
}

// fetchSubscription downloads the subscription URL with a Clash-like UA.
// It honors the optional GlobalOutboundProxy so VPS that block direct
// access to subscription CDNs can still reach them via a jump host.
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
	if jump := strings.TrimSpace(config.GetGlobalOutboundProxy()); jump != "" {
		if u, perr := url.Parse(jump); perr == nil && u.Scheme != "" && u.Host != "" {
			transport.Proxy = http.ProxyURL(u)
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
func parseSubscription(raw []byte) (map[string]C.Proxy, []string, error) {
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

	out := make(map[string]C.Proxy, len(cfg.Proxies))
	names := make([]string, 0, len(cfg.Proxies))
	for _, pm := range cfg.Proxies {
		p, err := adapter.ParseProxy(pm)
		if err != nil {
			// Skip unsupported / malformed nodes; do not abort the whole load.
			continue
		}
		if _, dup := out[p.Name()]; dup {
			// Name collision: keep first, skip second.
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
