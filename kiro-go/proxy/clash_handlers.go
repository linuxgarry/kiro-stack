package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"kiro-api-proxy/clash"
	"kiro-api-proxy/config"
	"net/http"
	"strings"
	"time"
)

// apiGetClash returns the current Clash subscription status snapshot.
func (h *Handler) apiGetClash(w http.ResponseWriter, r *http.Request) {
	status := clash.Default().Snapshot()
	_ = json.NewEncoder(w).Encode(status)
}

// apiGetOutbound returns the global outbound proxy (jump host) URL.
func (h *Handler) apiGetOutbound(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]string{"url": config.GetGlobalOutboundProxy()})
}

// apiUpdateOutbound persists the global outbound proxy URL. Empty string
// clears the jump host; non-empty is validated as http/https/socks5/trojan.
// On save we also install it into the live Clash manager so every per-node
// dial chains through it without restart.
func (h *Handler) apiUpdateOutbound(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL != "" {
		if _, err := urlParseStrict(req.URL); err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
	}
	if err := clash.Default().SetJump(req.URL); err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if err := config.UpdateGlobalOutboundProxy(req.URL); err != nil {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// apiGetModelMapping returns the full mapping table to the UI.
func (h *Handler) apiGetModelMapping(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(config.GetModelMapping())
}

// apiUpdateModelMapping replaces the full mapping table. Body is a flat
// {from: to} JSON object. Identity entries are silently dropped.
func (h *Handler) apiUpdateModelMapping(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON; expected {from: to}"})
		return
	}
	if err := config.UpdateModelMapping(in); err != nil {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "entries": len(config.GetModelMapping())})
}

// apiTestOutbound runs a connectivity + Geo probe through the currently
// configured global jump. Reports failure cleanly when no jump is set.
func (h *Handler) apiTestOutbound(w http.ResponseWriter, r *http.Request) {
	client, ok := clash.ClientForJumpOnly(15 * time.Second)
	if !ok {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "no global jump configured",
		})
		return
	}
	res := runProxyTest(client)
	res.Mode = "jump"
	if jumpURL := config.GetGlobalOutboundProxy(); jumpURL != "" {
		// Surface what we actually tested so the operator can sanity-check.
		res.Endpoint = res.Endpoint + " via " + jumpURL
	}
	_ = json.NewEncoder(w).Encode(res)
}

// urlParseStrict accepts the schemes the global jump can handle.
// Per-account proxies (v1) still use stdlib http.Client and are validated
// elsewhere (handler.go) — they support only http/https/socks5(h).
func urlParseStrict(s string) (string, error) {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "socks5://") || strings.HasPrefix(s, "socks5h://") ||
		strings.HasPrefix(s, "trojan://") || strings.HasPrefix(s, "ss://") ||
		strings.HasPrefix(s, "vmess://") {
		return s, nil
	}
	return "", fmt.Errorf("proxy URL must start with http://, https://, socks5://, socks5h://, trojan://, ss://, or vmess://")
}

