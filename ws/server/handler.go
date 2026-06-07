package server

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

const (
	// The websocket handshake must carry a recent timestamp to reduce replay risk.
	legalTimeStampPeriod       = 5
	messageSignatureLength     = 64
	publicKeyLength            = 66
	defaultWSWriteChanCapacity = 100
	defaultWSWriteWait         = 10 * time.Second
	defaultWSReadWait          = 30 * time.Second
	defaultWSPingPeriod        = (defaultWSReadWait * 9) / 10
)

// WebsocketManager 解决的是“多连接怎么管”，
type WebsocketManager struct {
	websocket.Upgrader

	logger        log.Logger
	wsConnOptions []func(*wsConnection)

	recvChanMap map[string]chan ResponseMsg // request ID -> waiting response channel
	rcRWLock    *sync.RWMutex

	sendChan   map[string]chan types.RPCRequest // node public key -> outbound request channel
	aliveNodes map[string]struct{}              // online node public key set
	scRWLock   *sync.RWMutex
}

// NewWebsocketManager 创建连接管理器。
// 这里会初始化两类核心索引：
// 1. recvChanMap：按请求 ID 找等待响应的人；
// 2. sendChan：按节点公钥找到对应连接的发送通道。
func NewWebsocketManager(wsConnOptions ...func(*wsConnection)) *WebsocketManager {
	return &WebsocketManager{
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		logger:        log.NewNopLogger(),
		wsConnOptions: wsConnOptions,

		recvChanMap: make(map[string]chan ResponseMsg),
		rcRWLock:    &sync.RWMutex{},

		sendChan:   make(map[string]chan types.RPCRequest),
		aliveNodes: make(map[string]struct{}),
		scRWLock:   &sync.RWMutex{},
	}
}

// SetWsConnOptions 设置每条新连接创建后要附加的回调/选项。
func (wm *WebsocketManager) SetWsConnOptions(wsConnOptions ...func(*wsConnection)) {
	wm.wsConnOptions = wsConnOptions
}

func (wm *WebsocketManager) SetLogger(logger log.Logger) {
	wm.logger = logger
}

// AliveNodes 返回当前在线节点公钥列表的一个快照。
func (wm *WebsocketManager) AliveNodes() []string {
	ret := make([]string, 0)
	for node := range wm.aliveNodes {
		ret = append(ret, node)
	}
	return ret
}

// RegisterResChannel registers the response channel that should receive replies
// for a given JSON-RPC request ID.
func (wm *WebsocketManager) RegisterResChannel(requestId string, recvChan chan ResponseMsg, stopChan chan struct{}) error {
	wm.rcRWLock.Lock()
	defer wm.rcRWLock.Unlock()
	wm.recvChanMap[requestId] = recvChan

	go func() {
		// The business layer closes/stops this channel once it no longer wants replies.
		<-stopChan
		wm.unregisterRecvChan(requestId)
	}()
	return nil
}

// SendMsg 按目标节点公钥把 JSON-RPC 请求投递到对应连接的 requestChan。
// 这里不直接写 socket，而是交给该连接自己的写协程串行发送。
func (wm *WebsocketManager) SendMsg(msg RequestMsg) error {
	wm.scRWLock.RLock()
	defer wm.scRWLock.RUnlock()
	sendChan, ok := wm.sendChan[msg.TargetNode]
	if !ok {
		return fmt.Errorf("the node(%s) is lost", msg.TargetNode)
	}
	go func() {
		sendChan <- msg.RpcRequest
	}()
	return nil
}

// unregisterRecvChan removes a response waiter after the request lifecycle ends.
func (wm *WebsocketManager) unregisterRecvChan(requestId string) {
	wm.rcRWLock.Lock()
	defer wm.rcRWLock.Unlock()
	delete(wm.recvChanMap, requestId)
}

// clientConnected 在节点连上来之后注册两类信息：
// 1. sendChan：后续给这个节点发消息要走的通道；
// 2. aliveNodes：当前在线节点集合。
func (wm *WebsocketManager) clientConnected(pubkey string, channel chan types.RPCRequest) {
	wm.scRWLock.Lock()
	defer wm.scRWLock.Unlock()

	if _, ok := wm.sendChan[pubkey]; !ok {
		wm.sendChan[pubkey] = channel
	}
	if wm.aliveNodes == nil {
		wm.aliveNodes = make(map[string]struct{})
	}
	wm.aliveNodes[pubkey] = struct{}{}
	wm.logger.Info("new node connected", "public key", pubkey)
}

