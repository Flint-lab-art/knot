package server

import (
	"log/slog"
	"net/http"
)

// recoverer 兜底 panic，返回统一 500，不让进程挂掉也不泄露堆栈。
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				// http.ErrAbortHandler 是显式中止，按约定直接透传。
				if v == http.ErrAbortHandler {
					panic(v)
				}
				slog.Error("panic recovered", "method", r.Method, "path", r.URL.Path, "panic", v)
				writeJSON(w, http.StatusInternalServerError, errorBody(http.StatusInternalServerError, "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestID 生成/透传 X-Request-Id，方便日志与排障对号入座。
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}
