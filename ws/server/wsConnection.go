package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// wsConnection 表示“一条具体的 WebSocket 长连接”。
// WebsocketManager 解决的是“多连接怎么管”，
// wsConnection 解决的是“单连接怎么读、怎么写、怎么保活、怎么停”。
type wsConnection struct {
	service.BaseService
	remoteAddr string

	baseConn      *websocket.Conn
	nodePublicKey string
	responseChan  chan types.RPCResponse // 节点 -> Manager 的 JSON-RPC 响应会先进入这里
	requestChan   chan types.RPCRequest  // Manager -> 节点 的 JSON-RPC 请求从这里取出发送

	readRoutineQuit   chan struct{}
	writeChanCapacity int                             // 写通道容量
	writeWait         time.Duration                   // 每次写 socket 的超时时间
	readWait          time.Duration                   // 多久没有收到任何数据就认为读超时，包括 ping/pong 在内
	pingPeriod        time.Duration                   // 心跳 ping 的发送周期，必须小于 readWait
	readLimit         int64                           // 单条消息最大读取大小
	onDisconnect      func(remoteAddr, pubKey string) // 连接断开时执行的回调

	ctx    context.Context
	cancel context.CancelFunc
}

// NewWsConnection 把 gorilla/websocket 的底层连接包装成项目内部的连接对象。
// 额外加入了：
// 1. 读写通道；
// 2. ping/pong 与超时控制；
// 3. 启停生命周期；
// 4. 断线回调。
func newWsConnection(baseConn *websocket.Conn, publicKey string, options ...func(*wsConnection)) *wsConnection {
	wsc := &wsConnection{
		remoteAddr:        baseConn.RemoteAddr().String(),
		baseConn:          baseConn,
		nodePublicKey:     publicKey,
		writeWait:         defaultWSWriteWait,
		writeChanCapacity: defaultWSWriteChanCapacity,
		readWait:          defaultWSReadWait,
		pingPeriod:        defaultWSPingPeriod,
		readRoutineQuit:   make(chan struct{}),
	}
	wsc.responseChan = make(chan types.RPCResponse, wsc.writeChanCapacity)
	wsc.requestChan = make(chan types.RPCRequest, wsc.writeChanCapacity)
	for _, option := range options {
		option(wsc)
	}
	wsc.baseConn.SetReadLimit(wsc.readLimit)
	wsc.BaseService = *service.NewBaseService(nil, "wsConnection", wsc)
	return wsc
}

// OnDisconnect 设置断线回调，只应在构造连接时传入。
func OnDisconnect(onDisconnect func(remoteAddr, pubKey string)) func(*wsConnection) {
	return func(wsc *wsConnection) {
		wsc.onDisconnect = onDisconnect
	}
}

// WriteWait 设置写超时，只应在构造时配置。
func WriteWait(writeWait time.Duration) func(*wsConnection) {
	return func(wsc *wsConnection) {
		wsc.writeWait = writeWait
	}
}

// WriteChanCapacity 设置读写缓冲通道容量，只应在构造时配置。
func WriteChanCapacity(cap int) func(*wsConnection) {
	return func(wsc *wsConnection) {
		wsc.writeChanCapacity = cap
	}
}

// ReadWait 设置读超时，只应在构造时配置。
func ReadWait(readWait time.Duration) func(*wsConnection) {
	return func(wsc *wsConnection) {
		wsc.readWait = readWait
	}
}

// PingPeriod 设置 ping 发送周期，只应在构造时配置。
func PingPeriod(pingPeriod time.Duration) func(*wsConnection) {
	return func(wsc *wsConnection) {
		wsc.pingPeriod = pingPeriod
	}
}

// ReadLimit 设置最大读取消息长度，只应在构造时配置。
func ReadLimit(readLimit int64) func(*wsConnection) {
	return func(wsc *wsConnection) {
		wsc.readLimit = readLimit
	}
}

// OnStart 启动单连接的读写循环。
// 这里特意采用“一个读协程 + 当前 goroutine 负责写”的模型，
// 这样可以保证对 websocket 的写操作始终串行，避免并发写冲突。
func (wsc *wsConnection) OnStart() error {
	// 读协程：持续从 socket 读节点返回的响应。
	go wsc.readRoutine()
	// 写循环：发送业务请求，同时负责 ping/pong 保活。
	wsc.writeRoutine()
	return nil
}

func (wsc *wsConnection) OnStop() {
	if wsc.onDisconnect != nil {
		wsc.onDisconnect(wsc.remoteAddr, wsc.nodePublicKey)
	}
	if wsc.ctx != nil {
		wsc.cancel()
	}
}

// Output 暴露节点响应输出通道，供 OnConnect 中的路由协程消费。
func (wsc *wsConnection) Output() chan types.RPCResponse {
	return wsc.responseChan
}

func (wsc *wsConnection) GetRemoteAddr() string {
	return wsc.remoteAddr
}

// WriteRPCResponse 把一个节点响应写入 responseChan，必要时阻塞等待。
func (wsc *wsConnection) WriteRPCResponse(ctx context.Context, resp types.RPCResponse) error {
	select {
	case <-wsc.Quit():
		return errors.New("connection was stopped")
	case <-ctx.Done():
		return ctx.Err()
	case wsc.responseChan <- resp:
		return nil
	}
}

// TryWriteRPCResponse 尝试非阻塞写入 responseChan。
func (wsc *wsConnection) TryWriteRPCResponse(resp types.RPCResponse) bool {
	select {
	case <-wsc.Quit():
		return false
	case wsc.responseChan <- resp:
		return true
	default:
		return false
	}
}

