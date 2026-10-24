# Knot

自托管的个人笔记系统。Go 全栈，PostgreSQL 唯一真值源，本地 SQLite 两级缓存，TUI 为主力入口，Vue 只读 Web 端。

> 状态：MVP 开发中，接口与表结构可能调整。

## 特性

- **单写入口**：所有写操作经 CLI/TUI 通过 HTTP 提交后端，PG 是唯一数据源
- **无离线写作**：放弃双向同步与版本冲突处理，本地缓存只加速浏览与检索，可整体清空重建
- **TUI 优先**：浏览、检索、编辑、标签、同步集中在 `knot tui`；CLI 作脚本与 nvim 胶水
- **只读 Web**：Vue 端仅展示公开笔记，无写入接口
- **公开/私密隔离**：`visibility` 字段控制，公开接口在查询层强制过滤，不靠前端隐藏
- **并发保护**：`version` 乐观锁，提交带 `base_version`，冲突返回 409，不静默覆盖
- **限流**：公开 API 按 IP 令牌桶限流，搜索接口更严
- **全 Go**：CLI/TUI 与后端均用 Go，共享模型与 DTO，单语言工具链，交叉编译简单

## 快速开始

前置：Go >= 1.23、Node.js >= 20（仅 Web）、PostgreSQL 16（含 `pg_trgm`）。

```bash
# 安装 Go 依赖与构建工具
make setup

# 构建
make build

# 准备 PG：建库、CREATE EXTENSION pg_trgm、跑迁移
# 详见 docs/DEPLOYMENT.md

# 启动后端
make run-server

# 另开终端，启动 TUI
make run
```

配置放在 `~/.config/knot/config.toml`，字段见 [CLIENT](docs/CLIENT.md)。

## 开发

前后端分开跑，Vite 代理 `/api` 到本地 Go：

```bash
make run-server   # 后端 127.0.0.1:8080
make web-dev      # Vite 开发服务器
```

依赖清单、Makefile 目标、测试约定见 [DEVELOPMENT](docs/DEVELOPMENT.md)。

Neovim 插件为轻量胶水，只调用 `knot` CLI，不实现业务逻辑。

## 部署

Caddy 托管 `web/dist` 并反代 `/api` 到 `127.0.0.1:8080`，Go 不直接暴露公网。

```bash
make dist              # amd64
make dist GOARCH=arm64 # arm64
```

产物 `bin/knot-deploy.tar.gz`。完整步骤见 [DEPLOYMENT](docs/DEPLOYMENT.md)。

## 文档

| 文档 | 内容 |
|---|---|
| [ARCHITECTURE](docs/ARCHITECTURE.md) | 架构总览、模块职责、目录结构、依赖方向 |
| [DATA_MODEL](docs/DATA_MODEL.md) | PG / SQLite 表结构、索引、迁移策略 |
| [API](docs/API.md) | 私有/公开接口、鉴权、错误码、限流 |
| [CLIENT](docs/CLIENT.md) | CLI 子命令、TUI、编辑流程、配置文件 |
| [SYNC_CACHE](docs/SYNC_CACHE.md) | 两级缓存、懒加载、全量对账、删除处理 |
| [WEB](docs/WEB.md) | Vue 只读站：路由、页面线框、视觉风格、字体、Markdown 安全 |
| [DEPLOYMENT](docs/DEPLOYMENT.md) | 构建、Caddy、systemd、配置、备份、回滚 |
| [SECURITY](docs/SECURITY.md) | 鉴权、公开/私密隔离、限流、部署安全基线 |
| [DEVELOPMENT](docs/DEVELOPMENT.md) | 依赖清单、Makefile、脚本、测试约定 |
| [ROADMAP](docs/ROADMAP.md) | 分阶段计划、未决事项 |

## 许可

本项目采用 [MIT License](LICENSE)。
