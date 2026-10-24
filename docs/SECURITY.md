# 安全

目标：公开端默认拒绝、只读最小权限、输出转义、限流与隔离；私密数据不进入公开查询链路。

## 鉴权

- 私有接口：`Authorization: Bearer <token>`，服务端用 `crypto/subtle.ConstantTimeCompare` 比对。
- MVP 单 token，不轮换；多客户端各自签发留二期。
- 公开接口：无 token，仅 `visibility='public'`。
- token 存 `~/.config/knot/config.toml`，权限 600；支持 `KNOT_TOKEN` 环境变量覆盖。

## 公开/私密隔离

- `notes.visibility` 字段，默认 `private`；MVP 枚举 `private / public`，`unlisted` 保留二期。
- 公开 API **在查询层强制** `WHERE visibility='public'`，不靠前端隐藏。
- 公开接口使用**只读 PG 角色**：`REVOKE INSERT/UPDATE/DELETE`，只 `SELECT`。
- 非 `public` 笔记的 `GET /api/public/notes/:id` 返回 404，不泄露存在性。
- 公开接口不返回 `version` 等内部字段。

## 应用层

- 所有 SQL 参数化，禁止拼接。
- Markdown 渲染禁用 raw HTML 或严格 sanitize（`DOMPurify`）；前端加 CSP。
- 错误统一返回 `{"code","msg"}`，不泄露堆栈/SQL。
- 日志脱敏，不记录 token、正文全文。
- 请求体大小限制、超时、并发限制。
- CORS 白名单，不用 `*` 配凭据。

## 限流（MVP）

Go 中间件按 IP 令牌桶，`golang.org/x/time/rate`：

| 接口 | 速率 | 突发 |
|---|---|---|
| `/api/public/*` | 10 req/s | 20 |
| `/api/public/notes`、`/api/search` | 2 req/s | 5 |
| 私有接口 | 有 token 保护，不限 | — |

- 超限返回 `429` + `Retry-After`。
- limiter 按 IP 维护，带过期清理，避免表无限增长。
- Caddy 标准版不限流，限流在 Go 层做。

## 部署层

- 全站 TLS，Caddy 自动 HTTPS；反代到 `127.0.0.1:8080`。
- Go 只监听 `127.0.0.1`，不暴露公网。
- Caddy 加安全头：CSP、`X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`，去掉 `Server` 头。
- 数据库不暴露公网。
- systemd：`NoNewPrivileges`、`ProtectSystem=strict`、`ProtectHome`、`PrivateTmp`、独立低权用户。
- 备份加密，定期恢复演练。
- 依赖锁定 + 定期扫描。

## SSH 基线

国内公网服务器从开机起就被无差别扫描，以下为最低成本基线：

1. SSH 密钥登录，禁用密码认证。
2. SSH 端口改到 20000 以上高位端口。
3. 云安全组只放行 80/443；22 不对 `0.0.0.0/0` 开放，限定自己的 IP 或用 Tailscale/WireGuard。
4. 装 `Fail2ban`，自动封禁反复失败 IP。

## 未决

- token 轮换与多客户端签发（二期）。
- 私密笔记在公开实例的存储策略（MVP 允许存，默认 `private`，公开端不查）。
- 审计日志。
