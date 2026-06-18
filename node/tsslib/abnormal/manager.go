package abnormal

import (
	"sync"

	"github.com/bnb-chain/tss-lib/tss"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// PartyID 是 TSS 协议层给每个参与节点分配的“协议身份”
// 它不是 libp2p 的 peer.ID，也不是节点公钥本身，而是 tss-lib 在协议内部识别参与方用的对象。
//- 网络层通信用的是 peer.ID
//- 协议层路由和 UpdateFromBytes() 用的是 PartyID
//- 业务层常常又用节点公钥来标识参与者

type Manager struct {
	logger            zerolog.Logger
	Abnormal          *Abnormal            // Abnormal 保存当前已经归因出来的异常信息。
	lastUnicastPeer   map[string][]peer.ID // lastUnicastPeer 记录每个轮次最后一次单播涉及到的远端节点。
	shareMgr          *ShareMgr            // shareMgr 用于跟踪 share 请求是否已经被处理过。
	roundMgr          *RoundMgr            // roundMgr 用于按轮次缓存和检索收到的消息。
	partyInfo         *PartyInfo           // partyInfo 保存当前本地 tss party 及 PartyID 映射。
	PartyIDtoP2PID    map[string]peer.ID   // PartyIDtoP2PID 维护 tsslib 协议层 PartyID 到 libp2p peer.ID 的映射。
	lastMsgLocker     sync.RWMutex
	lastMsg           tss.Message            // lastMsg 记录最近一次处理的 tss 消息，便于异常定位。
	acceptedShares    map[RoundInfo][]string // acceptedShares 记录每个轮次已经接受过 share 的节点，避免重复处理。
	acceptShareLocker sync.Mutex
	localPartyID      string // localPartyID 是当前节点在本轮协议中的 PartyID。
}

func NewAbnormalManager() *Manager {
	abnormal := NewAbnormal("", nil)
	return &Manager{
		logger:            log.With().Str("module", "Abnormal_manager").Logger(),
		partyInfo:         nil,
		PartyIDtoP2PID:    make(map[string]peer.ID),
		lastUnicastPeer:   make(map[string][]peer.ID),
		shareMgr:          NewTssShareMgr(),
		roundMgr:          NewTssRoundMgr(),
		Abnormal:          abnormal,
		lastMsgLocker:     sync.RWMutex{},
		acceptedShares:    make(map[RoundInfo][]string),
		acceptShareLocker: sync.Mutex{},
	}
}

func (m *Manager) GetAbnormal() *Abnormal {
	return m.Abnormal
}

func (m *Manager) GetShareMgr() *ShareMgr {
	return m.shareMgr
}

func (m *Manager) GetRoundMgr() *RoundMgr {
	return m.roundMgr
}

// UpdateAcceptShare 记录某个轮次已接受来自指定 PartyID 的 share。
func (m *Manager) UpdateAcceptShare(round RoundInfo, id string) {
	m.acceptShareLocker.Lock()
	defer m.acceptShareLocker.Unlock()
	partyList, ok := m.acceptedShares[round]
	if !ok {
		partyList := []string{id}
		m.acceptedShares[round] = partyList
		return
	}
	partyList = append(partyList, id)
	m.acceptedShares[round] = partyList
}

// CheckMsgDuplication 判断某个轮次是否已接收过来自同一 PartyID 的 share。
func (m *Manager) CheckMsgDuplication(round RoundInfo, id string) bool {
	m.acceptShareLocker.Lock()
	defer m.acceptShareLocker.Unlock()
	partyList, ok := m.acceptedShares[round]
	if ok {
		for _, el := range partyList {
			if el == id {
				return true
			}
		}
	}
	return false
}

// SetLastMsg 保存最近一次处理的 tss 消息。
func (m *Manager) SetLastMsg(lastMsg tss.Message) {
	m.lastMsgLocker.Lock()
	defer m.lastMsgLocker.Unlock()
	m.lastMsg = lastMsg
}

// GetLastMsg 读取最近一次处理的 tss 消息。
func (m *Manager) GetLastMsg() tss.Message {
	m.lastMsgLocker.RLock()
	defer m.lastMsgLocker.RUnlock()
	return m.lastMsg
}

// SetPartyInfo 注册当前本地 party 及其 PartyID 映射关系。
func (m *Manager) SetPartyInfo(party tss.Party, partyIDMap map[string]*tss.PartyID) {
	partyInfo := &PartyInfo{
		Party:      party,
		PartyIDMap: partyIDMap,
	}
	m.partyInfo = partyInfo
	m.localPartyID = party.PartyID().Id
}

// SetLastUnicastPeer 记录某个轮次最近一次单播涉及到的目标节点。
func (m *Manager) SetLastUnicastPeer(peerID peer.ID, roundInfo string) {
	m.lastMsgLocker.Lock()
	defer m.lastMsgLocker.Unlock()
	l, ok := m.lastUnicastPeer[roundInfo]
	if !ok {
		peerList := []peer.ID{peerID}
		m.lastUnicastPeer[roundInfo] = peerList
	} else {
		l = append(l, peerID)
		m.lastUnicastPeer[roundInfo] = l
	}
}

// GetAbnormalNodePubKeys 提取当前异常节点列表中的全部公钥。
func (m *Manager) GetAbnormalNodePubKeys() []string {
	var pubkeys []string
	for _, node := range m.GetAbnormal().Nodes {
		pubkeys = append(pubkeys, node.Pubkey)
	}
	return pubkeys
}

// TssCulpritsNodes 在 share 校验失败场景下 返回被归因为异常的节点公钥列表。
func (m *Manager) TssCulpritsNodes() []string {
	var pubkeys []string
	if m.GetAbnormal().FailReason == TssBrokenMsg {
		for _, node := range m.GetAbnormal().Nodes {
			pubkeys = append(pubkeys, node.Pubkey)
		}
	}
	return pubkeys
}
