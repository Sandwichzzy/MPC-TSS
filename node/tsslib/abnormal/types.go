package abnormal

import (
	"errors"
	"sync"

	"github.com/bnb-chain/tss-lib/tss"
)

const (
	HashCheckFail       = "hash check failed"
	TssTimeout          = "Tss timeout"
	TssSyncFail         = "signers fail to sync before keygen/keysign"
	TssBrokenMsg        = "tss share verification failed"
	InternalError       = "fail to start the join party "
	GenerateNewKeyError = "fail to generate new key"
	SignatureError      = "fail to signature message"
)

var (
	ErrHashFromOwner     = errors.New(" hash sent from data owner")
	ErrNotEnoughPeer     = errors.New("not enough nodes to evaluate hash")
	ErrNotMajority       = errors.New("message we received does not match the majority")
	ErrTssTimeOut        = errors.New("error Tss Timeout")
	ErrHashCheck         = errors.New("error in processing hash check")
	ErrHashInconsistency = errors.New("fail to agree on the hash value")
)

// Abnormal 表示一轮 TSS 协议执行中当前汇总出来的异常信息。
type Abnormal struct {
	FailReason   string  `json:"fail_reason"`              // FailReason 描述当前异常的归因结果。
	IsUnicast    bool    `json:"is_broadcast"`             // IsUnicast 标记异常是否发生在单播消息链路中。
	Nodes        []*Node `json:"abnormal_peers,omitempty"` // Nodes 保存被认为与本次异常相关的节点列表。
	AbnormalLock sync.RWMutex
}

// Node 记录一个被标记为异常的节点以及相关证据数据。
type Node struct {
	Pubkey    string `json:"pubkey"`              // Pubkey 是异常节点对应的公钥标识。
	Data      []byte `json:"data"`                // Data 保存用于异常分析的原始数据片段。
	Signature []byte `json:"signature,omitempty"` // Signature 是该数据对应的签名，可能为空。
}

// PartyInfo 保存本地运行中的 party 及 PartyID 查找表。
type PartyInfo struct {
	Party      tss.Party               // Party 是当前节点持有的 tss-lib 协议实例。
	PartyIDMap map[string]*tss.PartyID // PartyIDMap 用于按公钥或业务标识反查对应的 PartyID。
}
