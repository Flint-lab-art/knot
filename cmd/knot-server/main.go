// Command knot-server 是 knot 的后端服务入口（唯一写入口）。
//
// 监听地址来自 config.toml 的 listen（默认 127.0.0.1:8080，由 Caddy 反代）。
// `knot-server migrate` 执行内嵌的 PG 迁移；部署与 healthz 约定见
// docs/DEPLOYMENT.md。
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

	"github.com/spf13/cobra"

	"knot/internal/config"
	"knot/internal/server"
	"knot/internal/store/pg"
)

// shutdownTimeout 是优雅关闭等待在途请求的上限。
const shutdownTimeout = 10 * time.Second

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "knot-server:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:           "knot-server",
		Short:         "Knot 后端服务（唯一写入口）",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return serve(configPath)
		},
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "配置文件路径（默认仅用内置默认值与环境变量）")

	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "执行 PostgreSQL 迁移（部署/升级时先于启动服务运行）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadServer(configPath)
			if err != nil {
				return err
			}
			if err := pg.Migrate(cmd.Context(), cfg.PGDSN); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "✓ PostgreSQL 迁移完成")
			return err
		},
	}
	root.AddCommand(migrate)

	return root
}

func serve(configPath string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
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
