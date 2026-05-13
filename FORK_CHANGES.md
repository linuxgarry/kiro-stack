# Fork 修改说明 / Fork Changes

本仓库 fork 自 **[Yoahoug/kiro-stack](https://github.com/Yoahoug/kiro-stack)**，在其基础上增加了**单账号独立代理**功能。

This repository is forked from **[Yoahoug/kiro-stack](https://github.com/Yoahoug/kiro-stack)**. It adds **per-account proxy configuration** on top of the upstream.

## 新增功能 / What's New

为 `kiro-go` 里的每个账号单独设置 HTTP / HTTPS / SOCKS5 代理：

- 在 Admin Web UI 的账号详情弹窗里，「机器码」下方新增「账号代理」一栏
- 支持 `http://host:port`、`https://host:port`、`socks5://host:port`（以及 `socks5h://`）
- 留空 = 该账号走直连（默认行为保持不变）
- 代理作用于该账号的所有出站 HTTP：
  - `POST https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken`（Social token 刷新）
  - `POST https://oidc.<region>.amazonaws.com/token`（IdC token 刷新）
  - Kiro REST API：`getUsageLimits` / `GetUserInfo` / `ListAvailableModels`
  - Kiro 流式 API：`codewhisperer.us-east-1.amazonaws.com` 和 `q.us-east-1.amazonaws.com` 的 `generateAssistantResponse`

Per-account HTTP / HTTPS / SOCKS5 proxy for every account in `kiro-go`:

- In the Admin Web UI account-detail modal, a new "Account Proxy" section appears below "Machine ID".
- Accepts `http://host:port`, `https://host:port`, `socks5://host:port` (and `socks5h://`).
- Leave it empty for direct connection (default — behavior unchanged).
- The proxy is applied to every outbound request issued on behalf of that account (token refresh, usage/user-info/models REST calls, and the streaming Kiro API).

## 修改的文件 / Files Changed

全部改动集中在 `kiro-go/`：

| 文件 | 改动 |
|------|------|
| `kiro-go/config/config.go` | `Account` 结构体新增 `ProxyURL string` (JSON 字段 `proxyUrl`) |
| `kiro-go/config/httpclient.go` | **新增**：按代理 URL 缓存的 `*http.Client` 工厂；支持 `http` / `https` / `socks5` |
| `kiro-go/auth/http_client.go` | 新增 `pickClient(proxyURL)` 小工具；无代理时仍走原全局客户端 |
| `kiro-go/auth/oidc.go` | `RefreshToken` / `refreshSocialToken` / `refreshOIDCToken` 接收并使用 `account.ProxyURL` |
| `kiro-go/proxy/kiro_api.go` | `GetUsageLimits` / `GetUserInfo` / `ListAvailableModels` 改用 `config.GetAccountHTTPClient(account.ProxyURL)` |
| `kiro-go/proxy/kiro.go` | 新增 `pickKiroStreamClient(proxyURL)`；流式调用按账号选择客户端 |
| `kiro-go/proxy/handler.go` | `apiUpdateAccount` 接收 `proxyUrl` 字段（带 URL schema 校验）；`apiGetAccounts` 返回 `proxyUrl` |
| `kiro-go/web/index.html` | 账号详情弹窗新增代理输入 / 保存 / 直连按钮，中英文 i18n |

All changes are confined to `kiro-go/`:

| File | Change |
|------|--------|
| `kiro-go/config/config.go` | Added `ProxyURL string` (JSON `proxyUrl`) to `Account` struct |
| `kiro-go/config/httpclient.go` | **New**: cached `*http.Client` factory keyed by proxy URL; supports `http` / `https` / `socks5` |
| `kiro-go/auth/http_client.go` | Added `pickClient(proxyURL)` helper; falls back to the global client when empty |
| `kiro-go/auth/oidc.go` | `RefreshToken` / `refreshSocialToken` / `refreshOIDCToken` now use `account.ProxyURL` |
| `kiro-go/proxy/kiro_api.go` | `GetUsageLimits` / `GetUserInfo` / `ListAvailableModels` route through `config.GetAccountHTTPClient(account.ProxyURL)` |
| `kiro-go/proxy/kiro.go` | Added `pickKiroStreamClient(proxyURL)`; streaming call picks client per account |
| `kiro-go/proxy/handler.go` | `apiUpdateAccount` accepts `proxyUrl` (with URL-scheme validation); `apiGetAccounts` returns `proxyUrl` |
| `kiro-go/web/index.html` | New proxy input / save / direct buttons in the account-detail modal, with zh/en i18n |

## 兼容性 / Compatibility

- 完全向后兼容：旧的 `data/config.json` 里没有 `proxyUrl` 字段时，反序列化后默认为空，等价于直连。
- 不需要任何新的环境变量。
- 如果原先通过 `HTTPS_PROXY` / `HTTP_PROXY` 环境变量给整个容器设代理 —— 当账号自己没填 `proxyUrl` 时，仍会沿用 `http.ProxyFromEnvironment` 的行为。

Fully backward-compatible: missing `proxyUrl` in existing `data/config.json` deserializes as empty string = direct. No new env vars required. If you previously used `HTTPS_PROXY`/`HTTP_PROXY` container-wide, that still works when `proxyUrl` is empty (we fall back to `http.ProxyFromEnvironment`).

## License

本 fork 继承上游的 license。所有新增和修改都属于对上游项目的增强。
This fork inherits the upstream license. New / modified code is an enhancement of the upstream project.