// apiUpdateClash accepts {"subscriptionUrl": "..."} from the admin UI.
// An empty string clears the subscription. A non-empty string is persisted
// and then Load() is attempted synchronously so the UI can show immediate
// feedback.
func (h *Handler) apiUpdateClash(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SubscriptionURL string `json:"subscriptionUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	req.SubscriptionURL = strings.TrimSpace(req.SubscriptionURL)

	if req.SubscriptionURL != "" && !(strings.HasPrefix(req.SubscriptionURL, "http://") || strings.HasPrefix(req.SubscriptionURL, "https://")) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "subscriptionUrl must start with http:// or https://"})
		return
	}

	if err := config.UpdateClashSubscriptionURL(req.SubscriptionURL); err != nil {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.SubscriptionURL == "" {
		clash.Default().Clear()
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "loaded": 0})
		return
	}

	n, err := clash.Default().Load(req.SubscriptionURL)
	if err != nil {
		w.WriteHeader(502)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "loaded": n})
}

// apiRefreshClash re-fetches the stored subscription URL.
func (h *Handler) apiRefreshClash(w http.ResponseWriter, r *http.Request) {
	url := config.GetClashSubscriptionURL()
	if url == "" {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no subscription configured"})
		return
	}
	n, err := clash.Default().Load(url)
	if err != nil {
		w.WriteHeader(502)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "loaded": n})
}

// proxyTestResult is returned to the UI by apiTestAccountProxy.
type proxyTestResult struct {
	OK         bool   `json:"ok"`
	LatencyMs  int64  `json:"latencyMs"`
	Mode       string `json:"mode"`                 // "direct" | "clash" | "proxyUrl"
	Endpoint   string `json:"endpoint,omitempty"`   // which geo service answered
	IP         string `json:"ip,omitempty"`
	Country    string `json:"country,omitempty"`
	Region     string `json:"region,omitempty"`
	City       string `json:"city,omitempty"`
	ASN        string `json:"asn,omitempty"`
	Error      string `json:"error,omitempty"`
}

// apiTestAccountProxy runs a single GET to a public IP-info service through
// the account's configured proxy (Clash node → proxyUrl → direct). It
// returns latency plus the observed egress IP + geo so the operator can
// visually confirm the exit location.
func (h *Handler) apiTestAccountProxy(w http.ResponseWriter, r *http.Request, id string) {
	accounts := config.GetAccounts()
	var acc *config.Account
	for i := range accounts {
		if accounts[i].ID == id {
			acc = &accounts[i]
			break
		}
	}
	if acc == nil {
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}

	mode := "direct"
	if acc.ProxyNode != "" && clash.Default().Has(acc.ProxyNode) {
		mode = "clash"
	} else if acc.ProxyURL != "" {
		mode = "proxyUrl"
	}

	client := clash.PickAccountClient(acc)

	res := runProxyTest(client)
	res.Mode = mode
	_ = json.NewEncoder(w).Encode(res)
}

// runProxyTest hits a list of public IP-info endpoints through `client` and
// returns the first successful response, or the last error.
//
// We try multiple unrelated providers because some nodes (especially HK
// machines) blacklist the ipinfo / ifconfig.co / ip.sb family. Cloudflare's
// trace endpoint is plain text and is therefore parsed separately.
func runProxyTest(client *http.Client) proxyTestResult {
	type endpoint struct {
		url      string
		isTrace  bool // true → key=value text format from /cdn-cgi/trace
	}
	endpoints := []endpoint{
		{"https://ipinfo.io/json", false},
		{"https://ifconfig.co/json", false},
		{"https://api.ip.sb/geoip", false},
		{"https://www.cloudflare.com/cdn-cgi/trace", true},
		{"https://1.1.1.1/cdn-cgi/trace", true},
		{"https://api.myip.com", false},
	}
	start := time.Now()
	var lastErr string
	for _, ep := range endpoints {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", ep.url, nil)
		req.Header.Set("User-Agent", "curl/8.4")
		req.Header.Set("Accept", "application/json,text/plain")
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = ep.url + ": " + err.Error()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		cancel()
		if resp.StatusCode/100 != 2 {
			lastErr = fmt.Sprintf("%s: HTTP %d", ep.url, resp.StatusCode)
			continue
		}
		var r proxyTestResult
		if ep.isTrace {
			r = parseCloudflareTrace(body)
		} else {
			r = parseGeoResponse(body)
		}
		if r.IP == "" && r.Error == "" {
			lastErr = ep.url + ": empty parse"
			continue
		}
		r.OK = true
		r.Endpoint = ep.url
		r.LatencyMs = time.Since(start).Milliseconds()
		return r
	}
	return proxyTestResult{
		OK:        false,
		LatencyMs: time.Since(start).Milliseconds(),
		Error:     lastErr,
	}
}

// parseCloudflareTrace handles `key=value\n` text from /cdn-cgi/trace.
// Sample fields: ip=..., loc=US, colo=IAD, ts=..., visit_scheme=https.
func parseCloudflareTrace(body []byte) proxyTestResult {
	out := proxyTestResult{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key, val := line[:eq], line[eq+1:]
		switch key {
		case "ip":
			out.IP = val
		case "loc":
			out.Country = val
		case "colo":
			// Cloudflare datacenter code (IAD = Ashburn etc.) — surface it as City.
			out.City = val
		}
	}
	return out
}

// parseGeoResponse extracts common fields across ipinfo/ifconfig.co/ip.sb.
func parseGeoResponse(body []byte) proxyTestResult {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return proxyTestResult{Error: "parse: " + err.Error()}
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
		return ""
	}
	return proxyTestResult{
		IP:      get("ip"),
		Country: get("country", "country_iso", "country_code"),
		Region:  get("region", "region_name"),
		City:    get("city"),
		ASN:     get("org", "asn", "asn_org"),
	}
}
