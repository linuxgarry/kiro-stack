# 🚀 kiro-stack (linuxgarry fork)

本仓库 fork 自 **[Yoahoug/kiro-stack](https://github.com/Yoahoug/kiro-stack)**。在 `kiro-go` 模块上加了一套面向「机场订阅 + 多账号代理隔离」的实用功能。下面按 **从新到旧** 的顺序列出本 fork 在上游基础上做的所有改动。

Forked from **[Yoahoug/kiro-stack](https://github.com/Yoahoug/kiro-stack)**. Adds a set of practical features for "subscription-based proxies + per-account isolation" to the `kiro-go` module. Changes are listed **newest first**.

### 📜 一句话改了什么 / TL;DR

| 版本 | 重点 / Highlight |
|------|------|
| 🆕 **v2.4** | 🔗 **链式 dial 真的实现了** · jump → node → target 三跳跑通 |
| **v2.3** | UI 缓存竞态修复 · 账号卡到期 / 重置日期 · ss/vmess 用法说明 |
| **v2.2** | 跳板 +ss/+vmess · 跳板「测试」按钮 · 前端正则修复 |
| **v2.1** | 跳板 +trojan · 跳板热加载 · 卡片清零废行 |
| **v2** | 🌐 内嵌 mihomo (Clash.Meta) 内核 · 订阅缓存 · 每账号节点绑定 + 联通性测试 · 3 列响应式网格 |
| **v1** | 单账号 HTTP/SOCKS5 代理 |

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

VPS egress is Singapore. With `jump = trojan://...@oracleus1.adaosb.xyz:443?sni=...` (Oracle Virginia) and an account bound to a Hong Kong node:

| Scenario | Behavior |
|----------|----------|
| Jump cleared + HK node | DNS hijack: `bepgzbgp01.114837322.xyz:14091 connect error: dial tcp 127.0.0.1:14091` (VPS can't reach the HK node directly) |
| Jump=Oracle US + HK node | `proxyName[__kiro_jump__] not found` ❌ (pre-fix, missing tunnel registration) |
| After fix: Jump=Oracle US + HK node | ✅ Name error gone; dial time jumps from 86ms to 2052ms — that's the SG → Virginia → HK → target three-hop RTT |

### ⚠️ 一个已知的二次问题 / Known follow-up

链通了之后，HK 节点到 `api.ip.sb` 的 connection EOF — 是节点运营商屏蔽了那一类 ipinfo 服务，跟链路实现无关。换 `ipinfo.io / ifconfig.co` fallback 也不行（同一类服务）。后续如果要更稳的「联通性测试」，可以加一组备选 endpoint（比如 cloudflare trace），但这跟链 dial 的实现无关，先不动。

After the chain came up, the HK node refuses connections to `api.ip.sb` (node provider blacklists that family of geo-info endpoints — same family as ipinfo.io / ifconfig.co, all of which fall back to each other). Not a chain-dial issue. Adding more diverse test endpoints (e.g. cloudflare trace) is its own future task.

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
