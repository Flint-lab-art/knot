// Package middleware 提供 HTTP 中间件：Bearer 鉴权、按 IP 令牌桶限流。
//
// 限流规则与错误码见 docs/API.md；鉴权要求见 docs/SECURITY.md。
package middleware

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// WWWAuthenticate 是 401 响应的认证挑战头。
const wwwAuthenticate = `Bearer realm="knot"`

// Unauthorized 写出 401：统一 {"code","msg"} 结构，不透露 token 是否配置。
func Unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", wwwAuthenticate)
	writeError(w, http.StatusUnauthorized, "invalid or missing token")
}

// writeError 与 handler 的约定保持一致（见 docs/API.md 的错误码表）。
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	// 错误体极小，编码失败没有可补救的余地（头已写出）。
	_ = json.NewEncoder(w).Encode(struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}{Code: code, Msg: msg})
}

// Token 为 nil 或 token 为空时视为「未配置 token」，拒绝一切私有请求。
//
// 比较用 crypto/subtle，避免时序侧信道。缺失与错误都返回 401，不区分原因。
func Token(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := bearerToken(r)
			if !ok || token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				Unauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken 从 Authorization 头取 Bearer token；大小写不敏感地匹配 scheme。
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	return tok, tok != ""
}
