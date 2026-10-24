// Package client 封装对 knot-server 的 HTTP 调用（Bearer 鉴权、错误码映射、429 重试提示）。
//
// 约束（见 docs/ARCHITECTURE.md）：只依赖 dto，不依赖 store/pg。
// 接口映射见 docs/API.md 的「CLI-API 映射」。
package client

import (
	"errors"
	"fmt"
)

// ErrNotImplemented 表示 HTTP client 尚未实现（阶段 4）。
var ErrNotImplemented = errors.New("client 尚未实现")

// APIError 是服务端返回的业务错误，对应 docs/API.md 的 {"code":..,"msg":..}。
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("服务端返回错误 %d", e.Code)
	}
	return fmt.Sprintf("%d: %s", e.Code, e.Msg)
}