// clientDisconnected 在连接断开后清理该节点的路由状态，
func (wm *WebsocketManager) clientDisconnected(pubkey string) {
	wm.scRWLock.Lock()
	defer wm.scRWLock.Unlock()

	delete(wm.aliveNodes, pubkey)
	delete(wm.sendChan, pubkey)
	wm.logger.Info("node disconnected", "public key", pubkey)
}

// WebsocketHandler 是 `/ws` 的入口处理函数。
// 它负责：
// 1. 把 HTTP 升级成 WebSocket；
// 2. 校验请求头里的身份信息；
// 3. 创建单连接对象 wsConnection；启动这条连接的读写循环。
func (wm *WebsocketManager) WebsocketHandler(w http.ResponseWriter, r *http.Request) {
	//1：把 HTTP 请求升级为 WebSocket 会话
	wsConn, err := wm.Upgrade(w, r, nil)
	if err != nil {
		wm.logger.Error("Failed to upgrade connection", "err", err)
		http.Error(w, "Failed to upgrade connection", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := wsConn.Close(); err != nil {
			wm.logger.Error("Failed to close connection", "err", err)
		}
	}()
	// 2. 校验请求头里的身份信息；
	// 节点通过 3 个 header 完成身份证明：
	// pubKey：节点公钥（十六进制）
	// sig：对时间戳摘要做出的签名
	// time：当前 unix 时间戳，用于限制重放窗口
	pubKey := r.Header.Get("pubKey")
	if len(pubKey) < publicKeyLength {
		wm.logger.Error("Failed to establish connection", "err", fmt.Errorf("invalid pubKey in header, expected length %d, actual length %d", publicKeyLength, len(pubKey)))
		return
	}
	sig := r.Header.Get("sig")
	if len(sig) < messageSignatureLength {
		wm.logger.Error("Failed to establish connection", "err", fmt.Errorf("failed to establish connection, expected length %d, actual length %d", messageSignatureLength, len(sig)))
		return
	}
	timeStr := r.Header.Get("time")
	if len(timeStr) == 0 {
		wm.logger.Error("Failed to establish connection", "err", fmt.Errorf("failed to establish connection, expected length %d, actual length %d", 0, len(timeStr)))
		return
	}
	timeInt64, err := strconv.ParseInt(timeStr, 10, 64)
	if err != nil || timeInt64 < 0 {
		wm.logger.Error("illegal timestamp", "err", err)
		return
	}
	// 时间戳过期直接拒绝，避免旧签名被反复拿来重放连接。
	if time.Now().Unix()-timeInt64 > legalTimeStampPeriod {
		wm.logger.Error("illegal timestamp", "err", errors.New("reject because illegal timestamp"))
		return
	}

	pubKeyBytes, pubErr := hex.DecodeString(pubKey)
	sigBytes, sigErr := hex.DecodeString(sig)
	if pubErr != nil || sigErr != nil {
		wm.logger.Error("hex decode error for pubkey or sig", "err", err)
		return
	}
	if len(sigBytes) < 64 {
		wm.logger.Error(fmt.Sprintf("invalid sigBytes, expected length is no less than 64, actual length is %d", len(sigBytes)))
		return
	}
	// 用公钥验签，确认这个连接方真的持有对应私钥。
	// 签名内容不是任意字符串，而是 timeStr 的 Keccak256 摘要。
	digestBz := crypto.Keccak256Hash([]byte(timeStr)).Bytes()
	if !crypto.VerifySignature(pubKeyBytes, digestBz, sigBytes[:64]) {
		wm.logger.Error("illegal signature", "publicKey", pubKey, "time", timeStr, "signature", sig)
		return
	}

	// 3.创建单连接对象 wsConnection
	// 之后这条连接自己的生命周期、读写协程、心跳维护都交给 wsConnection 管。
	con := newWsConnection(wsConn, pubKey, wm.wsConnOptions...)
	// Start 是阻塞的：只要这条连接还活着，就一直停在这里跑读写循环。
	err = con.Start()
	if err != nil {
		wm.logger.Error("Failed to start connection", "err", err)
		return
	}
	if err := con.Stop(); err != nil {
		wm.logger.Error("error while stopping connection", "error", err)
	}

}
