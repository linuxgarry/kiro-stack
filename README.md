# 🚀 kiro-stack (linuxgarry fork)

本仓库 fork 自 **[Yoahoug/kiro-stack](https://github.com/Yoahoug/kiro-stack)**。在 `kiro-go` 模块上加了一套面向「机场订阅 + 多账号代理隔离」的实用功能。下面按 **从新到旧** 的顺序列出本 fork 在上游基础上做的所有改动。

Forked from **[Yoahoug/kiro-stack](https://github.com/Yoahoug/kiro-stack)**. Adds a set of practical features for "subscription-based proxies + per-account isolation" to the `kiro-go` module. Changes are listed **newest first**.

### 📜 一句话改了什么 / TL;DR

| 版本 | 重点 / Highlight |
|------|------|
| 🆕 **v2.6.5** | 隧道断线自动刷新，支持 `{session}` 轮换和冷却保护 |
| 🆕 **v2.6.4** | 全局/账号隧道代理，支持 Luminati/Bright Data、Rola 等 HTTP/SOCKS 隧道 |
| 🆕 **v2.6.3** | 后台新增 DNS 策略切换：auto/global/china/system/off |
| 🆕 **v2.6.2** | 账号卡测试默认严格测节点真实出口，不再被业务 fallback 掩盖 |
| 🆕 **v2.6.1** | DNS 覆盖表 · 代理测试显式标记 fallback · AliDNS/DNSPod DoH |
| 🆕 **v2.6** | 🛡️ DNSGuard 抗污染 · Clash 节点失败自动回退到全局跳板 · 熔断管理标签页 |
| 🆕 **v2.5** | 测试接口下拉框（含 geosurf / Kiro 直测）· 「联通」改为「连接成功」|
| **v2.4** | 🔗 **链式 dial 真的实现了** · jump → node → target 三跳跑通 |
| **v2.3** | UI 缓存竞态修复 · 账号卡到期 / 重置日期 · ss/vmess 用法说明 |
| **v2.2** | 跳板 +ss/+vmess · 跳板「测试」按钮 · 前端正则修复 |
| **v2.1** | 跳板 +trojan · 跳板热加载 · 卡片清零废行 |
| **v2** | 🌐 内嵌 mihomo (Clash.Meta) 内核 · 订阅缓存 · 每账号节点绑定 + 联通性测试 · 3 列响应式网格 |
| **v1** | 单账号 HTTP/SOCKS5 代理 |

---

## ♻️ v2.6.5 — 隧道自动刷新 / Tunnel auto-refresh

隧道代理支持 `{session}` 占位符。连续出现 EOF、timeout、407、429、5xx 等隧道异常达到阈值后，程序会自动生成新 session，下一次请求使用新隧道会话。

示例：

```text
http://brd-customer-xxx-zone-xxx-session-{session}:password@brd.superproxy.io:22225
```

后台「全局隧道代理」卡片新增：

- 断线自动刷新开关
- 连续失败阈值
- 刷新冷却秒数
- 当前 session / 失败次数 / 最近错误 / 最近刷新时间

没有 `{session}` 的固定隧道仍会记录失败并进入冷却，但无法真正更换代理商出口。

---

## 🌐 v2.6.4 — 隧道代理并存 / Tunnel proxies alongside Clash

后台新增「全局隧道代理」，账号详情新增「账号隧道代理」。它们面向 Bright Data/Luminati、Rola、Oxylabs 等主流隧道代理服务，支持 `http://user:pass@host:port`、`https://...`、`socks5://...`、`socks5h://...`。

出口选择顺序：

- 账号专属隧道
- Clash 订阅节点
- 老的账号代理 `proxyUrl`
- 全局隧道代理
- 直连

如果账号绑定了 Clash 节点，业务请求仍优先走节点；节点出现 EOF、timeout、connection refused 等传输错误时，会优先回退到账号/全局隧道。账号卡严格测试默认只测当前主出口，避免把回退出口误判成节点出口。

---

## 🧭 v2.6.3 — DNS 策略切换 / DNS strategy switch

设置页新增「DNS 策略」下拉，保存后会立即重载缓存订阅节点：

- `auto`：Cloudflare → Google → AliDNS → DNSPod，默认策略。
- `global`：只走 Cloudflare / Google，适合海外 VPS。
- `china`：只走 AliDNS / DNSPod，适合国内网络或海外 DoH 被阻断时尝试。
- `system`：只用系统 DNS，但会拒绝 `127.0.0.1`、内网、链路本地、组播等污染结果。
- `off`：关闭自动解析改写，只使用手动 DNS 覆盖表。

Manual DNS overrides still have the highest priority in every strategy.

---

## 🧪 v2.6.2 — 测试严格模式 / Strict proxy test mode

修正账号卡「测试」按钮的语义：它现在默认只测试当前账号绑定的节点真实出口，不再自动使用业务请求里的运行时 fallback。这样如果节点因为 DNS 污染解析到 `127.0.0.1`，测试会直接失败并显示真实错误，而不是被全局跳板兜底成同一个出口 IP。

Real Kiro requests still keep runtime fallback for availability, but admin proxy tests are now strict by default. Add `fallback=1` to the test API only when you explicitly want to verify the fallback chain.

---

## 🛠️ v2.6.1 — DNS 覆盖 + fallback 透明化 / DNS overrides + visible fallback

这一版修复一个容易误判的问题：当 Clash 节点因为 DNS 污染或连接失败不可用时，运行时会按设计回退到全局跳板；如果测试接口返回的是跳板 IP，旧 UI 会把它显示成普通成功，容易误以为“节点出口成功”。现在后台会明确显示 `clash+fallback` 和 `fallback: global jump`。

This release makes runtime fallback visible. If a Clash node fails and the request succeeds through the global jump, the admin UI now shows `clash+fallback` plus `fallback: global jump`, so the returned IP is not mistaken for the node's egress IP.

### 加了什么 / New

- **DNS 覆盖表**：设置页新增「DNS 覆盖（抗污染）」输入框。每行写 `域名 IP`，支持 `*.example.com` 通配。保存后会立即重载缓存订阅。
- **fallback 标识**：账号代理测试如果实际走了账号代理或全局跳板兜底，返回 JSON 会带 `fallback` 字段，UI 显示 `clash+fallback`。
- **更多 DoH Provider**：DNSGuard 现在除了 Cloudflare / Google，还会尝试 AliDNS 和 DNSPod，并为它们做 bootstrap 直连。

- **DNS overrides**: Settings now has a persistent override table. Use `host IP` per line; wildcard suffixes like `*.example.com` are supported. Saving reloads cached subscription nodes.
- **Visible fallback**: Proxy tests now expose the runtime fallback transport through the JSON `fallback` field and UI text.
- **More DoH providers**: DNSGuard now tries AliDNS and DNSPod in addition to Cloudflare and Google, with bootstrap IP dialing.

### 重要说明 / Important

如果无跳板时节点域名被污染成 `127.0.0.1`，且公共 DoH 也查不到真实 A 记录，程序不能凭空推导真实节点 IP。解决方式是把真实 IP 固化到「DNS 覆盖（抗污染）」里，例如：

```text
bepshbgp01.114837322.xyz 真实IP
bepgzbgp01.114837322.xyz 真实IP
```

只有当这些域名确实共用一个入口 IP 时，才建议使用通配：

```text
*.114837322.xyz 真实IP
```

### 新增 API / New APIs

| API | 用途 |
|-----|------|
| `GET /admin/api/dns-overrides` | 获取 DNS 覆盖表 |
| `POST /admin/api/dns-overrides` | 替换 DNS 覆盖表，并立即重载缓存订阅 |

---

## 🛡️ v2.6 — DNSGuard 抗污染 + 失败回退 + 熔断管理 / DNSGuard + fallback + circuit breaker

这一版专门处理 VPS / 跳板机 / 订阅节点域名被污染时的稳定性问题。目标是：下次在别的机器上安装时，不需要再手工改 hosts 或临时猜测哪里被污染，程序自己先做干净解析、链路失败时再自动兜底。

This release hardens VPS installs where either subscription node domains or the jump-host domain can be poisoned. The app now tries clean resolution first, then falls back automatically when a Clash node path still fails.

### 加了什么 / New

- **DNSGuard 抗污染解析**：Clash 订阅节点和全局跳板都会先检查系统 DNS 结果；如果解析到 `127.0.0.1`、内网、链路本地、组播等明显污染地址，会改用 Cloudflare / Google DoH 解析。
- **DoH bootstrap**：DoH 客户端不会再依赖系统 DNS 解析 `cloudflare-dns.com` / `dns.google`，而是直连 `1.1.1.1` / `8.8.8.8`，避免“连 DoH 域名也被污染”的二次问题。
- **保留 SNI/ServerName**：当节点或跳板域名被改写为干净 IP 时，原域名会保存在 `sni` / `servername` / `server-name`，TLS 仍按原域名握手。
- **Clash 节点运行时回退**：账号绑定 Clash 节点后，如果请求出现 `EOF`、timeout、connection reset/refused 等链路错误，会自动重试一次：优先走账号 `proxyUrl`，否则走全局跳板。
- **熔断管理标签页**：后台新增「熔断」tab，可配置失败阈值、错误率窗口、打开时长、半开恢复次数，并能手动熔断/解除单个账号。

- **DNSGuard** checks Clash subscription nodes and global jump hosts. If system DNS returns loopback/private/link-local/multicast addresses, it resolves through Cloudflare / Google DoH.
- **DoH bootstrap** dials `1.1.1.1` / `8.8.8.8` directly for the DoH providers, so a poisoned resolver cannot poison the resolver lookup itself.
- **SNI is preserved** when a proxy server hostname is rewritten to a clean IP.
- **Runtime fallback** retries failed Clash-node requests once through per-account `proxyUrl`, then the global jump.
- **Circuit Breaker tab** adds configurable account-level isolation and manual open/close controls.

### 本次 VPS 实测 / Verified on VPS

| 项目 | 结果 |
|------|------|
| 全局跳板 `trojan://...@oracleus1.adaosb.xyz:443?sni=...` | ✅ DNSGuard 将域名改写为干净 IP，同时保留 SNI；`Kiro API (codewhisperer)` 直测成功 |
| 订阅节点域名 `*.114837322.xyz` | 系统 DNS 返回 `127.0.0.1`，DNSGuard 识别为污染；公共 DoH 对这批域名没有返回可用 A 记录 |
| 账号绑定污染节点后测试 Kiro | ✅ Clash 节点失败后自动回退全局跳板，Kiro 直测成功 |
| HTTP 跳板 `http://admin:...@100.98.245.39:3128` | 可访问部分 geo 接口，但对 Kiro CONNECT 后 TLS 超时；程序保留错误提示，建议继续用 trojan 跳板 |

> 注意：如果一个机场节点域名在公共 DoH 上也没有任何可用 A 记录，程序无法凭空知道真实 IP。这种情况下 v2.6 的正确策略是：识别污染 → 不信任坏地址 → 运行时回退到可用全局跳板，保证 Kiro API 测试和业务调用继续工作。

### 新增 API / New APIs

| API | 用途 |
|-----|------|
| `GET /admin/api/breaker` | 获取熔断配置和账号熔断状态 |
| `POST /admin/api/breaker` | 更新熔断配置 |
| `POST /admin/api/breaker/accounts/:id/open` | 手动熔断账号 |
| `POST /admin/api/breaker/accounts/:id/close` | 解除账号熔断并清除短冷却 |

### v2.6 修改的文件 / v2.6 files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/clash/dns.go` | 新增 DNSGuard：污染地址检测、DoH 查询、DoH bootstrap、SNI 保留 |
| `kiro-go/clash/jump.go` | 全局跳板解析时接入 DNSGuard |
| `kiro-go/clash/manager.go` | 订阅节点解析时接入 DNSGuard |
| `kiro-go/clash/account.go` | Clash 节点请求失败后自动回退账号代理/全局跳板 |
| `kiro-go/config/config.go` | 新增 `CircuitBreakerConfig` 和持久化配置 |
| `kiro-go/pool/account.go` | 新增账号级 closed/open/half-open 熔断状态、错误率窗口、手动开关 |
| `kiro-go/proxy/handler.go` | 新增 `/admin/api/breaker` 管理 API |
| `kiro-go/web/index.html` | 新增「熔断」标签页、配置表单和账号状态卡片 |

---

## 🧪 v2.5 — 测试接口可选 + Kiro 直测 / Pickable test endpoint + Kiro direct probe

v2.4 验证了链 dial 跑通，但「测试」按钮在节点屏蔽 ipinfo 类接口时会全军覆没。这一轮把测试改成可选：

After v2.4 chain dial works, but the "Test" button still kept failing on nodes that blacklist ipinfo-family endpoints. This round makes the probe target user-pickable:

### 加了什么 / New

- **测试接口下拉框**：账号卡的「代理」行 + 设置页跳板那一行都新增一个下拉，包含 16 个候选 endpoint。空选项 = 「自动 (依次回退)」（v2.4 行为，按顺序试到一个成功为止）。
- **包含 [geo.geosurf.io](https://geo.geosurf.io/)** 这个新 endpoint，加上 ipinfo / ifconfig.co / api.ip.sb / api.myip.com / ipify (api64 + api) / ipapi.co / ip-api.com / httpbin.org/ip / icanhazip.com / checkip.amazonaws / cloudflare trace ×2 共 14 个 geo 类。
- **🎯 Kiro 直测选项**：下拉里有两条 `[kiro]` 标记的选项 — `Kiro API (codewhisperer)` 和 `Kiro API (q)`，直接打到 `codewhisperer.us-east-1.amazonaws.com` / `q.us-east-1.amazonaws.com` 根目录。任何 HTTP 响应（包括预期中的 403/404）= 链路通；这是判断「Kiro 能不能用」最直接的探针，不依赖第三方 geo 服务。
- **「联通」→「连接成功」/ "OK" → "Connected"**，更直白。

- **A test-endpoint dropdown** appears in the proxy row of every account card and next to the outbound jump's Test button. 16 candidate endpoints; the empty option = "Auto (fallback)" which keeps v2.4 behavior.
- **Includes `geo.geosurf.io`** plus ipinfo / ifconfig.co / ip.sb / api.myip.com / ipify (api64 + api) / ipapi.co / ip-api.com / httpbin.org/ip / icanhazip.com / checkip.amazonaws / cloudflare trace ×2 — 14 geo probes total.
- **🎯 Two `[kiro]` options** that hit `codewhisperer.us-east-1.amazonaws.com` and `q.us-east-1.amazonaws.com` directly. Any HTTP response (403/404 expected without auth) = path is reachable. This is the most honest "can Kiro work?" probe, independent of third-party geo services.
- **Success label changed**: "联通" → "连接成功" (zh) / "OK" → "Connected" (en).

### 怎么用 / Usage

1. 想看节点真实出口 IP/Geo？下拉选 `ipinfo.io` 或 `geo.geosurf.io`，按测试。
2. 节点屏蔽 ipinfo 家族？换 `cloudflare trace (cf.com)` 或 `httpbin.org/ip`。
3. 只关心「Kiro API 能不能调」？选 `Kiro API (codewhisperer)`。返回 HTTP 4xx 都算成功 — 说明 TCP + TLS + HTTP 三层都穿过链路了。
4. 不知道选哪个？留「自动 (依次回退)」就行。

### v2.5 修改的文件 / v2.5 files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/proxy/clash_handlers.go` | `runProxyTest` 增加 `pickName string` 参数；新增 `proxyTestEndpoints()` 注册 16 个候选；`testEndpoint` 结构含 `IsTrace` / `IsPlainIP` / `IsKiroPing` 三种解析模式；新增 `apiGetTestEndpoints` 给 UI 用；`apiTestOutbound` / `apiTestAccountProxy` 现在读 `?endpoint=` 查询参数 |
| `kiro-go/proxy/handler.go` | 注册 `GET /admin/api/test-endpoints` 路由 |
| `kiro-go/web/index.html` | 新增 `loadTestEndpoints()` / `testEndpointSelectOptions()` / `renderTestEndpointSelect(elId)`；账号卡 + 跳板设置区都接上下拉；`testAccountProxy` 和 `testOutbound` 把 `?endpoint=` 拼到请求里；i18n: `accounts.proxyTestOK` 改为「连接成功」/「Connected」；新增 `settings.testEndpoint` / `settings.testEndpointAuto` 文案 |

---

## 🔗 v2.4 — 链式 dial 实现 / Chain dial actually works

把 v2.1 ~ v2.3 一直挂着的「先跳板 → 再节点 → 再目标」做完了，可以正式从 README 里删掉那一段「未实现」的免责声明。

The "jump → node → target" chain that I'd been calling out as unimplemented through v2.1-v2.3 is finally done.

### 怎么做到的 / How

- 不走反射、不改 mihomo 源码。**完全用 mihomo 自己已有的 `dialer-proxy` 字段**：
  1. 解析订阅 YAML 时，把跳板配置以 `__kiro_jump__` 名字拼到 `proxies:` 列表最前
  2. 给 **每一个真实节点** 的 config map 注入 `dialer-proxy: __kiro_jump__`
  3. mihomo 内部的 `proxydialer.NewByName("__kiro_jump__")` 在 dial 时去 `tunnel.Proxies()` 查名字 — 所以解析完一次性 `tunnel.UpdateProxies(...)` 注册进去
  4. 每次 jump 改变 → 自动从磁盘缓存重读订阅、重新打 stamp、重新注册，不需要重拉网络

- No reflection, no mihomo patching. **All native mihomo `dialer-proxy` field**:
  1. At subscription parse time, prepend the jump as `__kiro_jump__` in the `proxies:` list
  2. Stamp `dialer-proxy: __kiro_jump__` onto every real node's config map
  3. mihomo's `proxydialer.NewByName("__kiro_jump__")` resolves names through `tunnel.Proxies()` at dial time — so we call `tunnel.UpdateProxies(...)` once after each parse to register
  4. Every jump change re-parses the cached YAML, re-stamps, re-registers — no network round-trip

### 实测怎么验证的 / How I verified

VPS 上 SG 出口 → 配置 jump=`trojan://...@oracleus1.adaosb.xyz:443?sni=...`（Oracle Virginia） → 给账号绑一个香港节点：

| 场景 | 现象 |
|------|------|
| jump 清空 + 香港节点 | DNS 污染：`bepgzbgp01.114837322.xyz:14091 connect error: dial tcp 127.0.0.1:14091`（VPS 直连节点失败）|
| jump=Oracle US + 香港节点 | `proxyName[__kiro_jump__] not found` ❌（修复前 — 缺少 tunnel 注册）|
| 修复后：jump=Oracle US + 香港节点 | ✅ 错误消失，dial 时间从 86ms 飙到 2052ms — 这是 SG → Virginia → HK → 目标 三跳的 RTT，链生效|
| **🎯 端到端：真实 Kiro API 调用** | ✅ `POST /v1/messages` model=`claude-opus-4.7` 返回 `"chained"`，链路：**SG VPS → trojan(Oracle Virginia) → JP 节点 → codewhisperer.us-east-1.amazonaws.com** |

VPS egress is Singapore. With `jump = trojan://...@oracleus1.adaosb.xyz:443?sni=...` (Oracle Virginia) and an account bound to a Hong Kong node:

| Scenario | Behavior |
|----------|----------|
| Jump cleared + HK node | DNS hijack: `bepgzbgp01.114837322.xyz:14091 connect error: dial tcp 127.0.0.1:14091` (VPS can't reach the HK node directly) |
| Jump=Oracle US + HK node | `proxyName[__kiro_jump__] not found` ❌ (pre-fix, missing tunnel registration) |
| After fix: Jump=Oracle US + HK node | ✅ Name error gone; dial time jumps from 86ms to 2052ms — that's the SG → Virginia → HK → target three-hop RTT |
| **🎯 End-to-end: real Kiro API call** | ✅ `POST /v1/messages` with model=`claude-opus-4.7` returned a proper claude reply. Path: **SG VPS → trojan(Oracle Virginia) → JP node → codewhisperer.us-east-1.amazonaws.com** |

### ⚠️ 一个已知的二次问题 / Known follow-up

链通了之后，「测试」按钮里的 `api.ip.sb / ipinfo.io / ifconfig.co / api.myip.com` 全部 EOF — 这是节点运营商屏蔽 geo-IP 服务这一类目标，跟链路实现无关。**Kiro 上游（AWS CodeWhisperer）不在被屏蔽的范围里**，所以真正用 Kiro 完全没问题。v2.2 加的 Cloudflare trace endpoint 在某些节点上能解决这个，但不是普适。

After the chain is up, the connectivity-test button still EOFs against `api.ip.sb / ipinfo.io / ifconfig.co / api.myip.com` — node operators blacklist that whole family of geo-IP services. **Kiro's upstream (AWS CodeWhisperer) is NOT on those blocklists**, so the actual Kiro API path works regardless. The v2.2 Cloudflare trace fallback works on some nodes but isn't universal.

### v2.4 修改的文件 / v2.4 files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/clash/jump.go` | `parseJumpURL` 拆成 `jumpConfigFor(raw, name)`（返回 mihomo 的 map[string]any 配置）+ `parseJumpURL`（包一层 `adapter.ParseProxy`），让订阅注入路径能直接拿到原始 cfg map |
| `kiro-go/clash/manager.go` | `parseSubscription(raw, jumpRawURL)` 接受 jump 参数；jump 非空时给每个节点 config map 复制并注入 `dialer-proxy: __kiro_jump__`；`commit()` 把 jump 一起塞进 `tunnel.UpdateProxies(...)`；`SetJump` 改完热重读 cache 重新解析；预留 `__kiro_jump__` 名字防止订阅意外撞名 |

> 旧版 README 在每个版本里都写「节点级链式 dial 未实现」的免责，**v2.4 起这段过期了，已从前面几个版本的章节里删除以避免误导。**

---

## 🆕 v2.3 — 缓存竞态 + 到期日期 + ss/vmess 用法 / Cache race + expiry dates + ss/vmess docs

### 修了什么 / Bug fixes

- 🐛 **修：刷新浏览器后下拉里只剩「直连」+ (missing) 节点**
  - **症状**：订阅 90 个节点正常加载到磁盘缓存（`data/clash-cache.yaml`），后端 `/admin/api/clash` 也返回 `loaded: 90`。但你按 F5 → 账号卡的代理下拉只剩"直连"和"(missing) 高级 专线 美国 03"。
  - **根因**：前端 `loadData()` 把 `loadStats / loadAccounts / loadSettings / loadVersion / loadClash` 全塞进 `Promise.all`，竞态导致 `renderAccounts()` 先于 `loadClash()` 完成。`renderProxySelect()` 拿到的是空 `clashStatus.names`，所以下拉里只剩当前绑定那个并标 `(missing)`。
  - **修法**：`loadData()` 现在先 `await loadClash()` 再并发载剩下的，渲染顺序 = Clash 节点列表 → 账号卡片。后端没动，0 风险。

- 🐛 **Bug fix: dropdowns lost everything after F5 (only "Direct" + "(missing)" left)**
  - The disk cache works fine and `/admin/api/clash` returns `loaded: 90`. The bug was a frontend race: `Promise.all([loadStats, loadAccounts, loadSettings, loadVersion, loadClash])` rendered account cards before `clashStatus.names` was populated. Fix: `await loadClash()` first, then parallel-load the rest.

### 加了什么 / New

- 📅 **账号卡右下角现在显示重要日期**：试用到期 🎁 / 配额重置日 🔄 / 订阅剩余天数 ⏳。优先级 trial → reset → days-remaining，没数据就让那块塌缩。

- 📅 **Account cards now show important dates in the bottom-right corner**: trial expiry 🎁 / quota reset 🔄 / subscription days remaining ⏳. Falls back to nothing when the data isn't known.

### 🌐 全局跳板用法 / Global Jump Usage

⚠️ **重要：跳板的真实作用范围**

跳板（Global Jump Host）当前生效于 **订阅 YAML 拉取** 这一条出站路径上。每账号绑定到 Clash 节点后，**节点本身的 dial 不经过跳板** —— 这条「先跳板 → 再节点 → 再目标」的链式 dial 还没实现（mihomo 的 `ProxyAdapter` 接口没有暴露干净的「上游 dialer」注入点；`proxydialer` 包能给一个 `C.Dialer`，但要喂回每个 `outbound` 的 `BasicOption.DialerForAPI` 字段，而我们并没有从 mihomo 完整的 `Tunnel` runtime 里拿到这些 `outbound` 对象）。

如果你的 VPS 直连节点失败（DNS 污染、ISP 黑洞），目前的 workaround：
1. 用 v1 的「账号代理」字段（账号详情弹窗里）给账号配一个能联通的 http/socks5 跳板
2. 或者跑一个独立的 mihomo 容器，把节点都挂在它上面，账号代理填 `socks5://mihomo:7890`

⚠️ **What the jump actually does**

The global jump applies to **subscription YAML fetches only** today. After accounts are bound to Clash nodes, **the node's own dial does NOT chain through the jump** — proper "jump → node → target" chain dial is still unimplemented (mihomo's public `ProxyAdapter` interface doesn't expose a clean upstream-dialer injection point). Workaround: use v1's per-account proxy field instead, or run a separate mihomo sidecar.

### 📝 跳板 URL 格式速查 / Jump URL cheatsheet

支持 6 种 scheme，可以直接从机场订阅复制粘贴：

```
http://[user:pass@]host:port
https://[user:pass@]host:port
socks5://[user:pass@]host:port
socks5h://[user:pass@]host:port
trojan://password@host:443?sni=example.com[&skip-cert-verify=true&alpn=h2,http/1.1]
ss://base64(method:password)@host:port[#name]              ← SIP002 (推荐)
ss://base64(method:password@host:port)[#name]              ← legacy
ss://method:password@host:port                             ← plaintext
vmess://base64(JSON)                                       ← V2RayN，base64 解码后是 JSON
```

#### 🟦 ss 示范 / Shadowsocks examples

```bash
# SIP002 (推荐写法)
# 用户信息部分 = base64("aes-256-gcm:my-secret-pass") = "YWVzLTI1Ni1nY206bXktc2VjcmV0LXBhc3M="
ss://YWVzLTI1Ni1nY206bXktc2VjcmV0LXBhc3M=@1.2.3.4:8388

# 明文（最直观，机场订阅基本不用）
ss://aes-256-gcm:my-secret-pass@1.2.3.4:8388

# 想测能不能解析？粘进设置页保存一下，能保存说明解析通过；接着按 「测试」 看真实联通性。
```

#### 🟪 vmess 示范 / VMess example

```jsonc
// 第一步：写出 V2RayN 标准 JSON
{
  "v": "2",
  "ps": "🇯🇵 jp-test",
  "add": "1.2.3.4",
  "port": 443,
  "id": "d3c8f4f8-3e3a-4b1f-8c8b-1d4f6a7b9e2c",
  "aid": 0,
  "scy": "auto",
  "net": "ws",          // ws / tcp / grpc / ...
  "type": "none",
  "host": "cdn.example.com",
  "path": "/ray",
  "tls": "tls",
  "sni": "cdn.example.com"
}
```

```bash
# 第二步：把 JSON 整体 base64，贴 vmess:// 前缀
vmess://eyJ2IjoiMiIsInBzIjoi8J+HrPCfh7Qgc2FtcGxlIiwiYWRkIjoiMS4yLjMuNCIsInBvcnQiOjQ0MywiaWQiOiJkM2M4ZjRmOC0zZTNhLTRiMWYtOGM4Yi0xZDRmNmE3YjllMmMiLCJhaWQiOjAsInNjeSI6ImF1dG8iLCJuZXQiOiJ3cyIsInR5cGUiOiJub25lIiwiaG9zdCI6ImNkbi5leGFtcGxlLmNvbSIsInBhdGgiOiIvcmF5IiwidGxzIjoidGxzIiwic25pIjoiY2RuLmV4YW1wbGUuY29tIn0=
```

字段映射 / Field mapping:

| V2RayN 字段 | mihomo proxy 字段 |
|-------------|-------------------|
| `add` | `server` |
| `port` | `port` |
| `id` | `uuid` |
| `aid` | `alterId` |
| `scy` | `cipher` (默认 auto) |
| `net` | `network` (tcp/ws/grpc...) |
| `tls`=`"tls"` | `tls: true` |
| `sni` 或 `host` | `servername` |
| `host` | `ws-opts.headers.Host` |
| `path` | `ws-opts.path` |

### 🟩 trojan 示范 / Trojan example

```bash
# 最常见的形式 (默认 SNI = host)
trojan://Sw0rdF1sh@example.com:443

# 自定义 SNI
trojan://Sw0rdF1sh@1.2.3.4:443?sni=example.com

# 跳过证书校验 + 指定 ALPN
trojan://Sw0rdF1sh@1.2.3.4:443?sni=example.com&skip-cert-verify=true&alpn=h2,http/1.1
```

### v2.3 修改的文件 / v2.3 files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/web/index.html` | `loadData()` 改为 await Clash → 再并发其他；新增 `formatAccountSchedule(a)` 在卡片右下角显示 🎁 trial 到期 / 🔄 重置日 / ⏳ 剩余天数 |

---

## v2.2 — ss/vmess 跳板 + 跳板测试按钮 + 前端正则修复 / ss/vmess jump + jump test button + frontend regex fix


紧接 v2.1 的修复轮：

Quick fix round after v2.1:

- **跳板新增 ss / vmess 协议支持**。Shadowsocks 接受三种常见编码（SIP002 base64、legacy 整体 base64、明文）；vmess 接受 V2RayN 标准的 base64-JSON 形式（含 ws/tls/sni/host）。加上 v2.1 的 trojan，配合 http/https/socks5 已经覆盖大多数订阅里能直接拼出来的协议。
- **修复前端正则**：v2.1 后端虽然接受 `trojan://`，但前端 `saveOutbound()` 的校验正则忘了加 `trojan|ss|vmess`，把合法 URL 卡在客户端，根本没到后端 — 这就是「填进去说要 socks 或 http 开头」的真正原因。现在前后端正则统一为 `^(https?|socks5h?|trojan|ss|vmess)://[^\s]+`。
- **跳板设置区新增「测试」按钮** + `POST /admin/api/outbound/test` 端点。一键发请求到 `ipinfo.io / ifconfig.co / ip.sb`（依次 fallback），把延迟 / IP / 国家 / 城市 / ASN 显示在按钮下方，肉眼一秒确认跳板真的活着。

What changed:

- **Jump now also accepts ss:// and vmess://**. Shadowsocks: SIP002 base64, legacy single-blob base64, or plaintext userinfo — all three. VMess: V2RayN-style base64-JSON, with ws / tls / sni / host header support. Combined with v2.1's trojan + http/https/socks5, this covers the protocols you can name-paste from a typical subscription.
- **Frontend regex fix**: v2.1 made the backend accept `trojan://`, but the frontend `saveOutbound()` validator regex still hard-coded the old set, so legal URLs got bounced client-side and never reached the backend — that's why you saw "must start with socks5:// or http://". Front and back now share `^(https?|socks5h?|trojan|ss|vmess)://[^\s]+`.
- **Jump test button + `POST /admin/api/outbound/test` endpoint**. One click probes `ipinfo.io / ifconfig.co / ip.sb` (fallback chain) through the configured jump and displays latency / IP / country / city / ASN inline. Verified live: a trojan jump pointing at `oracleus1.adaosb.xyz:443` reported `150.136.98.170 · US / Virginia / Ashburn · AS31898 Oracle · 1004ms` first try.

### v2.2 修改的文件 / v2.2 files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/clash/jump.go` | 重构 `parseJumpURL`，按 scheme 路由：`parseStdJump` (http/https/socks5/trojan) / `parseSsJump` (ss 三种形式) / `parseVmessJump` (vmess V2RayN JSON)。新增 `decodeFlexBase64`（试 std/raw/url/raw-url，带 padding 修复）、`splitHostPort`、`coerceInt` 工具 |
| `kiro-go/proxy/clash_handlers.go` | 新增 `apiTestOutbound`：基于 `clash.ClientForJumpOnly` 跑联通性 + Geo 探测；`urlParseStrict` 接收 ss / vmess |
| `kiro-go/proxy/handler.go` | 注册 `POST /admin/api/outbound/test` 路由 |
| `kiro-go/web/index.html` | `saveOutbound` 正则统一为 `^(https?|socks5h?|trojan|ss|vmess)://[^\s]+`；跳板设置区加 「测试」 按钮 + 结果显示行；i18n 文案精简（删掉关于「所有出站走跳板」的过度承诺，改为说订阅拉取） |

---



紧接 v2 后的小步迭代：

Small follow-up to v2:

- **跳板支持 trojan**（除了 http/https/socks5）。设置页填 `trojan://密码@host:443?sni=example.com&skip-cert-verify=true&alpn=h2,http/1.1`，由 mihomo 内核负责协议握手——无需额外 sidecar。
- **跳板作用范围讲清楚**：跳板生效在 **订阅 YAML 拉取** 和 **`ClientForJumpOnly` 这条「单跳」路径** 上。**节点级链式 dial（dial(jump) → tunnel-to-node → node-handshake → target）暂未实现** —— mihomo 当前 `ProxyAdapter` 接口没有暴露允许外部注入「上游 dialer」的方法（`dialer-proxy` 字段需要把 jump 注册进 mihomo 自己的 Tunnel runtime，本项目只把它当库用）。我尝试了用 `StreamConnContext` 自行链式 dial，编译失败后撤回，老实写在文档里。如果你的 VPS 直连屏蔽节点，目前的可行解：用 v1 的「账号代理」字段直接给账号配一个能联通的 http/socks5。
- **`/admin/api/outbound` POST 现在直接热加载 jump 到内存**，不需要重启容器。
- **账号卡片去掉零数据行**：`0 请求 · 0 tok · 0 cr` 这种新账号永远是 0 的占位栏被砍掉，腾出位置给真正用得到的内容。

What changed:

- **Jump now supports trojan** (in addition to http/https/socks5). Format: `trojan://password@host:443?sni=...&skip-cert-verify=true&alpn=h2,http/1.1`. Mihomo handles the handshake — no sidecar needed.
- **Jump scope is documented honestly**: the jump applies to **subscription YAML fetches** and the **`ClientForJumpOnly` single-hop path**. **Node-level chain dial (dial(jump) → tunnel-to-node → node handshake → target) is NOT implemented** — mihomo's current `ProxyAdapter` interface does not expose a way to inject an upstream dialer from outside its Tunnel runtime. I attempted it via `StreamConnContext`, failed to compile, and rolled back rather than ship something half-working. Workaround if your VPS can't reach Clash nodes directly: use v1's per-account `proxyUrl` to point at a reachable http/socks5.
- **`POST /admin/api/outbound` hot-installs the jump into memory** — no container restart.
- **Removed the always-zero stats line** from account cards (`0 requests · 0 tok · 0 cr`).

### v2.1 修改的文件 / v2.1 files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/clash/jump.go` (**新**) | `parseJumpURL` 把 URL 转成 mihomo `C.Proxy`；trojan 支持 `?sni=&skip-cert-verify=&alpn=` 查询参数 |
| `kiro-go/clash/manager.go` | `Manager` 新增 `jump`/`jumpRawURL`/`jumpLastErr` 字段，`SetJump`/`JumpURL`/`JumpError` 公开方法；`Init` 启动时 SetJump；`fetchSubscription` 改走 jump 的 `DialContext`（兼容 trojan） |
| `kiro-go/clash/dial.go` | 新增 `ClientForJumpOnly`（单跳直接走 jump，用于联通性测试 fallback）；移除半成品的 chain-dial 路径 |
| `kiro-go/proxy/clash_handlers.go` | `apiUpdateOutbound` 现在调 `clash.Default().SetJump`，热加载；`urlParseStrict` 接收 `trojan://` |
| `kiro-go/web/index.html` | 跳板说明文案改为「前置 / Prepend」并提到 trojan 例子；账号卡片底部那行 0/0/0 占位栏删除 |

---

## v2 — Clash 内核集成 + 全局跳板 + 紧凑 UI / Clash core integration + global jump host + compact UI

把 **Clash.Meta (mihomo) 内核作为 Go 库** 直接编进 `kiro-go`，单容器同时跑「API 网关」+「Clash 客户端」。账号可以在网页上一键绑定订阅里的任意节点（ss / vmess / vless / trojan / hysteria2 / tuic 等），不再受限于 http/socks5。

Embeds the **Clash.Meta (mihomo) core as a Go library** so a single `kiro-go` container is *both* the API gateway *and* the Clash client. Accounts can bind to any node from a Clash subscription (ss / vmess / vless / trojan / hysteria2 / tuic, etc.), not just http/socks5.

### 一图看懂 / What it looks like

```
┌─ Clash 订阅（全局）─────────────────────────────────┐
│ 订阅 URL: [https://...]                  [刷新] [保存并加载] │
│ 已加载 90 个节点 · 更新于 2026/05/13 18:04:15        │
└──────────────────────────────────────────────────────┘
账号卡片 (3 列网格)
┌─────────────────────┐ ┌─────────────────────┐ ┌─────────────────────┐
│ li***@gm***.com     │ │ sa***@ou***.com     │ │ mu***@be***.com     │
│ PRO Google 正常     │ │ PRO Google 正常     │ │ PRO Google 正常     │
│ ████████░ 80% / 26m │ │ ░░░░░░░░░ 1% / 58m  │ │ ░░░░░░░░░ 0% / 34m  │
│ 代理: [🇺🇸 US-01 ▼][测试]│ │ 代理: [🇯🇵 JP-01 ▼][测试]│ │ 代理: [直连 ▼][测试]    │
│ 权重: [100][保存]   │ │ 权重: [100][保存]   │ │ 权重: [100][保存]   │
└─────────────────────┘ └─────────────────────┘ └─────────────────────┘
```

### 新增功能 / New features

1. **Clash 订阅（全局）** — 首页面板填订阅 URL → 后端调 mihomo 解析 YAML（自动识别 base64 包装）→ 节点名暴露给账号下拉。订阅 YAML 在第一次成功加载时**写入本地缓存**（`data/clash-cache.yaml`），重启或刷新页面后**先用缓存**渲染，再后台异步重新拉取——网络抖动不会让你「页面一刷就空」。

2. **每账号绑定节点** — 账号卡片的「代理」下拉直接列出全部节点 + 「直连」。1:1 绑定，状态明确；账号绑定的节点如果在新一轮订阅里不存在了，下拉里仍然保留并标记 `(missing)` 而不是悄悄回退到直连。

3. **每账号联通性测试** — 「测试」按钮发一次到 `ipinfo.io / ifconfig.co / ip.sb` 的请求（依次 fallback），按当前账号配置走 Clash 节点 / proxyUrl / 直连，把出口 IP、国家、城市、ASN、延迟、模式（`clash` / `proxyUrl` / `direct`）原地显示在卡片上。肉眼一秒确认走的是哪。

4. **全局跳板代理（订阅拉取）** — 当 VPS 直连屏蔽订阅 CDN（比如某些机场要走特定区域）时，可以在「设置」里填一个 http(s)/socks5 跳板，订阅拉取专走它，不影响账号自身代理。

5. **响应式 3 列账号网格** — 账号卡片从「一行一个」改成 `grid-template-columns: repeat(auto-fill, minmax(360px, 1fr))`，桌面端能看到 3-4 个账号，800px 以下回退到单列。每张卡也精简了一遍：进度条和到期时间合并、状态条合并到 footer 一行。

6. **模型映射** — 设置页可以编辑「入站 model 名 → 上游 Kiro model 名」映射表（默认空表 = 透传，与上游行为一致）。⚠️ 截至本次提交，运行时映射在 `INVALID_MODEL_ID` 这条路径上还没完全生效，PR 欢迎；写盘 / API / UI 均已完成。

7. **`/v1/messages` 兼容性副作用：账号代理（v1）继续可用**，并且现在会按「Clash 节点 → proxyUrl → 直连」的顺序解析。

### 新增端点 / New admin endpoints

| 端点 | 用途 |
|------|------|
| `GET  /admin/api/clash` | 订阅状态、节点名列表、最近一次拉取时间、错误信息 |
| `POST /admin/api/clash` | 设置/清除订阅 URL（`{"subscriptionUrl": "..."}`），同步触发一次拉取 |
| `POST /admin/api/clash/refresh` | 重新拉取已配置的订阅 |
| `POST /admin/api/accounts/:id/proxy-test` | 触发该账号的联通性 + Geo 测试 |
| `GET  /admin/api/outbound` | 全局跳板代理 URL |
| `POST /admin/api/outbound` | 设置全局跳板代理（`{"url":"http://..."}`） |
| `GET  /admin/api/modelmapping` | 当前映射表（flat `{from: to}`） |
| `POST /admin/api/modelmapping` | 替换整个映射表 |
| `PUT  /admin/api/accounts/:id` | 增加 `proxyNode` 字段（绑定到 Clash 节点名） |

### 修改的文件 / Files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/clash/manager.go` (**新**) | mihomo 适配器：订阅拉取、YAML 解析、base64 兼容、节点 registry、本地缓存 |
| `kiro-go/clash/dial.go` (**新**) | 把 mihomo 的 `proxy.DialContext` 包装成 `http.Transport.DialContext`；按 (节点, 超时, 订阅 generation) 缓存 `*http.Client` |
| `kiro-go/clash/account.go` (**新**) | 解析顺序：`ProxyNode → ProxyURL → 直连`；短/长两种超时的 picker |
| `kiro-go/proxy/clash_handlers.go` (**新**) | 上面所有 admin 端点的实现，含联通性测试 + Geo 解析（兼容 ipinfo/ifconfig.co/ip.sb 三种字段） |
| `kiro-go/config/config.go` | 新增 `Account.ProxyNode`、`Config.ClashSubscriptionURL`、`Config.GlobalOutboundProxy`、`Config.ModelMapping`；对应 getter/setter；`MapModel(string)` |
| `kiro-go/auth/http_client.go` | `pickClientForAccount(*Account)`：根据 ProxyNode/ProxyURL 选客户端 |
| `kiro-go/auth/oidc.go` | `RefreshToken` 改签名，接收整个 `*Account` 并走 `pickClientForAccount` |
| `kiro-go/proxy/kiro.go` | 流式调用改用 `clash.PickAccountStreamClient(account)` |
| `kiro-go/proxy/kiro_api.go` | `GetUsageLimits` / `GetUserInfo` / `ListAvailableModels` 改用 `clash.PickAccountClient(account)` |
| `kiro-go/proxy/handler.go` | `apiUpdateAccount` 接受 `proxyNode` 字段（校验是否在订阅里）；`apiGetAccounts` 返回 `proxyNode`；新路由全部接到 mux |
| `kiro-go/proxy/translator.go` | `ParseModelAndThinking` 在 thinking-后缀剥离之后插一次 `config.MapModel`，让用户自定义优先 |
| `kiro-go/main.go` | 启动时调用 `clash.Init()`：先读缓存、再后台重拉 |
| `kiro-go/go.mod` | 升到 Go 1.22；新增依赖 `github.com/metacubex/mihomo v1.19.24`、`gopkg.in/yaml.v3` |
| `kiro-go/Dockerfile` | 升到 `golang:1.22-alpine`；加 `build-base git`（mihomo 部分包要 cgo 头文件）；`go mod tidy` 自动拉 mihomo 依赖 |
| `kiro-go/web/index.html` | 首页订阅卡片 + 设置页跳板 / 模型映射；3 列响应式网格；i18n（中/英） |

### 兼容性 / Compatibility

- **完全向后兼容**：旧 `config.json` 里没有 `proxyNode` / `clashSubscriptionUrl` / `globalOutboundProxy` / `modelMapping` 时全部当空处理，行为与上游一致。
- 镜像体积：~10MB → ~37MB（mihomo 内核 + 加密库）；常驻内存增加约 5MB。
- 第一次构建会拉一堆 Go 模块，时间 3-5 分钟；之后被 Docker layer 缓存后秒级。

---

## v1 — 单账号 HTTP/SOCKS5 代理 / Per-account HTTP/SOCKS5 proxy

为 `kiro-go` 里的每个账号单独设置 HTTP / HTTPS / SOCKS5 代理：

- 在 Admin Web UI 的账号详情弹窗里，「机器码」下方新增「账号代理」一栏
- 支持 `http://host:port`、`https://host:port`、`socks5://host:port`（以及 `socks5h://`）
- 留空 = 该账号走直连（默认行为保持不变）
- 代理作用于该账号的所有出站 HTTP（token 刷新 / usage / user-info / models / 流式 generateAssistantResponse）

Per-account HTTP / HTTPS / SOCKS5 proxy for every account in `kiro-go`. Leave empty for direct (default unchanged).

### v1 修改的文件 / v1 files changed

| 文件 | 改动 |
|------|------|
| `kiro-go/config/config.go` | `Account` 新增 `ProxyURL` (JSON `proxyUrl`) |
| `kiro-go/config/httpclient.go` | **新增**：按代理 URL 缓存的 `*http.Client` 工厂 |
| `kiro-go/auth/oidc.go` | `RefreshToken` 系列接收并使用 `account.ProxyURL` |
| `kiro-go/proxy/kiro_api.go` | usage / user-info / models 走账号代理 |
| `kiro-go/proxy/kiro.go` | 流式调用走账号代理 |
| `kiro-go/proxy/handler.go` | `apiUpdateAccount` 接受 `proxyUrl`；`apiGetAccounts` 返回 `proxyUrl` |
| `kiro-go/web/index.html` | 账号详情弹窗新增代理输入 / 保存 / 直连按钮 |

> v2 的 Clash 集成在 v1 基础之上层叠：账号绑定 Clash 节点时优先走节点；没绑或节点已不在订阅里时，回落到 v1 的 `proxyUrl`；都为空才直连。

---

## License

本 fork 继承上游的 license。所有新增和修改都属于对上游项目的增强。
This fork inherits the upstream license. All additions are enhancements on top of the upstream project.
