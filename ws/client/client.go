package client

import (
	"context"
	"crypto/ecdsa"
	"sync"
	"time"

	"github.com/Sandwichzzy/tss/ws/client/tm"
	"github.com/ethereum/go-ethereum/log"
	tmtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// WSClients is the node-side wrapper around the low-level websocket JSON-RPC client.
type WSClients struct {
	mtx sync.RWMutex

	ReqChan  chan tmtypes.RPCRequest //从 Manager → Node 的请求,ReqChan 不是 websocket 层直接写入，只是 rspListener 做的一次“桥接转发
	StopChan chan struct{}

	Cli *tm.WSClient
}

func NewWSClients(remoteAddr, endpoint string, privKey *ecdsa.PrivateKey, pubkey string) (*WSClients, error) {
	if client, err := tm.NewWS(remoteAddr, endpoint); err != nil {
		return nil, err
	} else {
		client.PubKey = pubkey
		client.PriKey = privKey
		wsc := &WSClients{
			Cli: client,
		}
		if err := wsc.Cli.Start(); err != nil {
			return nil, err
		}
		log.Info("auth success!")
		return wsc, nil
	}
}

// RegisterResChannel 把 websocket 收到的请求接入业务系统
//
//	Manager → WebSocket → Cli.RequestsCh → rspListener → ReqChan → 业务逻辑
func (wsc *WSClients) RegisterResChannel(requestMsg chan tmtypes.RPCRequest, stopChan chan struct{}) error {
	wsc.mtx.Lock()
	defer wsc.mtx.Unlock()
	wsc.ReqChan = requestMsg
	wsc.StopChan = stopChan
	wsc.Cli.Logger.Info("register-res-channel")
	// Subscribe the message stream coming from the Manager side.
	go wsc.rspListener()
	return nil

}

func (wsc *WSClients) SendMsg(rsp tmtypes.RPCResponse) error {
	if err := wsc.Cli.Send(context.Background(), rsp); err != nil {
		log.Error("send rsp failed!")
		return err
	}
	log.Info("send rsp success!")
	return nil
}

func (wsc *WSClients) rspListener() {
	ticker := time.NewTicker(100 * time.Second)
	for {
		select {
		case msg := <-wsc.Cli.RequestsCh:
			wsc.ReqChan <- msg
		case <-wsc.StopChan:
			wsc.Cli.Logger.Info("we are stopping channel")
			wsc.StopChan = nil
			return
		case <-ticker.C:
			wsc.Cli.Logger.Info("rsp goroutine is alive")
		}
	}
}
