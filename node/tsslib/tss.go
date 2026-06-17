package tsslib

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	tssconfig "github.com/Sandwichzzy/tss/config"
	common2 "github.com/Sandwichzzy/tss/node/tsslib/common"
	"github.com/Sandwichzzy/tss/node/tsslib/conversion"
	"github.com/Sandwichzzy/tss/node/tsslib/monitor"
	storage2 "github.com/Sandwichzzy/tss/node/tsslib/storage"
	"github.com/Sandwichzzy/tss/node/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const poolPublicKey = 66

type TssServer struct {
	conf                common2.TssConfig
	logger              zerolog.Logger
	p2pCommunication    *p2p2.Communication
	localNodePubKey     string
	participants        map[string][]string
	preParams           *bkeygen.LocalPreParams
	tssKeyGenLocker     *sync.Mutex
	stopChan            chan struct{}
	stateManager        storage2.LocalStateManager
	secretsManager      storage2.SecretsManager
	shamirManager       storage2.ShamirManager
	privateKey          *ecdsa.PrivateKey
	tssMetrics          *monitor.Metric
	secretsEnable       bool
	shamirEnable        bool
	tssGroupMemberStore types.TssMemberStore
}

func NewTss(
	cmdBootstrapPeers string,
	waitFullConnected bool,
	p2pPort int,
	priKey *ecdsa.PrivateKey,
	storageFolder string,
	conf common2.TssConfig,
	preParamsFile string,
	externalIP string,
	secretsEnable bool,
	secretId string,
	shamirConfig tssconfig.ShamirConfig,
	store types.TssMemberStore,
) (*TssServer, error) {
	pubkey := crypto.CompressPubkey(&priKey.PublicKey)
	pubkeyHex := hex.EncodeToString(pubkey)
	log.Info().Msgf("pub key is (%s) \n", pubkeyHex)

	peerId, err := conversion.GetPeerIDFromPubKey(pubkeyHex)
	if err != nil {
		log.Error().Err(err).Msg("ERROR: fail to get peer id by pub key")
		return nil, errors.New("ERROR: fail to get peer id by pub key")
	}
	log.Info().Msgf("peer id is (%s) \n", peerId)

	//stateManager, err := storage2.NewFileStateMgr(storageFolder)
	//if err != nil {
	//	return nil, errors.New("fail to create file state manager")
	//}
	//var secretsManager *storage2.SecretsMgr
	//var shamirManager *storage2.ShamirMgr
	//if shamirConfig.Enable {
	//	shamirManager, err = storage2.NewShamirMgr(shamirConfig)
	//	if err != nil {
	//		log.Error().Err(err).Msgf("fail to create shamir manager :%v", err)
	//		return nil, errors.New("fail to create shamir manager")
	//	}
	//} else if secretsEnable {
	//	secretsManager, err = storage2.NewSecretsMgr(secretId)
	//	if err != nil {
	//		log.Error().Err(err).Msgf("fail to create secrets manager :%v", err)
	//		return nil, errors.New("fail to create secrets manager")
	//	}
	//}
	//cmdBootstrapPeerS := strings.Split(cmdBootstrapPeers, ",")
	//var bootstrapPeers p2p2.AddrList
	//savedPeers, err := stateManager.RetrieveP2PAddresses()
	//if err == nil {
	//	bootstrapPeers = savedPeers
	//}
	//for _, peerAdd := range cmdBootstrapPeerS {
	//	if peerAdd != "" {
	//		if err = bootstrapPeers.Set(strings.TrimSpace(peerAdd)); err != nil {
	//			return nil, err
	//		}
	//	}
	//}
}
