package keygen

import (
	"crypto/ecdsa"

	common2 "github.com/Sandwichzzy/tss/node/tsslib/common"
	"github.com/Sandwichzzy/tss/node/tsslib/messages"
	"github.com/Sandwichzzy/tss/node/tsslib/p2p"
	storage2 "github.com/Sandwichzzy/tss/node/tsslib/storage"
	"github.com/bnb-chain/tss-lib/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/tss"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type TssKeyGen struct {
	logger          zerolog.Logger
	localNodePubKey string
	ParticipantKeys []string
	preParams       *keygen.LocalPreParams
	tssCommonStruct *common2.TssCommon
	stopChan        chan struct{}
	localParty      *tss.PartyID
	stateManager    storage2.LocalStateManager
	//secretsEnable   bool
	//secretsManager  storage2.SecretsManager
	//shamirEnable    bool
	//shamirManager   storage2.ShamirManager
	commStopChan chan struct{}
	p2pComm      *p2p.Communication
}

func NewTssKeyGen(localP2PID string,
	conf common2.TssConfig,
	localNodePubKey string,
	broadcastChan chan *messages.BroadcastMsgChan,
	stopChan chan struct{},
	preParam *keygen.LocalPreParams,
	msgID string,
	stateManager storage2.LocalStateManager,
//secretsEnable bool,
//secretsManager storage2.SecretsManager,
//shamirEnable bool,
//shamirManager storage2.ShamirManager,
	privateKey *ecdsa.PrivateKey,
	p2pComm *p2p.Communication,
	thresHold int) *TssKeyGen {
	return &TssKeyGen{
		logger:          log.With().Str("module", "keygen").Str("msgID", msgID).Logger(),
		localNodePubKey: localNodePubKey,
		ParticipantKeys: nil,
		preParams:       preParam,
		tssCommonStruct: common2.NewTssCommon(localP2PID, broadcastChan, conf, msgID, privateKey, thresHold),
		stopChan:        stopChan,
		localParty:      nil,
		stateManager:    stateManager,
		//secretsEnable:   secretsEnable,
		//secretsManager:  secretsManager,
		//shamirEnable:    shamirEnable,
		//shamirManager:   shamirManager,
		commStopChan: make(chan struct{}),
		p2pComm:      p2pComm,
	}
}

func (tKeyGen *TssKeyGen) GetTssKeyGenChannels() chan *p2p.Message {
	return tKeyGen.tssCommonStruct.TssMsg
}
