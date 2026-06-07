package server

import (
	"net/http"
	"sync"
	"time"

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

type WebsocketManager struct {
	websocket.Upgrader

	logger log.Logger
	//wsConnOptions []func(*wsConnection)

	recvChanMap map[string]chan ResponseMsg // request ID -> waiting response channel
	rcRWLock    *sync.RWMutex

	sendChan   map[string]chan types.RPCRequest // node public key -> outbound request channel
	aliveNodes map[string]struct{}              // online node public key set
	scRWLock   *sync.RWMutex
}

func NewWebsocketManager() *WebsocketManager {
	return &WebsocketManager{
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		logger: log.NewNopLogger(),
		//wsConnOptions: wsConnOptions,

		recvChanMap: make(map[string]chan ResponseMsg),
		rcRWLock:    &sync.RWMutex{},
		sendChan:    make(map[string]chan types.RPCRequest),
		aliveNodes:  make(map[string]struct{}),
		scRWLock:    &sync.RWMutex{},
	}
}

func (wm *WebsocketManager) AliveNodes() []string {
	return []string{}
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

func (wm *WebsocketManager) SendMsg(msg RequestMsg) error {
	return nil
}

// unregisterRecvChan removes a response waiter after the request lifecycle ends.
func (wm *WebsocketManager) unregisterRecvChan(requestId string) {
	wm.rcRWLock.Lock()
	defer wm.rcRWLock.Unlock()
	delete(wm.recvChanMap, requestId)
}
