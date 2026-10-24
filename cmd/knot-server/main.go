// Command knot-server 是 knot 的后端服务入口（唯一写入口）。
//
// 监听地址来自 config.toml 的 listen（默认 127.0.0.1:8080，由 Caddy 反代）。
// 部署与 healthz 约定见 docs/DEPLOYMENT.md。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"knot/internal/config"
	"knot/internal/server"
)

// shutdownTimeout 是优雅关闭等待在途请求的上限。
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "knot-server:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.DefaultServer()
	srv := server.New(server.Config{Addr: cfg.Listen}, nil)

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("knot-server listening", "addr", cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("knot-server shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
