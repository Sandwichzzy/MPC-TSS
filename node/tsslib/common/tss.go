package common

import (
	"crypto/ecdsa"
	"sync"

	"github.com/Sandwichzzy/tss/node/tsslib/messages"
	"github.com/Sandwichzzy/tss/node/tsslib/p2p"
	"github.com/bnb-chain/tss-lib/tss"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/rs/zerolog"
)

type TssCommon struct {
	conf   TssConfig
	logger zerolog.Logger

	partyLock sync.RWMutex
	partyInfo *abnormal2.PartyInfo

	partyIDtoP2PIDMap      *sync.Map // map[string]peer.ID
	unConfirmedMessagesMap *sync.Map // map[string]*LocalCacheItem

	localPeerID      string
	broadcastChannel chan *messages.BroadcastMsgChan
	TssMsg           chan *p2p.Message

	P2PPeersLock sync.RWMutex
	P2PPeers     []peer.ID

	msgID         string
	privateKey    *ecdsa.PrivateKey
	taskDone      chan struct{}
	abnormalMgr   *abnormal2.Manager
	finishedPeers map[string]bool

	culpritsLock sync.RWMutex
	culprits     []*tss.PartyID

	cachedWireBroadcastMsgLists *sync.Map
	cachedWireUnicastMsgLists   *sync.Map
	threshHold                  int
}
