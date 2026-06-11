package tm

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
	"github.com/rcrowley/go-metrics"
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

const (
	defaultMaxReconnectAttempts = 25
	defaultWriteWait            = 0
	defaultReadWait             = 0
	defaultPingPeriod           = 0
	defaultMaxSleepTime         = 3600 * time.Second
)

// WSClient is a JSON-RPC client, which uses WebSocket for communication with the remote server.
//
// WSClient is safe for concurrent use by multiple goroutines.
type WSClient struct {
	conn        *websocket.Conn
	Address     string                                 //  "127.0.0.1:8080"
	Endpoint    string                                 // "/ws"
	Dialer      func(string, string) (net.Conn, error) //Dialer 用于底层 TCP 建连。
	PubKey      string                                 //用于 WebSocket 握手认证。
	PriKey      *ecdsa.PrivateKey                      //用于 WebSocket 握手认证。
	RequestsCh  chan types.RPCRequest                  //Manager 推送过来的 RPC 请求。
	onReconnect func()                                 //重连成功后调用。

	// internal channels
	send                 chan types.RPCResponse // node -> manager 待发送响应队列
	backlog              chan types.RPCResponse // 断线期间保存一条未发送成功的消息,重连成功，补发
	reconnectAfter       chan error             // 重连通知
	readRoutineQuit      chan struct{}          // 读协程通知写协程退出。
	maxReconnectAttempts int                    //(0 or greater; default: 25).
	protocol             string                 // Support both ws and wss protocols
	wg                   sync.WaitGroup
	mtx                  sync.RWMutex
	sentLastPingAt       time.Time
	reconnecting         bool
	nextReqID            int
	writeWait            time.Duration // Time allowed to write a message to the server. 0 means block until operation succeeds.
	readWait             time.Duration // Time allowed to read the next message from the server. 0 means block until operation succeeds.
	pingPeriod           time.Duration // Send pings to server with this period. Must be less than readWait. If 0, no pings will be sent.

	service.BaseService                // Tendermint 生命周期组件:Start() Stop() Quit() IsRunning()
	PingPongLatencyTimer metrics.Timer // Time between sending a ping and receiving a pong.
}

func NewWS(remoteAddr, endpoint string, options ...func(*WSClient)) (*WSClient, error) {
	parsedURL, err := newParsedURL(remoteAddr)
	if err != nil {
		return nil, err
	}

	if parsedURL.Scheme != protoWSS {
		parsedURL.Scheme = protoWS
	}
	dialFn, err := makeHTTPDialer(remoteAddr)
	if err != nil {
		return nil, err
	}

	c := &WSClient{
		Address:              parsedURL.GetTrimmedHostWithPath(),
		Dialer:               dialFn,
		Endpoint:             endpoint,
		PingPongLatencyTimer: metrics.NewTimer(),

		maxReconnectAttempts: defaultMaxReconnectAttempts,
		readWait:             defaultReadWait,
		writeWait:            defaultWriteWait,
		pingPeriod:           defaultPingPeriod,
		protocol:             parsedURL.Scheme,
	}
	c.BaseService = *service.NewBaseService(nil, "WSClient", c)

	for _, option := range options {
		option(c)
	}
	return c, nil
}

func MaxReconnectAttempts(max int) func(*WSClient) {
	return func(c *WSClient) {
		c.maxReconnectAttempts = max
	}
}

func ReadWait(readWait time.Duration) func(*WSClient) {
	return func(c *WSClient) {
		c.readWait = readWait
	}
}

func WriteWait(writeWait time.Duration) func(*WSClient) {
	return func(c *WSClient) {
		c.writeWait = writeWait
	}
}

func PingPeriod(pingPeriod time.Duration) func(*WSClient) {
	return func(c *WSClient) {
		c.pingPeriod = pingPeriod
	}
}

func OnReconnect(cb func()) func(*WSClient) {
	return func(c *WSClient) {
		c.onReconnect = cb
	}
}

// String returns WS client full address.
func (c *WSClient) String() string {
	return fmt.Sprintf("WSClient{%s (%s)}", c.Address, c.Endpoint)
}

