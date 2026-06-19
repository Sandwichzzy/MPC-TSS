package common

import (
	"sync"

	"github.com/Sandwichzzy/tss/node/tsslib/messages"
)

// LocalCacheItem 想解决的问题是：
//
// - 把“某个发送者在某一 round 的广播消息”先挂起来
// - 记录不同节点报告的 hash
// - 后面如果足够多节点都说自己收到的是同一个 hash，就认为这条广播是一致的
// - 如果 hash 对不上，就能判定存在 equivocation / 消息分叉
type LocalCacheItem struct {
	Msg           *messages.WireMessage
	Hash          string
	lock          *sync.Mutex
	ConfirmedList map[string]string
}

func NewLocalCacheItem(msg *messages.WireMessage, hash string) *LocalCacheItem {
	return &LocalCacheItem{
		Msg:           msg,
		Hash:          hash,
		lock:          &sync.Mutex{},
		ConfirmedList: make(map[string]string),
	}
}

// UpdateConfirmList add the given party's hash into the confirm list
func (l *LocalCacheItem) UpdateConfirmList(P2PID, hash string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.ConfirmedList[P2PID] = hash
}

// TotalConfirmParty number of parties that already confirmed their hash
func (l *LocalCacheItem) TotalConfirmParty() int {
	l.lock.Lock()
	defer l.lock.Unlock()
	return len(l.ConfirmedList)
}

func (l *LocalCacheItem) GetPeers() []string {
	peers := make([]string, 0, len(l.ConfirmedList))
	l.lock.Lock()
	defer l.lock.Unlock()
	for peer := range l.ConfirmedList {
		peers = append(peers, peer)
	}
	return peers
}
