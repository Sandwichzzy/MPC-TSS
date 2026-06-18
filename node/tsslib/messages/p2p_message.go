package messages

import (
	"fmt"

	"github.com/bnb-chain/tss-lib/tss"
	"github.com/libp2p/go-libp2p/core/peer"
)

type TSSMessageTpe uint8

const (
	TSSKeyGenMsg TSSMessageTpe = iota
	TSSKeySignMsg
	TSSTaskDone
	Unknown
)

func (msgType TSSMessageTpe) String() string {
	switch msgType {
	case TSSKeyGenMsg:
		return "TSSKeyGenMsg"
	case TSSKeySignMsg:
		return "TSSKeySignMsg"
	default:
		return "Unknown"
	}
}

type WrappedMessage struct {
	MessageType TSSMessageTpe `json:"message_type"` // MessageType 表示这条外层消息承载的是 keygen、keysign 还是任务完成通知。
	MsgID       string        `json:"message_id"`   // MsgID 是本轮 TSS 请求的唯一消息标识，用于把不同任务隔离开。
	Payload     []byte        `json:"payload"`      // Payload 是实际发送的业务载荷，通常是序列化后的 WireMessage 或任务通知。
}

type BroadcastMsgChan struct {
	WrappedMessage WrappedMessage // WrappedMessage 是已经封装好、可直接发往 P2P 层的消息体。
	PeersID        []peer.ID      // PeersID 是这条广播消息实际需要投递到的目标 peer 列表。
}

type WireMessage struct {
	Routing   *tss.MessageRouting `json:"routing"`    // Routing 保存 tss-lib 生成的路由信息，描述消息来自谁、发给谁以及是否广播。
	RoundInfo string              `json:"round_info"` // RoundInfo 标记当前消息属于哪一轮协议阶段，便于去重和异常定位。
	Message   []byte              `json:"message"`    // Message 是当前轮次的原始 TSS 消息内容。
	Sig       []byte              `json:"signature"`  // Sig 是发送方对消息内容做的签名，用于校验消息来源和完整性。
}

func (m *WireMessage) GetCacheKey() string {
	return fmt.Sprintf("%s-%s", m.Routing.From.Id, m.RoundInfo)
}

type TssTaskNotifier struct {
	TaskDone bool `json:"task_done"`
}
