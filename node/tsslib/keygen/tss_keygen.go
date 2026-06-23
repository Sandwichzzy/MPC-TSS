package keygen

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sandwichzzy/tss/node/tsslib/abnormal"
	common2 "github.com/Sandwichzzy/tss/node/tsslib/common"
	"github.com/Sandwichzzy/tss/node/tsslib/conversion"
	"github.com/Sandwichzzy/tss/node/tsslib/messages"
	"github.com/Sandwichzzy/tss/node/tsslib/p2p"
	storage2 "github.com/Sandwichzzy/tss/node/tsslib/storage"
	bcrypto "github.com/bnb-chain/tss-lib/crypto"
	"github.com/bnb-chain/tss-lib/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/tss"
	"github.com/btcsuite/btcd/btcec/v2"
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
	secretsEnable   bool
	secretsManager  storage2.SecretsManager
	shamirEnable    bool
	shamirManager   storage2.ShamirManager
	commStopChan    chan struct{}
	p2pComm         *p2p.Communication
}

func NewTssKeyGen(localP2PID string,
	conf common2.TssConfig,
	localNodePubKey string,
	broadcastChan chan *messages.BroadcastMsgChan,
	stopChan chan struct{},
	preParam *keygen.LocalPreParams,
	msgID string,
	stateManager storage2.LocalStateManager,
	secretsEnable bool,
	secretsManager storage2.SecretsManager,
	shamirEnable bool,
	shamirManager storage2.ShamirManager,
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
		secretsEnable:   secretsEnable,
		secretsManager:  secretsManager,
		shamirEnable:    shamirEnable,
		shamirManager:   shamirManager,
		commStopChan:    make(chan struct{}),
		p2pComm:         p2pComm,
	}
}

func (tKeyGen *TssKeyGen) GetTssKeyGenChannels() chan *p2p.Message {
	return tKeyGen.tssCommonStruct.TssMsg
}

func (tKeyGen *TssKeyGen) GetTssCommonStruct() *common2.TssCommon {
	return tKeyGen.tssCommonStruct
}

