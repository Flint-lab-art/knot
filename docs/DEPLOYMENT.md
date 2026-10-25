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

## 启动服务

`knot-server` 是单个静态二进制，没有外部依赖（除 PG）。配置可以从**文件**或**环境变量**来，
环境变量优先级更高。

### 配置项

| 项 | 配置文件字段 | 环境变量 | 说明 |
|---|---|---|---|
| 监听地址 | `listen` | `KNOT_LISTEN` | 默认 `127.0.0.1:8080`，只监听本机 |
| PG 连接 | `pg_dsn` | `KNOT_PG_DSN` | **必填**，缺失直接退出 |
| 鉴权 token | `token` | `KNOT_TOKEN` | 缺失时服务照常起，但私有接口全 401 |

### 方式一：本地调试（最常用）

```sh
# 1) 准备库（只需一次）
psql -U postgres -d postgres -c 'CREATE DATABASE knot_dev'
psql -U postgres -d knot_dev -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm'

# 2) 跑迁移（每次升级后也要跑）
KNOT_PG_DSN='postgres://postgres@/knot_dev?host=/run/postgresql&sslmode=disable' \
  ./bin/knot-server-linux-amd64 migrate

# 3) 前台启动（Ctrl+C 停止）
KNOT_PG_DSN='postgres://postgres@/knot_dev?host=/run/postgresql&sslmode=disable' \
KNOT_TOKEN='用 openssl rand -hex 32 生成的串' \
KNOT_LISTEN=127.0.0.1:8080 \
  ./bin/knot-server-linux-amd64
```

或者用 `go run`（不用先编译）：

```sh
make run-server
```

> `make run-server` 不传 `--config`，所以只读**环境变量**。
> 要先 `export KNOT_PG_DSN=...` 和 `KNOT_TOKEN=...`，否则会报「未配置 pg_dsn」。

### 方式二：用配置文件（接近生产）

```toml
# /path/to/config.toml，权限 600
listen = "127.0.0.1:8080"
pg_dsn = "postgres://knot:密码@127.0.0.1:5432/knot?sslmode=disable"
token = "openssl rand -hex 32 的输出"
```

```sh
./bin/knot-server-linux-amd64 --config /path/to/config.toml
```

**参数必须写双横线 `--config`**。写成单横线 `-config` 会被 cobra 解析成
`-c onfig`，把 "onfig" 当成子命令，报 `unknown command`。

### 方式三：生产 systemd

```sh
sudo cp deploy/knot-server.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now knot-server     # 开机自启 + 立即启动
```

日常操作：

```sh
sudo systemctl status knot-server      # 看状态
sudo systemctl restart knot-server     # 重启（改配置后）
sudo systemctl stop knot-server        # 停止
sudo journalctl -u knot-server -f      # 跟日志
```

启动失败时先看 `journalctl -u knot-server -n 50 --no-pager`。常见原因：

| 现象 | 原因 |
|---|---|
| `status=226/NAMESPACE` | `ReadWritePaths=/opt/knot/data` 目录不存在（`deploy.sh` 现在会建，手工部署要自己 `mkdir -p`）|
| `未配置 pg_dsn` | `config.toml` 路径不对或字段名写错 |
| `bind: address already in use` | 端口被占，`ss -ltnp \| grep 8080` 查 |
| healthz 503 | PG 连不上，检查 DSN 与 PG 是否在跑 |

### 启动顺序（重要）

```text
建库 + 装 pg_trgm  →  迁移  →  启动服务  →  配 Caddy
```

**迁移必须早于启动服务**。老版本二进制遇到新 schema 可能出错，所以不要把迁移放进服务启动流程。

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

## systemd 安装细节

启动方式见上面的「[方式三：生产 systemd](#方式三生产-systemd)」，这里只列首次安装的完整步骤。

仓库里是完整 unit：`deploy/knot-server.service`（含 `ProtectSystem`、`MemoryDenyWriteExecute`
等加固项）。

```bash
sudo cp deploy/knot-server.service /etc/systemd/system/
sudo useradd --system --home /opt/knot --shell /usr/sbin/nologin knot
sudo mkdir -p /opt/knot/data /opt/knot/backup    # ReadWritePaths 要求目录已存在
sudo chown -R knot:knot /opt/knot
sudo systemctl daemon-reload
sudo systemctl enable --now knot-server
```

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
