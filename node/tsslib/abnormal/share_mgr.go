package abnormal

import "sync"

// ShareMgr 用于记录某个 share 请求是否已经发起或处理过。
//
//	收到的协议消息份额是否重复、是否有问题
type ShareMgr struct {
	requested map[string]bool // requested 以 key 标记请求状态，true 表示该请求已存在。
	reqLocker *sync.Mutex
}

func NewTssShareMgr() *ShareMgr {
	return &ShareMgr{
		reqLocker: &sync.Mutex{},
		requested: make(map[string]bool),
	}
}

func (sm *ShareMgr) Set(key string) {
	sm.reqLocker.Lock()
	defer sm.reqLocker.Unlock()
	sm.requested[key] = true
}

func (sm *ShareMgr) QueryAndDelete(key string) bool {
	sm.reqLocker.Lock()
	defer sm.reqLocker.Unlock()
	ret := sm.requested[key]
	delete(sm.requested, key)
	return ret
}
