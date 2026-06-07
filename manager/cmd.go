package manager

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/config"
	"github.com/Sandwichzzy/tss/manager/router"
	"github.com/Sandwichzzy/tss/manager/store"
	"github.com/Sandwichzzy/tss/ws/server"
	"github.com/ethereum/go-ethereum/log"
	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manager",
		Short: "launch a tss manager process",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd)
		},
	}
	return cmd
}

func run(cmd *cobra.Command) error {
	config := config.GetConfigFromCmd(cmd)

	// Manager 先初始化本地持久化存储，用来保存任务和中间状态。
	managerStore, err := store.NewStorage(config.Manager.DBDir)
	if err != nil {
		return err
	}

	// WebSocket 服务负责和各个 Node 建连，转发 keygen/sign 协议消息。
	// 这条链路与下面的 HTTP API 服务独立，Node 只会连到这里的 `/ws` 入口。
	wsServer, err := server.NewWSServer(config.Manager.WsAddr)
	if err != nil {
		return err
	}

	manager, err := NewManager(wsServer, managerStore, config)
	if err != nil {
		return err
	}
	manager.Start()

	// HTTP 路由对外暴露管理接口，例如 keygen、sign 等 API。
	registry := router.NewRegistry(manager)
	r := gin.Default()
	registry.Register(r)

	var s *http.Server
	if config.Manager.JwtSecret != "" {
		// 配置了 JWT 时，API 入口会先做鉴权再进入路由。
		jwtHandler, err := common.NewJwtHandler(r, config.Manager.JwtSecret)
		if err != nil {
			return err
		}
		s = &http.Server{
			Addr:    config.Manager.HttpAddr,
			Handler: jwtHandler,
		}
	} else {
		s = &http.Server{
			Addr:    config.Manager.HttpAddr,
			Handler: r,
		}
	}

	go func() {
		// HTTP 服务独立启动，主 goroutine 继续等待退出信号。
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("api server starts failed", err)
		}
	}()

	//创建信号通道
	quit := make(chan os.Signal, 1)
	// 把系统信号“转发”到 quit channel
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit //阻塞等待
	log.Info("Shutting down server...")

	//先停业务，再停 HTTP
	manager.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	//优雅关闭 HTTP Server
	if err := s.Shutdown(ctx); err != nil {
		log.Error("Server forced to shutdown", err)
		return err
	}
	log.Info("Server exiting")

	return nil
}