// Context 返回连接级 context，连接关闭时会被取消。
func (wsc *wsConnection) Context() context.Context {
	if wsc.ctx != nil {
		return wsc.ctx
	}
	wsc.ctx, wsc.cancel = context.WithCancel(context.Background())
	return wsc.ctx
}

// readRoutine 持续读取“节点 -> Manager”的 JSON-RPC 响应。
func (wsc *wsConnection) readRoutine() {
	writeCtx := context.Background()
	// 如果 readRoutine 自己 panic，下面会尝试构造一个内部错误响应写回内部通道。
	defer func() {
		if r := recover(); r != nil {
			err, ok := r.(error)
			if !ok {
				err = fmt.Errorf("WSJSONRPC: %v", r)
			}
			wsc.Logger.Error("Panic in WSJSONRPC handler", "err", err, "stack", string(debug.Stack()))
			if err := wsc.WriteRPCResponse(writeCtx, types.RPCInternalError(types.JSONRPCIntID(-1), err)); err != nil {
				wsc.Logger.Error("Error writing RPC response", "err", err)
			}
			//使用 go wsc.readRoutine() 重新启动读取循环，保证 WebSocket 连接的健壮性。
			go wsc.readRoutine()
		}
	}()

	//WebSocket 中，客户端会发送 ping，服务端回复 pong。 每收到一次 pong，就延长读超时 readWait。
	//心跳机制，确保连接活跃
	wsc.baseConn.SetPongHandler(func(ms string) error {
		return wsc.baseConn.SetReadDeadline(time.Now().Add(wsc.readWait))
	})

	for {
		select {
		//先检查 wsc.Quit() 信号，如果连接被关闭，则退出
		case <-wsc.Quit():
			return
		default:
			// 每次阻塞读之前都刷新 deadline，控制消息和业务消息都算活跃流量。
			if err := wsc.baseConn.SetReadDeadline(time.Now().Add(wsc.readWait)); err != nil {
				wsc.Logger.Error("Error setting read deadline", "err", err)
			}
			//NextReader() 阻塞直到收到一条消息。
			_, r, err := wsc.baseConn.NextReader()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					wsc.Logger.Info("client closed the connection")
				} else {
					wsc.Logger.Error("Failed to read request", "err", err)
				}
				// 读侧一旦失败，关闭 readRoutineQuit，让写侧也跟着退出。
				close(wsc.readRoutineQuit)
				return
			}
			dec := json.NewDecoder(r)
			var response types.RPCResponse
			err = dec.Decode(&response)
			if err != nil {
				wsc.Logger.Error("error unmarshaling response", "err", err)
				continue
			}
			// 服务端在这条连接上只期望收到“响应”，没有 ID 的消息直接丢弃。
			if response.ID == nil {
				wsc.Logger.Info("[WS]received response with no ID, drop it")
				continue
			}
			if err := wsc.WriteRPCResponse(writeCtx, response); err != nil {
				wsc.Logger.Error("Error writing RPC response", "err", err)
			}
		}
	}
}

// 负责把所有消息统一序列化后写入 WebSocket
// Pong 消息 → 响应客户端 ping。
// Ping 消息 → 定时发送心跳。
// 业务消息 → 从 requestChan 获取 RPC 请求写入客户端。
func (wsc *wsConnection) writeRoutine() {
	pingTicker := time.NewTicker(wsc.pingPeriod)
	defer pingTicker.Stop()

	pongs := make(chan string, 1)
	wsc.baseConn.SetPongHandler(func(ms string) error {
		select {
		case pongs <- ms:
		default:
		}
		// 不在 ping handler 里直接写 pong，避免与业务写并发写 socket。
		// 将 ping 内容发送到 pongs 通道, 统一交给 writeRoutine 串行写出。
		return nil
	})

	for {
		select {
		case <-wsc.Quit(): //收到 Quit() 信号（主动关闭）
			return
		case <-wsc.readRoutineQuit: //读端异常或关闭
			return
		case m := <-pongs:
			//从 pongs 通道读取node ping 内容。写出对应的 Pong 消息。
			err := wsc.writeMessageWithDeadline(websocket.PongMessage, []byte(m))
			if err != nil {
				wsc.Logger.Info("Failed to write pong (client may disconnect)", "err", err)
			}
		case <-pingTicker.C:
			//每个周期发送一次 Ping 消息。如果写 ping 失败，说明连接可能断开，直接退出写循环。
			//保持长连接活跃，检测客户端是否在线。
			err := wsc.writeMessageWithDeadline(websocket.PingMessage, []byte{})
			if err != nil {
				wsc.Logger.Error("Failed to write ping", "err", err)
				return
			}
		case msg := <-wsc.requestChan:
			// Manager 业务层只负责把 RPCRequest 投进 requestChan，
			// 真正的 JSON 序列化和 socket 写出统一由当前写循环完成。
			wsc.Logger.Info("send msg from requestChan to target client", "method", msg.Method)
			jsonBytes, err := json.Marshal(msg)
			if err != nil {
				wsc.Logger.Error("Failed to marshal RPCRequest to JSON", "err", err)
				continue
			}
			if err := wsc.writeMessageWithDeadline(websocket.TextMessage, jsonBytes); err != nil {
				wsc.Logger.Error("Failed to write request", "err", err, "msg", msg)
				return
			}
		}
	}
}

// writeMessageWithDeadline 统一封装写超时和实际写 socket 的动作。
func (wsc *wsConnection) writeMessageWithDeadline(msgType int, msg []byte) error {
	if err := wsc.baseConn.SetWriteDeadline(time.Now().Add(wsc.writeWait)); err != nil {
		return err
	}
	return wsc.baseConn.WriteMessage(msgType, msg)
}
