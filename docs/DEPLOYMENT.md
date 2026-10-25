# 部署

## 架构

```text
公网 80/443
   │
 Caddy（自动 HTTPS、静态托管、反代、压缩、安全头）
   ├── /            → /opt/knot/web/（Vue dist，SPA fallback）
   └── /api/*       → 127.0.0.1:8080（knot-server）
                        │
                    PostgreSQL（本机/内网，不暴露公网）
```

Go 只监听 `127.0.0.1:8080`，Caddy 是唯一入口。

## 服务器目录

```text
/opt/knot/
├── knot-server          # 当前二进制
├── knot-server.prev     # 上一版，回滚用
├── config.toml          # 权限 600
└── web/                 # Vue dist

/etc/caddy/Caddyfile
/var/log/caddy/knot.log
```

## 构建部署包

```bash
make dist              # 默认 amd64
make dist GOARCH=arm64 # arm64 服务器
```

产物 `bin/knot-deploy.tar.gz`，解开为 `dist/knot-server + dist/web/ + dist/config.toml`。

## Caddyfile

仓库里是可直接用的模板：`deploy/Caddyfile`

```bash
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile
sudo sed -i 's/knot.example.com/你的域名/' /etc/caddy/Caddyfile
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

要点：

- `handle /api/*` 必须在静态 `handle` 之前，否则会被 SPA fallback 吞掉；
- SPA fallback 靠 `try_files {path} /index.html`；
- `/healthz` 通过 `respond … 404` 对外隐藏（只给本机探活）；
- `/assets/*` 加 `immutable` 长缓存，`index.html` 不缓存，发版即生效；
- 安全头见文件顶部注释；改动需同步 `docs/SECURITY.md`。

## systemd

仓库里是完整 unit：`deploy/knot-server.service`（含 `ProtectSystem`、`MemoryDenyWriteExecute`
等加固项）。

```bash
sudo cp deploy/knot-server.service /etc/systemd/system/
sudo useradd --system --home /opt/knot --shell /usr/sbin/nologin knot
sudo mkdir -p /opt/knot/data /opt/knot/backup
sudo chown -R knot:knot /opt/knot
sudo systemctl daemon-reload
sudo systemctl enable --now knot-server
```

**参数必须用双横线**：`--config`。写成单横线 `-config` 会被 cobra 解析成 `-c onfig`
（把 "onfig" 当成子命令）。

## 首次部署

```text
1. 本地 make dist
2. 上传 tar 包到服务器
3. 建用户、建目录
4. 写 config.toml（PG DSN、token、listen=127.0.0.1:8080），chmod 600
5. 初始化 PG：建库、CREATE EXTENSION pg_trgm
6. scripts/deploy.sh 安装二进制 + 跑迁移 + 启动 + healthz 冒烟
7. Caddy 配好、reload，验证 HTTPS
8. 本地 knot sync，确认能拉到数据
9. 配 pg_dump 定时备份（scripts/backup.sh）
```

**迁移必须在启动服务之前完成。** 用二进制自带子命令（`deploy.sh` 会自动执行）：

```bash
./knot-server migrate --config /opt/knot/config.toml
```

迁移文件 `//go:embed`（仓库根 `migrations/`，`knot-server` 只打包 `pg/`），服务器不需要装 goose。

`pg_trgm` 扩展需先由管理员安装一次，否则 `0002_trgm_index.sql` 会失败（`0001` 已成功，不阻断重试）：

```bash
sudo -u postgres psql -d knot -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm'
```

本地开发一条命令搞定建库、装扩展、跑迁移：`make db-setup`（幂等，可重复执行）。

## 后续部署（一条命令）

```bash
sudo scripts/deploy.sh /tmp/knot-deploy.tar.gz
sudo scripts/deploy.sh /tmp/knot-deploy.tar.gz --skip-migrate   # 无新迁移时
# 或用 Makefile：
make deploy DEPLOY_PKG=/tmp/knot-deploy.tar.gz
```

`scripts/deploy.sh` 的步骤与失败处理：

1. 解包并校验 `dist/knot-server` 可执行、`dist/web/index.html` 存在（坏包直接退出，不碰现有安装）；
2. 备份当前二进制到 `knot-server.prev`；
3. 安装二进制与 `web/`（有 rsync 用 `rsync --delete`，否则整目录替换）；
4. 跑迁移；失败 **自动回滚二进制并重启**；
5. 重启服务后最多等 20 秒做 `healthz` 冒烟，失败同样回滚。

环境变量：`APP_DIR`（默认 `/opt/knot`）、`SERVICE`（默认 `knot-server`）、
`HEALTH_URL`（默认 `http://127.0.0.1:8080/healthz`）。
`SKIP_ROOT_CHECK=1` 只用于本地联调，生产不要设。

## 回滚

自动回滚已覆盖迁移/启动失败；手动回滚：

```bash
sudo mv /opt/knot/knot-server.prev /opt/knot/knot-server
sudo systemctl restart knot-server
```

迁移只加不删，回滚安全；删字段/改类型前先确认（`0003` 删列属于例外，
它有对应的 down 补回列定义，但**数据不可恢复**）。

## 备份

```bash
PG_DSN='postgres://knot@/knot?host=/run/postgresql' \
  BACKUP_DIR=/opt/knot/backup KEEP=14 scripts/backup.sh
```

- 先写 `.partial` 临时文件、`pg_restore -l` 校验归档可读，再 `mv` 成正式备份 ——
  中途失败不会留下"看起来完整"的半截文件；
- 按修改时间只保留最新 `KEEP` 份（默认 7），只删脚本产出的 `knot-*.dump`；
- 部署前必做一次备份；定时任务每天一次；
- 异地推送策略待定。

恢复演练（每季度至少一次，不要等出事才验）：

```bash
createdb knot_restore
psql -d knot_restore -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm'
pg_restore --no-owner --no-privileges -d knot_restore backup/knot-YYYY-MM-DD-HHMMSS.dump
psql -d knot_restore -c 'select count(*) from notes'
```

## 配置权限

```bash
chmod 600 /opt/knot/config.toml
chown knot:knot /opt/knot/config.toml
```

不要把 token 写进 systemd `ExecStart` 参数。

token 为空时服务**照常启动**（公开站点可用），但所有私有 `/api/*` 请求一律 401，
启动日志里会有一条 warn。没有内置默认 token：默认值等于零鉴权。

## 健康检查

`GET /healthz` 返回 200，只检查进程与 PG 连接，不返回版本/配置。不暴露到 Caddy 公开路由。

## 日志

- 服务端 `log/slog` JSON 到 stdout，`journalctl -u knot-server`；
- 访问日志由 Caddy 写文件；
- 不自建日志文件轮转。
