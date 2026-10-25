package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"golang.org/x/time/rate"

	"knot/internal/server/handler"
	"knot/internal/server/middleware"
)

// Deps 是构造路由所需的依赖。
type Deps struct {
	// Store 是 PG 存储；为 nil 时只注册 healthz（测试与无 PG 启动用）。
	Store handler.Store
	// Token 是私有接口的 Bearer Token。为空表示未配置，所有私有请求都 401。
	Token string
	// Health 用于 healthz；为 nil 时只报告进程存活。
	Health HealthChecker
	// 限流参数；零值用默认值（见 middleware 包常量）。
	PublicPerSecond rate.Limit
	PublicBurst     int
	SearchPerSecond rate.Limit
	SearchBurst     int
}

// NewRouter 构造完整的 HTTP 路由。
//
// 分组约定（见 docs/API.md）：
//   - /healthz                     健康检查，不暴露到 Caddy 公开路由
//   - /api/*                       私有接口，Bearer Token 鉴权
//   - /api/public/*                公开只读接口，查询层强制 visibility='public'
//   - /api/llm/chat                二期 RAG，暂不注册
//
// 返回的 *chi.Mux 内部持有两个限流器的清理协程，进程退出即可，
// 无需显式关闭（服务端生命周期与进程一致）。
func NewRouter(d Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(recoverer, requestID)

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, errorBody(http.StatusNotFound, "not found"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody(http.StatusMethodNotAllowed, "method not allowed"))
	})

	r.Get("/healthz", healthHandler(d.Health))

	// 没有 store 就没有 /api/*（例如只跑 healthz 的诊断模式）。
	if d.Store == nil {
		return r
	}

	api := handler.New(d.Store)
	publicLimiter := middleware.NewLimiter(orDefault(d.PublicPerSecond, middleware.PublicPerSecond),
		orDefaultInt(d.PublicBurst, middleware.PublicBurst))
	searchLimiter := middleware.NewLimiter(orDefault(d.SearchPerSecond, middleware.SearchPerSecond),
		orDefaultInt(d.SearchBurst, middleware.SearchBurst))

	r.Route("/api", func(r chi.Router) {
		// 公开接口：无 token，按 IP 限流。
		r.Route("/public", func(r chi.Router) {
			// 更贵的列表/检索单独一档，先匹配，避免被通用档先行拦截。
			r.With(searchLimiter.Wrap).Get("/notes", api.ListPublicNotes)
			r.With(publicLimiter.Wrap).Get("/notes/{id}", api.GetPublicNote)
			r.With(publicLimiter.Wrap).Get("/tags", api.ListPublicTags)
		})

		// 私有接口：Bearer 鉴权。搜索也走 token，但同样限流以防单 token 刷爆。
		r.Group(func(r chi.Router) {
			r.Use(middleware.Token(d.Token))
			r.Post("/notes", api.CreateNote)
			r.Get("/notes", api.ListNotes)
			r.Get("/notes/{id}", api.GetNote)
			r.Put("/notes/{id}", api.UpdateNote)
			r.Delete("/notes/{id}", api.DeleteNote)
			r.Post("/notes/{id}/tags", api.BindTag)
			r.Delete("/notes/{id}/tags/{tag_name}", api.UnbindTag)
			r.Get("/tags", api.ListTags)
			r.Put("/tags/{old_name}", api.RenameTag)
			r.Get("/sync/snapshot", api.Snapshot)
			r.With(searchLimiter.Wrap).Get("/search", api.SearchNotes)
		})
	})

	return r
}

func orDefault(v rate.Limit, def rate.Limit) rate.Limit {
	if v <= 0 {
		return def
	}
	return v
}

func orDefaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