// OnStart implements service.Service by dialing a server and creating read and write routines.
func (c *WSClient) OnStart() error {
	// First connection attempt: this performs the websocket handshake and sends the authentication headers expected by the Manager websocket server.
	err := c.dial()
	if err != nil {
		return err
	}

	// RequestsCh is what upper layers read from when the Manager pushes RPCRequest to this node.
	c.RequestsCh = make(chan types.RPCRequest)
	c.send = make(chan types.RPCResponse)
	// 1 additional error may come from the read/write goroutine depending on which failed first.
	c.reconnectAfter = make(chan error, 1)
	// capacity for 1 response. a user won't be able to send more because the send channel is unbuffered.
	c.backlog = make(chan types.RPCResponse, 1)
	// After the first successful dial, start the long-lived reader, writer and reconnect supervisor.
	c.startReadWriteRoutines()
	go c.reconnectRoutine()

	return nil
}

// Stop overrides service.Service#Stop. There is no other way to wait until Quit channel is closed.
func (c *WSClient) Stop() error {
	if err := c.BaseService.Stop(); err != nil {
		return err
	}
	// only close user-facing channels when we can't write to them
	c.wg.Wait()
	close(c.RequestsCh)

	return nil
}

func (c *WSClient) IsReconnecting() bool {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.reconnecting
}

func (c *WSClient) IsActive() bool {
	return c.IsRunning() && !c.IsReconnecting()
}

// Send queues a node-produced JSON-RPC response to be written to the Manager.
// It blocks until the writer goroutine accepts the message or ctx.Done fires.
func (c *WSClient) Send(ctx context.Context, request types.RPCResponse) error {
	select {
	case c.send <- request:
		// On the node side, "send" means pushing a JSON-RPC response back to Manager.
		c.Logger.Info("sent a request", "reqId", request.ID)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ======================Private methods===========================

func (c *WSClient) nextRequestID() types.JSONRPCIntID {
	c.mtx.Lock()
	id := c.nextReqID
	c.nextReqID++
	c.mtx.Unlock()
	return types.JSONRPCIntID(id)
}

func (c *WSClient) dial() error {
	dialer := &websocket.Dialer{
		NetDial: c.Dialer,
		Proxy:   http.ProxyFromEnvironment,
	}
	rHeader := http.Header{}
	timeStr := strconv.FormatInt(time.Now().Unix(), 10)
	// The Manager authenticates websocket clients by verifying a signature over the timestamp.
	digestBz := crypto.Keccak256Hash([]byte(timeStr)).Bytes()
	sig, err := crypto.Sign(digestBz, c.PriKey)
	if err != nil {
		return err
	}
	// These three headers must match the checks in ws/server/handler.go:WebsocketHandler.
	rHeader.Set("pubKey", c.PubKey)
	rHeader.Set("sig", hex.EncodeToString(sig))
	rHeader.Set("time", timeStr)
	conn, _, err := dialer.Dial(c.protocol+"://"+c.Address+c.Endpoint, rHeader) // nolint:bodyclose
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// reconnect tries to redial up to maxReconnectAttempts with exponential backoff
func (c *WSClient) reconnect() error {
	attempt := 0

	c.mtx.Lock()
	c.reconnecting = true
	c.mtx.Unlock()

	defer func() {
		c.mtx.Lock()
		c.reconnecting = false
		c.mtx.Unlock()
	}()

	for {
		backoffDuration := (1 << uint(attempt)) * time.Second
		if backoffDuration > defaultMaxSleepTime {
			backoffDuration = defaultMaxSleepTime
		}
		c.Logger.Info("reconnecting", "attempt", attempt+1, "backoff_duration", backoffDuration)
		time.Sleep(backoffDuration)

		err := c.dial()
		if err != nil {
			c.Logger.Error("failed to redial", "err", err)
		} else {
			c.Logger.Info("reconnected")
			if c.onReconnect != nil {
				go c.onReconnect()
			}
			return nil
		}

		attempt++

		if attempt > c.maxReconnectAttempts {
			return fmt.Errorf("reached maximum reconnect attempts: %w", err)
		}
	}
}

func (c *WSClient) startReadWriteRoutines() {
	c.wg.Add(2)
	c.readRoutineQuit = make(chan struct{})
	go c.readRoutine()
	go c.writeRoutine()
}

func (c *WSClient) reconnectRoutine() {
	for {
		select {
		case originalError := <-c.reconnectAfter:
			// Wait until the old read/write goroutines have fully exited before redialing.
			// wait until writeRoutine and readRoutine finish
			c.wg.Wait()
			if err := c.reconnect(); err != nil {
				c.Logger.Error("failed to reconnect", "err", err, "original_err", originalError)
				if err = c.Stop(); err != nil {
					c.Logger.Error("failed to stop conn", "error", err)
				}
				return
			}
		LOOP:
			for {
				select {
				case <-c.reconnectAfter:
					//如果有数据 → 丢掉
				default:
					break LOOP
					//如果没有数据 → 退出循环
				}
			}
			// Once the socket is back, replay the buffered response before resuming normal traffic.
			err := c.processBacklog()
			if err == nil {
				c.startReadWriteRoutines()
			}
		case <-c.Quit():
			return

		}
	}
}

func (c *WSClient) processBacklog() error {
	select {
	case request := <-c.backlog:
		// If the last outbound response was interrupted by a disconnect, retry it first after reconnect.
		if c.writeWait > 0 {
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeWait)); err != nil {
				c.Logger.Error("failed to set write deadline", "err", err)
			}
		}
		if err := c.conn.WriteJSON(request); err != nil {
			c.Logger.Error("failed to resend request", "err", err)
			c.reconnectAfter <- err
			// Requeue the response so it can be retried after the next reconnect.
			c.backlog <- request
			return err
		}
		c.Logger.Info("resend a request", "req", request)
	default:
	}
	return nil
}

func (c *WSClient) writeRoutine() {
	var ticker *time.Ticker
	if c.pingPeriod > 0 {
		// 如果启用了心跳
		ticker = time.NewTicker(c.pingPeriod)
	} else {
		//关闭 Ping 功能
		ticker = &time.Ticker{C: make(<-chan time.Time)}
	}

	defer func() {
		ticker.Stop()
		c.conn.Close()
		c.wg.Done()
	}()

	for {
		select {
		case request := <-c.send:
			if c.writeWait > 0 {
				if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeWait)); err != nil {
					c.Logger.Error("failed to set write deadline", "err", err)
				}
			}
			// Node -> Manager traffic consists of JSON-RPC responses generated by signer logic.
			if err := c.conn.WriteJSON(request); err != nil {
				c.Logger.Error("failed to send request", "err", err)
				c.reconnectAfter <- err
				// add request to the backlog, so we don't lose it
				c.backlog <- request
				return
			}
		case <-ticker.C:
			if c.writeWait > 0 {
				if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeWait)); err != nil {
					c.Logger.Error("failed to set write deadline", "err", err)
				}
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				c.Logger.Error("failed to write ping", "err", err)
				c.reconnectAfter <- err
				return
			}
			c.mtx.Lock()
			c.sentLastPingAt = time.Now()
			c.mtx.Unlock()
			c.Logger.Debug("sent ping")
		case <-c.readRoutineQuit:
			// Stop writing once the reader has observed a fatal socket error.
			return
		case <-c.Quit():
			if err := c.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			); err != nil {
				c.Logger.Error("failed to write message", "err", err)
			}
			return
		}
	}
}