func (tKeyGen *TssKeyGen) GenerateNewKey(keygenReq Request) (*bcrypto.ECPoint, error) {
	// 把参与方公钥列表转换成 tss-lib 使用的 PartyID 集合，并定位当前节点自己的 PartyID。
	// conversion.GetParties 内部会先排序，因此所有节点看到的参与方顺序是一致的。
	partiesID, localPartyID, err := conversion.GetParties(tKeyGen.ParticipantKeys, tKeyGen.localNodePubKey)
	if err != nil {
		return nil, fmt.Errorf("fail to get keygen parties: %w", err)
	}

	// 先把本轮 keygen 成功后需要持久化的上下文骨架准备好。
	// 其中 LocalData 会在协议结束后补上，它是后续 keysign 必需的本地秘密份额。
	keyGenLocalStateItem := storage2.KeygenLocalState{
		ParticipantKeys: tKeyGen.ParticipantKeys,
		LocalPartyKey:   tKeyGen.localNodePubKey,
		Threshold:       tKeyGen.tssCommonStruct.GetThreshHold(),
	}
	// PeerContext 描述“这一轮有哪些参与方”。
	// Parameters 则把曲线、参与人数、门限参数以及本地 PartyID 绑定到同一份协议配置里。
	ctx := tss.NewPeerContext(partiesID)
	params := tss.NewParameters(btcec.S256(), ctx, localPartyID, len(partiesID), keygenReq.ThresHold)
	// outCh: 本地 party 产出的待发送 TSS 协议消息。
	// endCh: keygen 成功结束时返回本地保存态以及聚合公钥点。
	// errChan: 仅用于通知 LocalParty.Start() 启动失败。
	outCh := make(chan tss.Message, len(partiesID))
	endCh := make(chan keygen.LocalPartySaveData, len(partiesID))
	errChan := make(chan struct{})
	// 预计算参数里包含安全素数、Paillier 等昂贵材料；缺失时 keygen 无法启动。
	if tKeyGen.preParams == nil {
		tKeyGen.logger.Error().Err(err).Msg("error, empty pre-parameters")
		return nil, errors.New("error, empty pre-parameters")
	}
	abnormalMgr := tKeyGen.tssCommonStruct.GetAbnormalMgr()
	// 创建 tss-lib 的本地 keygen party。后续 Start() 会驱动它进入第一轮，
	// 收到的远端消息再通过 ProcessInboundMessages -> UpdateFromBytes 持续推进状态机。
	keyGenParty := keygen.NewLocalParty(params, outCh, endCh, *tKeyGen.preParams)
	// tss-lib 协议内部用的是 PartyID，但网络层真正发消息依赖 libp2p peer.ID，
	// 因此这里提前建立一份 PartyID -> peer.ID 的路由映射，后续发包和 blame 都靠它还原身份。
	partyIDMap := conversion.SetupPartyIDMap(partiesID)
	partyIDtoP2PIDMaps, err := conversion.GeneratePartyIDtoP2PIDMaps(partyIDMap)
	if err != nil {
		tKeyGen.logger.Err(err).Msgf("error in creating mapping between partyID and P2P ID")
		return nil, err
	}
	tKeyGen.tssCommonStruct.InsertPartyIDtoP2PID(partyIDtoP2PIDMaps)
	partyInfo := &abnormal.PartyInfo{
		Party:      keyGenParty,
		PartyIDMap: partyIDMap,
	}

	// 注册本轮 party 信息：
	// 1. 入站消息处理时需要拿到当前 local party 才能调用 UpdateFromBytes。
	// 2. 异常归因时需要用 PartyIDMap 把 culprit 还原成节点公钥。
	tKeyGen.tssCommonStruct.SetPartyInfo(partyInfo)
	abnormalMgr.SetPartyInfo(keyGenParty, partyIDMap)
	// P2PPeers 只保存“其他参与方”，后续广播时直接把消息扔给它们，不会把自己包含进去。
	tKeyGen.tssCommonStruct.P2PPeersLock.Lock()
	conversion.GetPeersID(tKeyGen.tssCommonStruct.GetPartyIDtoP2PID(), tKeyGen.tssCommonStruct.GetLocalPeerID())
	tKeyGen.tssCommonStruct.P2PPeersLock.Unlock()

	var keyGenWg sync.WaitGroup
	keyGenWg.Add(2)
	// 并发启动两条执行线：
	// 1. keyGenParty.Start() 让本地协议状态机开始吐出第一批 share。
	go func() {
		defer keyGenWg.Done()
		defer tKeyGen.logger.Debug().Msg(">>>>>>>>>>>>>.keyGenParty started")
		if err := keyGenParty.Start(); err != nil {
			tKeyGen.logger.Error().Err(err).Msg("fail to start keygen party")
			close(errChan)
		}
	}()
	// 2. ProcessInboundMessages 持续消费网络层送来的远端 share，并交回本地状态机处理。
	go tKeyGen.tssCommonStruct.ProcessInboundMessages(tKeyGen.commStopChan, &keyGenWg)
	// 这里开始阻塞等待本轮 keygen 的最终结果。
	// processKeyGen 内部会同时处理发消息、收结果、超时、外部停止等事件。
	r, err := tKeyGen.processKeyGen(errChan, outCh, endCh, keyGenLocalStateItem)
	if err != nil {
		close(tKeyGen.commStopChan)
		return nil, fmt.Errorf("fail to process keygen party: %w", err)
	}
	// 协议主流程结束后，不立刻停掉入站协程，而是给“任务完成”通知留一个短暂传播窗口。
	// 这样其他节点还有机会把 TSSTaskDone 发过来，避免本端过早退出收包循环。
	select {
	case <-time.After(time.Second * 5):
		close(tKeyGen.commStopChan)

	case <-tKeyGen.tssCommonStruct.GetTaskDone():
		close(tKeyGen.commStopChan)
	}
	keyGenWg.Wait()
	return r, err
}

