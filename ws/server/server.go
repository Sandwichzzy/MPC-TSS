package server

import (
	"net"
	"net/http"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
	rpcserver "github.com/tendermint/tendermint/rpc/jsonrpc/server"
)

// WSServer 是 Manager 进程里 WebSocket 服务端的外层封装。
// 它本身不直接处理业务消息，而是负责：
// 1. 持有监听器和 HTTP handler；
// 2. 把 `/ws` 路由挂出来；
// 3. 持有真正做连接管理和消息路由的 WebsocketManager。
type WSServer struct {
	service.BaseService

	Config *rpcserver.Config
	Logger log.Logger

	Listener net.Listener
	Handler  http.Handler
	WM       *WebsocketManager
}

// NewWSServer 创建 Manager <-> Node 之间使用的 WebSocket 服务。
func NewWSServer(localAddr string) (*WebsocketManager, error) {
	//wsServer := &WSServer{}
	//var err error

	//logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	//// 这里复用 Tendermint 提供的 JSON-RPC HTTP server 外壳：
	//// 它已经把 listener、serve、logger、基础 server 配置封装好了，
	//// 本项目只需要在这个壳子里挂上自己的 `/ws` 处理函数即可，
	//// 不需要自己再重复实现一套 HTTP/WebSocket 服务框架。
	//wsServer.Config = rpcserver.DefaultConfig()
	//wsServer.Handler = http.NewServeMux()
	//
	//mux := http.NewServeMux()
	//wmLogger := logger.With("protocol", "ws")
	return nil, nil
}
