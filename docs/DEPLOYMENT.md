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

```caddyfile
{
    email you@example.com
}

knot.example.com {
    encode zstd gzip

    handle /api/* {
        reverse_proxy 127.0.0.1:8080
    }

    handle {
        root * /opt/knot/web
        try_files {path} /index.html
        file_server
    }

    header {
        X-Content-Type-Options nosniff
        X-Frame-Options DENY
        Referrer-Policy strict-origin-when-cross-origin
        Permissions-Policy "geolocation=(), microphone=(), camera=()"
        Content-Security-Policy "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self' data:; frame-ancestors 'none'"
        -Server
    }

    log {
        output file /var/log/caddy/knot.log {
            roll_size 10MiB
            roll_keep 5
        }
        format json
    }
}
```

`handle /api/*` 在静态 `handle` 之前。SPA fallback 靠 `try_files`。

## systemd

`/etc/systemd/system/knot-server.service`：

```ini
[Unit]
Description=Knot Server
After=network.target

[Service]
Type=simple
User=knot
Group=knot
WorkingDirectory=/opt/knot
ExecStart=/opt/knot/knot-server -config /opt/knot/config.toml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/knot/data

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd --system --home /opt/knot --shell /usr/sbin/nologin knot
sudo mkdir -p /opt/knot/data
sudo chown -R knot:knot /opt/knot
sudo systemctl daemon-reload
sudo systemctl enable --now knot-server
```

## 首次部署

```text
1. 本地 make dist
2. 上传 tar 包到服务器
3. 建用户、建目录、解包
4. 写 config.toml（PG DSN、token、listen=127.0.0.1:8080），chmod 600
5. 初始化 PG：建库、CREATE EXTENSION pg_trgm、跑迁移
6. systemctl start knot-server
7. Caddy 配好、reload，验证 HTTPS
8. 本地 knot sync，确认能拉到数据
9. 配 pg_dump 定时备份
```

**迁移必须在启动服务之前完成。** 用二进制自带子命令：

```bash
./knot-server migrate -config /opt/knot/config.toml
```

迁移文件 `//go:embed`（仓库根 `migrations/`，`knot-server` 只打包 `pg/`），服务器不需要装 goose。

`pg_trgm` 扩展需先由管理员安装一次，否则 `0002_trgm_index.sql` 会失败（`0001` 已成功，不阻断重试）：

```bash
sudo -u postgres psql -d knot -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm'
```

本地开发一条命令搞定建库、装扩展、跑迁移：`make db-setup`（幂等，可重复执行）。

## 后续部署

```bash
tar -xzf /tmp/knot-deploy.tar.gz -C /tmp
sudo systemctl stop knot-server
sudo cp /opt/knot/knot-server /opt/knot/knot-server.prev
sudo install -m 755 /tmp/dist/knot-server /opt/knot/knot-server
sudo rsync -a --delete /tmp/dist/web/ /opt/knot/web/
# 如有新迁移
sudo -u knot /opt/knot/knot-server migrate -config /opt/knot/config.toml
sudo systemctl start knot-server
sudo systemctl status knot-server
```

## 回滚

```bash
sudo mv /opt/knot/knot-server.prev /opt/knot/knot-server
sudo systemctl restart knot-server
```

迁移只加不删，回滚安全；删字段/改类型前先确认。

## 备份

```bash
pg_dump -Fc "$PG_DSN" > /opt/knot/backup/knot-$(date +%F-%H%M).dump
```

- 部署前必做一次 `pre-deploy` 备份；
- 定时任务每天一次，保留 N 份；
- 异地推送策略待定。

## 配置权限

```bash
chmod 600 /opt/knot/config.toml
chown knot:knot /opt/knot/config.toml
```

不要把 token 写进 systemd `ExecStart` 参数。

## 健康检查

`GET /healthz` 返回 200，只检查进程与 PG 连接，不返回版本/配置。不暴露到 Caddy 公开路由。

## 日志

- 服务端 `log/slog` JSON 到 stdout，`journalctl -u knot-server`；
- 访问日志由 Caddy 写文件；
- 不自建日志文件轮转。