func (tKeyGen *TssKeyGen) processKeyGen(
	errChan chan struct{},
	outCh <-chan tss.Message,
	endCh <-chan keygen.LocalPartySaveData,
	keyGenLocalStateItem storage2.KeygenLocalState) (*bcrypto.ECPoint, error) {

	defer tKeyGen.logger.Debug().Msg("finished keygen process")
	tKeyGen.logger.Debug().Msg("start to read messages from local party")

	tssConf := tKeyGen.tssCommonStruct.GetConf()
	abnormalMgr := tKeyGen.tssCommonStruct.GetAbnormalMgr()

	// 这是单轮 keygen 的主事件循环。
	// 本地状态机的推进，最终都会折叠成下面几个事件中的某一个。
	for {
		select {
		case <-errChan:
			// 这个分支通常表示 keyGenParty.Start() 自身启动失败，
			tKeyGen.logger.Error().Msg("key gen failed")
			return nil, errors.New("error channel closed fail to start local party")
		case <-tKeyGen.stopChan:
			// 外部整个 TSS 服务正在退出，本轮协议直接中止。
			return nil, errors.New("received exit signal")
		case <-time.After(tssConf.KeyGenTimeout):
			// 单轮 keygen 在配置的超时时间内没有完成。
			// 这里会尽量保留异常管理器里的上下文，方便上层做超时归因。
			tKeyGen.logger.Error().Msgf("fail to generate message with %s", tssConf.KeyGenTimeout.String())
			lastMsg := abnormalMgr.GetLastMsg()
			failReason := abnormalMgr.GetAbnormal().FailReason
			if failReason == "" {
				failReason = abnormal.TssTimeout
			}
			// 如果连最后一条本地消息都没有，说明可能在真正发出 share 之前就卡住了。
			if lastMsg == nil {
				tKeyGen.logger.Error().Msg("fail to start the keygen, the last produced message of this node is none")
				return nil, errors.New("timeout before shared message is generated")
			}
			return nil, abnormal.ErrTssTimeOut
		case msg := <-outCh:
			// outCh 上来的都是“本节点已经计算好、准备发给别人的 share”。
			// ProcessOutCh 会负责：
			// 1. 把 share 序列化成 wire bytes
			// 2. 用节点私钥签名
			// 3. 按广播/单播封装后投递给 P2P 层
			abnormalMgr.SetLastMsg(msg)
			err := tKeyGen.tssCommonStruct.ProcessOutCh(msg, messages.TSSKeyGenMsg)
			if err != nil {
				tKeyGen.logger.Error().Err(err).Msg("fail to process the message")
				return nil, err
			}
		case msg := <-endCh:
			// 收到 endCh 说明本节点的 keygen 已经完成。
			// msg 里既有聚合公钥点，也有后续 keysign 必需的本地保存态 LocalPartySaveData。
			tKeyGen.logger.Debug().Msgf("keygen finished successfully: %s", msg.ECDSAPub.Y().String())
			// 通知其他节点“我已经结束了，可以安全收尾”
			err := tKeyGen.tssCommonStruct.NotifyTaskDone()
			if err != nil {
				tKeyGen.logger.Error().Err(err).Msg("fail to broadcast the keySign done")
			}
			// 把聚合公钥点转换成上层更容易消费的几种形式：
			// 压缩公钥字符串、EVM 地址、未压缩公钥字节。
			pubKey, addr, _, err := conversion.GetTssPubKey(msg.ECDSAPub)
			if err != nil {
				return nil, fmt.Errorf("fail to get thorchain pubkey: %w", err)
			}
			tKeyGen.logger.Debug().Msgf("tss pub key is (%s),address is (%s).", pubKey, addr)
			// LocalData 是 keygen 阶段产出的本地秘密份额；
			// 没有它，后续任何使用该聚合公钥的 keysign 都无法进行。
			keyGenLocalStateItem.LocalData = msg
			keyGenLocalStateItem.PubKey = pubKey

			// 三种存储路径 目前先支持本地：
			// 1. Shamir 分片存储
			// 2. SecretsManager 外部秘密管理
			// 3. 本地磁盘状态文件
			if tKeyGen.shamirEnable {
				if err := tKeyGen.shamirManager.PutKeyFile(keyGenLocalStateItem); err != nil {
					return nil, fmt.Errorf("fail to put keygen result with shamir manager : %w", err)
				}
			} else if tKeyGen.secretsEnable {
				if err := tKeyGen.secretsManager.PutKeyFile(keyGenLocalStateItem); err != nil {
					return nil, fmt.Errorf("fail to put keygen result to secrets manager map : %w", err)
				}

				if err := tKeyGen.secretsManager.Save(); err != nil {
					return nil, fmt.Errorf("fail to put keygen result to secrets manager :%w", err)
				}

			} else {
				if err := tKeyGen.stateManager.SaveLocalState(keyGenLocalStateItem); err != nil {
					return nil, fmt.Errorf("fail to save keygen result to storage: %w", err)
				}
			}
			// 地址簿不是密钥材料的一部分，但保存它可以帮助节点下次启动时更快恢复 P2P 邻居信息。
			address := tKeyGen.p2pComm.ExportPeerAddress()
			if err := tKeyGen.stateManager.SaveAddressBook(address); err != nil {
				tKeyGen.logger.Error().Err(err).Msg("fail to save the peer addresses")
			}
			return msg.ECDSAPub, nil
		}

	}

}
