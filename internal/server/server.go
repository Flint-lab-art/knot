// Package server 实现 knot-server 的 HTTP 路由、中间件与 handler。
//
// 约束（见 docs/ARCHITECTURE.md）：只依赖 store/pg；无 service 层，
// 业务逻辑写在 store 方法里，handler 直接调用。接口见 docs/API.md。
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"knot/internal/dto"
	"knot/internal/server/handler"
)

// Config 是 HTTP 服务配置。
type Config struct {
	// Addr 是监听地址，如 127.0.0.1:8080。
	Addr string
	// Store 是 PG 存储；为 nil 时只注册 healthz（诊断模式与测试用）。
	Store handler.Store
	// Token 是私有接口的 Bearer Token。为空表示未配置，所有私有请求都 401。
	Token string
}

// HealthChecker 报告服务端依赖（PG）是否可用；healthz 只查进程与 PG 连接。
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// Server 是 knot-server 的 HTTP 服务。
type Server struct {
	cfg    Config
	health HealthChecker
}

// New 构造 Server。health 为 nil 时 healthz 仅报告进程存活。
func New(cfg Config, health HealthChecker) *Server {
	return &Server{cfg: cfg, health: health}
}

// Handler 返回完整路由（healthz + /api/*）。
func (s *Server) Handler() http.Handler {
	return NewRouter(Deps{
		Store:  s.cfg.Store,
		Token:  s.cfg.Token,
		Health: s.health,
	})
}

// writeJSON 以 JSON 写出响应体。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// 响应头已写出，只能记日志。
		slog.Error("write json response", "err", err)
	}
}

// healthTimeout 是 healthz 检查 PG 的上限；超过按不可用处理。
const healthTimeout = 2 * time.Second

// healthHandler 只检查进程与 PG 连接，不返回版本 / 配置（见 docs/DEPLOYMENT.md）。
func healthHandler(h HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h != nil {
			ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
			defer cancel()
			if err := h.Ping(ctx); err != nil {
				slog.Warn("healthz: pg unavailable", "err", err)
				writeJSON(w, http.StatusServiceUnavailable, errorBody(http.StatusServiceUnavailable, "pg unavailable"))
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// errorBody 构造统一错误体；契约只有 dto.Error 一处定义。
func errorBody(code int, msg string) dto.Error {
	return dto.Error{Code: code, Msg: msg}
}
