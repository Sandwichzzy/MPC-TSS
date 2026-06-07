package server

import tmtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"

// IWebsocketManager 抽象出 Manager 业务真正依赖的 WebSocket 能力。
// 这样 Manager 不需要依赖具体实现，只关心：
// 1. 当前有哪些在线节点；
// 2. 如何注册一个等待响应的通道；
// 3. 如何向某个目标节点发请求。
type IWebsocketManager interface {
	AliveNodes() []string
	RegisterResChannel(id string, responseMsg chan ResponseMsg, stopChan chan struct{}) error
	SendMsg(request RequestMsg) error
}

type ResponseMsg struct {
	RpcResponse tmtypes.RPCResponse
	SourceNode  string
}

type RequestMsg struct {
	RpcRequest tmtypes.RPCRequest
	TargetNode string
}
