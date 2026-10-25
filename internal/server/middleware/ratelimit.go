package middleware

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// 限流规则（docs/API.md / docs/SECURITY.md）。
const (
	// PublicPerSecond / PublicBurst：/api/public/* 普通接口。
	PublicPerSecond = 10
	PublicBurst     = 20
	// SearchPerSecond / SearchBurst：公开笔记列表与 /api/search，更贵。
	SearchPerSecond = 2
	SearchBurst     = 5
)

// limitEntry 是一个 IP 的桶及最后使用时间，用于过期清理。
type limitEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter 是按 IP 的令牌桶集合，实现 http.Handler 中间件。
//
// 桶在首次请求时创建；后台定时清理超过 TTL 未使用的桶，避免 IP 表
// 随扫描流量无限增长。TTL 只要大于「桶回满所需时间」，清理就不会
// 让被限流的 IP 提前恢复配额。
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*limitEntry
	limit   rate.Limit
	burst   int
	ttl     time.Duration
	now     func() time.Time // 测试注入

	stopOnce sync.Once
	stop     chan struct{}
}

// NewLimiter 创建一个按 perSecond/burst 限流的中间件，并启动清理协程。
// 调用方应持有返回的 *Limiter 以便 Close。
func NewLimiter(perSecond rate.Limit, burst int) *Limiter {
	l := &Limiter{
		entries: make(map[string]*limitEntry),
		limit:   perSecond,
		burst:   burst,
		ttl:     ttlFor(perSecond, burst),
		now:     time.Now,
		stop:    make(chan struct{}),
	}
	go l.cleanupLoop()
	return l
}

// ttlFor：桶从空回满需要 burst/perSecond 秒，清理阈值取其 10 倍并至少 1 分钟。
// perSecond 为 0（极罕见）时固定 10 分钟。
func ttlFor(perSecond rate.Limit, burst int) time.Duration {
	if perSecond <= 0 {
		return 10 * time.Minute
	}
	ttl := time.Duration(float64(burst)/float64(perSecond)*10) * time.Second
	if ttl < time.Minute {
		ttl = time.Minute
	}
	return ttl
}

// Close 停止清理协程，可重复调用。
func (l *Limiter) Close() {
	l.stopOnce.Do(func() { close(l.stop) })
}

// Wrap 返回中间件。超限时返回 429 + Retry-After（秒，向上取整，至少 1）。
func (l *Limiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, retryAfter := l.allow(clientIP(r))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allow 判断 ip 是否放行；不放行时返回建议的重试秒数。
func (l *Limiter) allow(ip string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	e, ok := l.entries[ip]
	if !ok {
		e = &limitEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.entries[ip] = e
	}
	e.lastSeen = now
	if e.limiter.AllowN(now, 1) {
		return true, 0
	}
	// 不放行：等下一个令牌可用需要 (1 - tokens) / limit 秒。
	// 用 Limits() 而不是 ReserveN —— ReserveN 会真的预占令牌，连续被拒
	// 的请求会把恢复时间越推越远，Retry-After 也就失真。
	tokens := e.limiter.TokensAt(now)
	limit := e.limiter.Limit()
	if limit <= 0 {
		return false, 1
	}
	wait := time.Duration(float64(time.Second) * (1 - tokens) / float64(limit))
	sec := int(wait / time.Second)
	if wait%time.Second != 0 {
		sec++
	}
	if sec < 1 {
		sec = 1
	}
	return false, sec
}

// cleanupLoop 定期删除长期未使用的桶。
func (l *Limiter) cleanupLoop() {
	interval := l.ttl / 2
	if interval < time.Second {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case now := <-t.C:
			l.cleanup(now)
		}
	}
}

func (l *Limiter) cleanup(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, e := range l.entries {
		if now.Sub(e.lastSeen) > l.ttl {
			delete(l.entries, ip)
		}
	}
}

// size 返回当前桶数量（测试与诊断用）。
func (l *Limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// clientIP 取连接对端 IP。
//
// 服务只监听 127.0.0.1 且由 Caddy 反代，因此这里**不读 X-Forwarded-For**：
// 客户端可以伪造该头绕过限流。Caddy 的 localhost 转发地址意味着所有请求
// 共享同一个桶 —— 公开接口的限流目标正是「单 IP 刷站」，在本地反代拓扑下
// 退化为全局限流，是可接受且更安全的取舍。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
