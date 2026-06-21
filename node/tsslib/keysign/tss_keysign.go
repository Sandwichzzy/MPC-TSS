package keysign

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
	"github.com/Sandwichzzy/tss/node/tsslib/storage"
	tsscommon "github.com/bnb-chain/tss-lib/common"
	"github.com/bnb-chain/tss-lib/ecdsa/signing"
	"github.com/bnb-chain/tss-lib/tss"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type TssKeySign struct {
	logger          zerolog.Logger
	tssCommonStruct *common2.TssCommon
	stopChan        chan struct{} // channel to indicate whether we should stop
	localParties    []*tss.PartyID
	commStopChan    chan struct{}
	p2pComm         *p2p.Communication
	stateManager    storage.LocalStateManager
}

func NewTssKeySign(
	localP2PID string,
	conf common2.TssConfig,
	broadcastChan chan *messages.BroadcastMsgChan,
	stopChan chan struct{},
	msgID string,
	privKey *ecdsa.PrivateKey,
	p2pComm *p2p.Communication,
	stateManager storage.LocalStateManager,
	thresHold int) *TssKeySign {
	logItems := []string{"keySign", msgID}
	return &TssKeySign{
		logger:          log.With().Strs("module", logItems).Logger(),
		tssCommonStruct: common2.NewTssCommon(localP2PID, broadcastChan, conf, msgID, privKey, thresHold),
		stopChan:        stopChan,
		localParties:    make([]*tss.PartyID, 0),
		commStopChan:    make(chan struct{}),
		p2pComm:         p2pComm,
		stateManager:    stateManager,
	}
}

func (tKeySign *TssKeySign) GetTssKeySignChannels() chan *p2p.Message {
	return tKeySign.tssCommonStruct.TssMsg
}

func (tKeySign *TssKeySign) GetTssCommonStruct() *common2.TssCommon {
	return tKeySign.tssCommonStruct
}

// SignMessage 使用 keygen 阶段落盘的本地秘密份额，对 msgToSign 执行一轮门限签名。
func (tKeySign *TssKeySign) SignMessage(msgToSign []byte, localStateItem storage.KeygenLocalState, parties []string) (*tsscommon.SignatureData, error) {
	// 先把本轮实际参与签名的节点公钥列表转换成 PartyID，并找出本节点自己的 PartyID。
	// 这里的 parties 通常是上层选出的在线 signer 子集，而不是所有历史参与者。
	partiesID, localPartyID, err := conversion.GetParties(parties, localStateItem.LocalPartyKey)
	if err != nil {
		return nil, fmt.Errorf("fail to form key sign party: %w", err)
	}

	outCh := make(chan tss.Message, 2*len(partiesID))           //本地 party 产出的待发送签名 share。
	endCh := make(chan tsscommon.SignatureData, len(partiesID)) // 协议结束时产出的最终签名结果。
	errCh := make(chan struct{})                                // errCh: 仅用于通知 LocalParty.Start() 启动失败。

	// tss-lib 的签名接口接收的是大整数 m，而不是原始 []byte。
	// 这里按 secp256k1 的规则把摘要转成 ECDSA 签名时使用的整数。
	m := common2.MsgToHashInt(msgToSign)

	// moniker 被塞进 local party 的身份信息里，后续出站消息会把它带到 MsgIdentifier。
	// 对 keysign 来说，这个标识取消息摘要的整数形式，可以避免同轮不同消息混淆。
	moniker := m.String()
	// 重新创建 PeerContext，告诉 tss-lib 这一轮到底有哪些 signer 在场。
	ctx := tss.NewPeerContext(partiesID)

	tKeySign.logger.Info().Msgf("message: (%s) keysign parties: %+v", m.String(), parties)
	localPartyID.Moniker = moniker
	tKeySign.localParties = nil

	// keygen 时保存下来的 threshold 会继续用于本轮 keysign，
	// 这样签名门限和生成该公钥时的门限保持一致。
	params := tss.NewParameters(btcec.S256(), ctx, localPartyID, len(partiesID), tKeySign.GetTssCommonStruct().GetThreshHold())

	// localStateItem.LocalData 就是 keygen 阶段保存的本地秘密份额；
	// 没有它，本节点无法针对该 PoolPubKey 参与签名。
	keySignParty := signing.NewLocalParty(m, params, localStateItem.LocalData, outCh, endCh)

	abnormalMgr := tKeySign.tssCommonStruct.GetAbnormalMgr()
	// 同 keygen 一样，网络层发的是 peer.ID，协议层认的是 PartyID，
	// 所以需要一份双向可还原的映射供路由和 blame 复用。
	partyIDMap := conversion.SetupPartyIDMap(partiesID)
	partyIDtoP2PIDMap, err := conversion.GeneratePartyIDtoP2PIDMaps(partyIDMap)
	if err != nil {
		tKeySign.logger.Err(err).Msgf("error in creating mapping between partyID and P2P ID")
		return nil, err
	}
	tKeySign.tssCommonStruct.InsertPartyIDtoP2PID(partyIDtoP2PIDMap)
	abnormalMgr.PartyIDtoP2PID = partyIDtoP2PIDMap

	// 注册 party 信息后，公共入站处理逻辑才知道把收到的 share 喂给哪一个 local party。
	tKeySign.tssCommonStruct.SetPartyInfo(&abnormal.PartyInfo{
		Party:      keySignParty,
		PartyIDMap: partyIDMap,
	})
	abnormalMgr.SetPartyInfo(keySignParty, partyIDMap)

	// P2PPeers 维护除自己外的所有 signer，供广播/单播封装阶段直接使用。
	tKeySign.tssCommonStruct.P2PPeersLock.Lock()
	tKeySign.tssCommonStruct.P2PPeers = conversion.GetPeersID(tKeySign.tssCommonStruct.GetPartyIDtoP2PID(), tKeySign.tssCommonStruct.GetLocalPeerID())
	tKeySign.tssCommonStruct.P2PPeersLock.Unlock()

	var keySignWg sync.WaitGroup
	keySignWg.Add(2)
	// 同时启动：
	// 1. 本地 keySign 状态机
	go func() {
		defer keySignWg.Done()
		defer tKeySign.logger.Info().Msgf("local party(%s) %s is ready", keySignParty.PartyID().Id, keySignParty.PartyID().Moniker)
		if err := keySignParty.Start(); err != nil {
			tKeySign.logger.Error().Err(err).Msg("fail to start key sign party")
			close(errCh)
		}
	}()
	// 2. 入站消息消费循环
	go tKeySign.tssCommonStruct.ProcessInboundMessages(tKeySign.commStopChan, &keySignWg)
	// processKeySign 会负责把本地生成的 share 发出去，同时等待最终签名结果或超时/失败。
	result, err := tKeySign.processKeySign(errCh, outCh, endCh)
	if err != nil {
		close(tKeySign.commStopChan)
		return nil, fmt.Errorf("fail to process key sign: %w", err)
	}
	// keysign 结束后同样给“任务完成”通知留一个很短的缓冲窗口。 这里比 keygen 更短，因为签名结果已经拿到，上层通常只关心尽快返回。
	select {
	case <-time.After(time.Second * 1):
		close(tKeySign.commStopChan)
	case <-tKeySign.tssCommonStruct.GetTaskDone():
		close(tKeySign.commStopChan)
	}
	keySignWg.Wait()

	tKeySign.logger.Info().Msgf("%s successfully sign the message", tKeySign.p2pComm.GetHost().ID().String())
	return &result, nil
}