// 客户端保证，同一个 WebSocket 连接在任何时刻最多只有一个读者，所有读操作都在这个 goroutine 中执行。
func (c *WSClient) readRoutine() {
	defer func() {
		err := c.conn.Close()
		if err != nil {
			c.Logger.Error("failed to close connection", "err", err)
		}
		c.wg.Done()
	}()

	c.conn.SetPongHandler(func(string) error {
		c.mtx.RLock()
		t := c.sentLastPingAt
		c.mtx.RUnlock()
		c.PingPongLatencyTimer.UpdateSince(t)

		c.Logger.Debug("got pong")
		return nil
	})

	for {
		// reset deadline for every message type (control or data)
		if c.readWait > 0 {
			if err := c.conn.SetReadDeadline(time.Now().Add(c.readWait)); err != nil {
				c.Logger.Error("failed to set read deadline", "err", err)
			}
		}
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
				return
			}

			c.Logger.Error("failed to read response", "err", err)
			close(c.readRoutineQuit)
			c.reconnectAfter <- err
			return
		}

		var request types.RPCRequest
		err = json.Unmarshal(data, &request)
		if err != nil {
			c.Logger.Error("failed to parse response", "err", err, "data", string(data))
			continue
		}
		// The client side only expects Manager -> Node JSON-RPC requests on this websocket.
		if err = validateResponseID(request.ID); err != nil {
			c.Logger.Error("error in response ID", "id", request.ID, "err", err)
			continue
		}

		c.Logger.Info("got request", "id", request.ID)
		select {
		case <-c.Quit():
		case c.RequestsCh <- request:
		}
	}
}
