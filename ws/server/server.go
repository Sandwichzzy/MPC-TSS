package server

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
	rpcserver "github.com/tendermint/tendermint/rpc/jsonrpc/server"
	tmtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
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
	wsServer := &WSServer{}
	var err error

	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	// 这里复用 Tendermint 提供的 JSON-RPC HTTP server 外壳：
	// 它已经把 listener、serve、logger、基础 server 配置封装好了，
	// 本项目只需要在这个壳子里挂上自己的 `/ws` 处理函数即可，不需要自己再重复实现一套 HTTP/WebSocket 服务框架。
	wsServer.Config = rpcserver.DefaultConfig()
	wsServer.Handler = http.NewServeMux()

	mux := http.NewServeMux()
	wmLogger := logger.With("protocol", "ws")

	wsServer.WM = NewWebsocketManager()

	// 当一个 Node 完成 WebSocket 鉴权后，需要把它的公钥和这条连接的 requestChan 绑定起来。
	// 后续 Manager 想给某个 Node 下发 JSON-RPC 请求时，就能通过公钥找到对应的发送通道。todo
	wsServer.WM.SetWsConnOptions(
		OnConnect(wsServer.WM),
		OnDisconnect(func(remoteAddr, pubKey string) {
			wsServer.WM.clientDisconnected(pubKey)
		}),
	)

	wsServer.WM.SetLogger(wmLogger)
	wsServer.Logger = wmLogger
	// 所有 Node 都通过 `/ws` 入口接入。
	mux.HandleFunc("/ws", wsServer.WM.WebsocketHandler)
	wsServer.Handler = mux

	// 先创建监听器，真正阻塞接收连接的是下面的 rpcserver.Serve。
	wsServer.Listener, err = rpcserver.Listen(localAddr, wsServer.Config.MaxOpenConnections)
	if err != nil {
		return nil, err
	}

	go func() {
		// Serve 会阻塞，因此放到独立 goroutine 中运行。
		if err := rpcserver.Serve(
			wsServer.Listener,
			wsServer.Handler,
			wsServer.Logger,
			wsServer.Config,
		); err != nil {
			panic(err)
		}
	}()
	return wsServer.WM, nil
}

// OnConnect 是一个连接建立后的回调。
// 它做两件关键事情：
// 1. 把 nodePublicKey -> requestChan 注册到 WebsocketManager；
// 2. 启动一个转发协程，把该 Node 返回的响应Output() 按 request ID 分发给等待中的业务方。
func OnConnect(wm *WebsocketManager) func(wsc *wsConnection) {
	return func(wsc *wsConnection) {
		// 连接一建立，立刻记录该节点对应的发送通道。
		wm.clientConnected(wsc.nodePublicKey, wsc.requestChan)
		go func() {
			for {
				select {
				case res := <-wsc.Output():
					func() {
						wm.rcRWLock.Lock()
						defer wm.rcRWLock.Unlock()
						if len(wm.recvChanMap) > 0 {
							// response.ID 是整个请求-响应关联的关键。
							// Manager 发请求前会先注册一个 requestId -> recvChan，
							// 这里收到节点响应后，就根据 ID 把结果投递回原先等待响应的业务协程。
							id := res.ID.(tmtypes.JSONRPCStringID).String()
							recvChan, ok := wm.recvChanMap[id]
							if ok {
								recvChan <- ResponseMsg{
									RpcResponse: res,
									SourceNode:  wsc.nodePublicKey,
								}
								return
							}
						}
						wsc.Logger.Info("[WS]received unrecognized responseID", "ID", res.ID.(tmtypes.JSONRPCStringID).String(), "CurrentMap", fmt.Sprintf("%v", wm.recvChanMap))
					}()

				case <-wsc.readRoutineQuit:
					return
				}
			}
		}()
	}
}
