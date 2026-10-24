package server

import "net/http"

// NewRouter 构造完整的 HTTP 路由。
//
// 分组约定（见 docs/API.md）：
//   - /healthz                     健康检查，不暴露到 Caddy 公开路由
//   - /api/*                       私有接口，Bearer Token 鉴权
//   - /api/public/*                公开只读接口，查询层强制 visibility='public'
//   - /api/llm/chat                二期 RAG，暂不注册
//
// 阶段 3 在此挂载 middleware 与 handler。
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()
	return mux
}
