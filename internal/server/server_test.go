package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"knot/internal/dto"
)

type stubHealth struct{ err error }

func (s stubHealth) Ping(context.Context) error { return s.err }

func TestHealthz(t *testing.T) {
	tests := []struct {
		name       string
		health     HealthChecker
		wantStatus int
	}{
		{name: "无 PG 检查时进程存活即 ok", health: nil, wantStatus: http.StatusOK},
		{name: "PG 可用", health: stubHealth{}, wantStatus: http.StatusOK},
		{name: "PG 不可用", health: stubHealth{err: errors.New("dial tcp: connection refused")}, wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

			New(Config{Addr: "127.0.0.1:8080"}, tt.health).Handler().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Fatalf("content-type = %q", ct)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是合法 JSON: %v", err)
			}
			if tt.wantStatus == http.StatusOK {
				if body["status"] != "ok" {
					t.Fatalf("body = %v", body)
				}
				return
			}
			// 失败时返回统一错误体 {"code":..,"msg":..}
			if int(body["code"].(float64)) != http.StatusServiceUnavailable {
				t.Fatalf("error code = %v", body["code"])
			}
			var apiErr dto.Error
			if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
				t.Fatalf("错误体无法解析为 dto.Error: %v", err)
			}
			if apiErr.Msg == "" {
				t.Fatal("错误体缺少 msg")
			}
		})
	}
}
