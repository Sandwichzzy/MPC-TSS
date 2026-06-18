package abnormal

import (
	"sync"

	"github.com/Sandwichzzy/tss/node/tsslib/messages"
)

// RoundInfo 描述一条消息所属的协议轮次及消息标识。
type RoundInfo struct {
	Index         int    // Index 是当前轮次的序号。
	RoundMsg      string // RoundMsg 是 tss-lib 输出的轮次名称。
	MsgIdentifier string // MsgIdentifier 用于区分不同请求下的同名轮次消息。
}

// RoundMgr 负责缓存每轮收到的消息，便于后续做异常分析。
type RoundMgr struct {
	storedMsg   map[string]*messages.WireMessage // storedMsg 以业务侧生成的 key 索引原始线上的消息。
	storeLocker *sync.Mutex                      // storeLocker 保护 storedMsg 的并发读写。
}

func NewTssRoundMgr() *RoundMgr {
	return &RoundMgr{
		storeLocker: &sync.Mutex{},
		storedMsg:   make(map[string]*messages.WireMessage),
	}
}

func (tr *RoundMgr) Get(key string) *messages.WireMessage {
	tr.storeLocker.Lock()
	defer tr.storeLocker.Unlock()
	ret, ok := tr.storedMsg[key]
	if !ok {
		return nil
	}
	return ret
}

func (tr *RoundMgr) Set(key string, msg *messages.WireMessage) {
	tr.storeLocker.Lock()
	defer tr.storeLocker.Unlock()
	tr.storedMsg[key] = msg
}

// GetByRound 返回指定轮次下 已经发来消息的节点 PartyID 列表。
func (tr *RoundMgr) GetByRound(roundInfo string) []string {
	var standbyNodes []string
	tr.storeLocker.Lock()
	defer tr.storeLocker.Unlock()
	for _, el := range tr.storedMsg {
		if el.RoundInfo == roundInfo {
			standbyNodes = append(standbyNodes, el.Routing.From.Id)
		}
	}
	return standbyNodes
}