func (tKeySign *TssKeySign) processKeySign(errChan chan struct{}, outCh <-chan tss.Message, endCh <-chan tsscommon.SignatureData) (tsscommon.SignatureData, error) {
	defer tKeySign.logger.Debug().Msg("key sign finished")
	tKeySign.logger.Debug().Msg("start to read messages from local party")

	var emptySignatureData tsscommon.SignatureData
	tssConf := tKeySign.tssCommonStruct.GetConf()

	// 这是单轮 keysign 的事件循环
	for {
		select {
		case <-errChan:
			tKeySign.logger.Error().Msg("key sign failed")
			return emptySignatureData, errors.New("error channel closed fail to start local party")
		case <-tKeySign.stopChan:
			return emptySignatureData, errors.New("received exit signal")
		case <-time.After(tssConf.KeySignTimeout):
			// 在允许的签名窗口内没有拿到最终结果。
			tKeySign.logger.Error().Msgf("fail to sign message with %s", tssConf.KeySignTimeout.String())
			return emptySignatureData, abnormal.ErrTssTimeOut
		case msg := <-outCh:
			// 每条 outCh 消息都是本节点当前轮次生成的签名 share。
			// ProcessOutCh 会把它打包、签名并发给对应的远端 signer。
			tKeySign.logger.Debug().Msgf(">>>>>>>>>>key sign msg: %s", msg.String())
			tKeySign.tssCommonStruct.GetAbnormalMgr().SetLastMsg(msg)
			err := tKeySign.tssCommonStruct.ProcessOutCh(msg, messages.TSSKeySignMsg)
			if err != nil {
				return emptySignatureData, err
			}

		case msg := <-endCh:
			// 收到 endCh 说明门限签名已经完成。
			// msg 是 tss-lib 原生的 SignatureData，包含 R/S/recovery 等字段。
			tKeySign.logger.Debug().Msg("we have done the key sign")
			// 广播控制面上的完成通知，让其他节点知道本轮协议可以收尾。
			err := tKeySign.tssCommonStruct.NotifyTaskDone()
			if err != nil {
				tKeySign.logger.Error().Err(err).Msg("fail to broadcast the keysign done")
			}
			// 顺手导出最新地址簿，便于后续 P2P 连接恢复。
			address := tKeySign.p2pComm.ExportPeerAddress()
			if err := tKeySign.stateManager.SaveAddressBook(address); err != nil {
				tKeySign.logger.Error().Err(err).Msg("fail to save the peer addresses")
			}
			return msg, nil

		}
	}

}
