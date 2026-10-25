package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestClientIPIgnoresForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	// 客户端可以伪造这些头；限流不能因此分桶。
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "5.6.7.8")
	if got := clientIP(r); got != "127.0.0.1" {
		t.Fatalf("clientIP = %q, want 127.0.0.1", got)
	}

	// 无端口的 RemoteAddr（理论上不该出现）原样返回。
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1"
	if got := clientIP(r); got != "10.0.0.1" {
		t.Fatalf("clientIP = %q", got)
	}
}

func TestLimiterCleanupExpiresIdleBuckets(t *testing.T) {
	l := NewLimiter(rate.Limit(10), 10)
	defer l.Close()

	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("首次请求应放行")
	}
	if l.size() != 1 {
		t.Fatalf("桶数量 = %d, want 1", l.size())
	}
	// 未过期时保留。
	l.cleanup(now.Add(l.ttl / 2))
	if l.size() != 1 {
		t.Fatal("未过期就被清理")
	}
	// 过期后删除，IP 表不会无限增长。
	l.cleanup(now.Add(l.ttl + time.Second))
	if l.size() != 0 {
		t.Fatalf("过期桶未清理: %d", l.size())
	}
	// 清理后同一 IP 重新获得完整突发额度。
	for i := 0; i < 10; i++ {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("清理后第 %d 次应放行", i+1)
		}
	}
}

func TestLimiterTTLLongerThanRefill(t *testing.T) {
	// 清理阈值必须大于桶回满时间，否则被限流的 IP 会提前恢复。
	for _, tc := range []struct {
		perSecond rate.Limit
		burst     int
	}{
		{PublicPerSecond, PublicBurst},
		{SearchPerSecond, SearchBurst},
		{rate.Limit(1), 100},
	} {
		ttl := ttlFor(tc.perSecond, tc.burst)
		refill := time.Duration(float64(tc.burst) / float64(tc.perSecond) * float64(time.Second))
		if ttl < refill {
			t.Fatalf("ttl %v < 回满时间 %v（%v/%d）", ttl, refill, tc.perSecond, tc.burst)
		}
	}
}

func TestBearerTokenParsing(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER  abc ", "abc", true},
		{"Bearer ", "", false},
		{"Basic abc", "", false},
		{"abc", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}
		got, ok := bearerToken(r)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("%q → (%q,%v), want (%q,%v)", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}
