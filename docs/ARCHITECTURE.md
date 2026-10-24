# 架构

## 总览

```text
┌─────────────────────────────┐
│  本地端                      │
│  Neovim 插件 → knot CLI/TUI  │
│                    ↓         │
│              SQLite 两级缓存 │
└────────────────────┼────────┘
                     │ HTTP
┌────────────────────▼────────┐
│  服务端                      │
│  knot-server → PostgreSQL    │
│              → Vue 只读 Web  │
└─────────────────────────────┘
```

## 核心决策

| 决策项 | 选择 | 依据 |
|---|---|---|
| 数据真值源 | 服务端 PostgreSQL | 无离线写作；支持 pg_trgm / pgvector，备份成熟 |
| 本地存储 | SQLite 两级缓存 | 仅元索引 + 正文懒缓存，可重建 |
| 技术栈 | 全 Go + Vue 只读 Web | 共享模型/DTO，交叉编译简单 |
| 同步模型 | 单向拉取，无双向冲突 | 本地不回写真值 |
| 主力入口 | knot TUI | CLI 作脚本与 nvim 胶水 |
| 标签来源 | 统一存 PG | 不写入 md 文件，单一数据源 |

## 模块职责

| 模块 | 归属 | 职责 |
|---|---|---|
| Neovim 插件 | 本地端 | 轻量胶水，调用 knot CLI，不实现业务逻辑 |
| Knot CLI / TUI | 本地端 | 主力交互；所有写操作走 HTTP 提交后端 |
| 本地 SQLite 缓存 | 本地端 | 常驻元索引 + 正文懒缓存 |
| Knot 后端服务 | 服务端 | 唯一写入口，对 Web 暴露只读 API |
| PostgreSQL + Vue Web | 服务端 | PG 存权威数据；Vue 纯只读展示 |

## 目录结构

```text
knot/
├── cmd/
│   ├── knot/                 # CLI/TUI 入口
│   └── knot-server/          # 后端入口
├── internal/
│   ├── model/                # 共享领域模型
│   ├── dto/                  # API 契约
│   ├── config/               # 配置加载
│   ├── client/               # 调后端的 HTTP client
│   ├── store/
│   │   ├── pg/               # 服务端 PG 存储
│   │   └── sqlite/           # 本地缓存
│   ├── server/               # HTTP 路由、中间件、handler
│   ├── cli/                  # Cobra 命令
│   ├── tui/                  # Bubble Tea 界面
│   └── reconcile/            # 全量快照对账
├── migrations/
│   ├── pg/
│   └── sqlite/
├── web/                      # Vue 独立工程
├── scripts/                  # dev-setup / web-build
├── docs/
├── go.mod
└── Makefile
```

## 依赖方向

```text
model ← dto ← client / store / reconcile ← cli / tui / server ← cmd
```

约束：

- `model`、`dto` 只依赖标准库和 `google/uuid`，不依赖 pgx、bubbletea、cobra。
- `server` 只依赖 `store/pg`，不依赖 `store/sqlite`。
- `cli`、`tui` 只依赖 `store/sqlite` 和 `client`，不依赖 `store/pg`。
- `reconcile` 只依赖 `store/sqlite` 和 `dto`，纯函数，便于单测。

## 关键取舍

- **不设 service 层**：业务逻辑薄，放 `store` 方法里，handler 直接调。
- **model 与 dto 分离**：dto 控制暴露字段，公开接口不返回内部字段。
- **sqlc 生成代码放 `store/pg/sqlcgen/`**：不手改，`store.go` 包一层，handler 只认 store 接口。
