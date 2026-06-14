package manager

import (
	tss "github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/manager/types"
	"github.com/Sandwichzzy/tss/ws/server"
	"github.com/ethereum/go-ethereum/log"
)

// agreement 的核心流程是：
//
// 1. 创建 `respChan`
// 2. 用 `ASK_<requestId>` 调用 `RegisterResChannel(...)`
// 3. 把原始请求 JSON 序列化
// 4. 调用 `askNodes(...)` 给所有候选节点发送 `askMessageHash`
// 5. 等待各节点通过 `respChan` 返回 `AskResponse`
// 6. 根据返回结果整理出：
// - `Approvers`
// - `UnApprovers`
func (m *Manager) agreement(ctx types.Context, request interface{}, method tss.Method) (types.Context, error) {
	respChan := make(chan server.ResponseMsg)
	stopChan := make(chan struct{})

	if err := m.wsServer.RegisterResChannel("ASK_"+ctx.RequestId(), respChan, stopChan); err != nil {
		log.Error("failed to register response channel", "step", "agreement", "err", err)
		return types.Context{}, err
	}
}
