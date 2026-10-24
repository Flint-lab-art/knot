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
)

// Config 是 HTTP 服务配置。
type Config struct {
	// Addr 是监听地址，如 127.0.0.1:8080。
	Addr string
}

// HealthChecker 报告服务端依赖（PG）是否可用；healthz 只查进程与 PG 连接。
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// Server 是 knot-server 的 HTTP 服务。
type Server struct {
	addr   string
	health HealthChecker
}

// New 构造 Server。health 为 nil 时 healthz 仅报告进程存活。
func New(cfg Config, health HealthChecker) *Server {
	return &Server{addr: cfg.Addr, health: health}
}

// Handler 返回路由：目前仅 /healthz，阶段 3 再挂 /api/*。
func (s *Server) Handler() http.Handler {
	mux := NewRouter()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	return mux
}

// handleHealth 只检查进程与 PG 连接，不返回版本 / 配置（见 docs/DEPLOYMENT.md）。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.health != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.health.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, dto.Error{Code: http.StatusServiceUnavailable, Msg: "pg unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
