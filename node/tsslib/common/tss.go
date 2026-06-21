package common

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Sandwichzzy/tss/node/tsslib/abnormal"
	"github.com/Sandwichzzy/tss/node/tsslib/conversion"
	"github.com/Sandwichzzy/tss/node/tsslib/messages"
	"github.com/Sandwichzzy/tss/node/tsslib/p2p"
	"github.com/bnb-chain/tss-lib/tss"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TssCommon 它不是具体的 keygen/keysign 算法实现，但负责：
//
// - 把 local party 的 `outCh` 消息转成 p2p 可发的结构
// - 把 libp2p 收到的消息验签、解包、更新回 local party
// - 管理 task done 通知
// - 管理 partyID / peerID 映射
// - 记录 abnormal 信息
type TssCommon struct {
	conf                        TssConfig
	logger                      zerolog.Logger
	partyLock                   sync.RWMutex
	partyInfo                   *abnormal.PartyInfo // partyInfo 保存当前本地 TSS 参与方对象，以及 partyID 到参与方信息的映射。
	partyIDtoP2PIDMap           *sync.Map           // partyIDtoP2PIDMap 维护 TSS PartyID 到 libp2p PeerID 的映射关系。
	unConfirmedMessagesMap      *sync.Map           // 缓存尚未完成确认的广播消息。
	localPeerID                 string
	broadcastChannel            chan *messages.BroadcastMsgChan // broadcastChannel 用于把封装后的 TSS 消息转交给 P2P 层发送。
	TssMsg                      chan *p2p.Message               // TssMsg 是接收来自 P2P 层入站消息的通道。
	P2PPeersLock                sync.RWMutex
	P2PPeers                    []peer.ID
	msgID                       string
	privateKey                  *ecdsa.PrivateKey
	taskDone                    chan struct{} // taskDone 在所有对端都声明任务完成后关闭，用于通知上层流程退出等待。
	abnormalMgr                 *abnormal.Manager
	finishedPeers               map[string]bool
	culpritsLock                sync.RWMutex
	culprits                    []*tss.PartyID // culprits 记录已经被识别出的恶意参与方，后续其消息将被拒绝。
	cachedWireBroadcastMsgLists *sync.Map      //用于缓存同一轮待广播发送消息，
	cachedWireUnicastMsgLists   *sync.Map      //用于缓存同一轮待发送消息，
	threshHold                  int
}

func NewTssCommon(peerID string, broadcastChannel chan *messages.BroadcastMsgChan, conf TssConfig, msgID string, privKey *ecdsa.PrivateKey, thresHold int) *TssCommon {
	return &TssCommon{
		conf:                        conf,
		logger:                      log.With().Str("module", "tsscommon").Logger(),
		partyLock:                   sync.RWMutex{},
		partyInfo:                   nil,
		partyIDtoP2PIDMap:           &sync.Map{},
		unConfirmedMessagesMap:      &sync.Map{},
		broadcastChannel:            broadcastChannel,
		TssMsg:                      make(chan *p2p.Message),
		P2PPeersLock:                sync.RWMutex{},
		P2PPeers:                    nil,
		msgID:                       msgID,
		localPeerID:                 peerID,
		privateKey:                  privKey,
		taskDone:                    make(chan struct{}),
		abnormalMgr:                 abnormal.NewAbnormalManager(),
		finishedPeers:               make(map[string]bool),
		culpritsLock:                sync.RWMutex{},
		cachedWireBroadcastMsgLists: &sync.Map{},
		cachedWireUnicastMsgLists:   &sync.Map{},
		threshHold:                  thresHold,
	}
}

// BulkWireMsg 是经过二次封装的 TSS 消息，携带原始 wire bytes、消息标识和路由信息。
type BulkWireMsg struct {
	WiredBulkMsg  []byte
	MsgIdentifier string
	Routing       *tss.MessageRouting
}

// NewBulkWireMsg 创建一个可直接用于网络发送的批量消息对象。
func NewBulkWireMsg(msg []byte, id string, r *tss.MessageRouting) BulkWireMsg {
	return BulkWireMsg{
		WiredBulkMsg:  msg,
		MsgIdentifier: id,
		Routing:       r,
	}
}

// tssJob 表示一个待投递到本地 TSS party 的消息处理任务。
type tssJob struct {
	wireBytes     []byte
	msgIdentifier string
	partyID       *tss.PartyID
	isBroadcast   bool
	localParty    tss.Party
}

func newJob(party tss.Party, wireBytes []byte, msgIdentifier string, from *tss.PartyID, isBroadcast bool) *tssJob {
	return &tssJob{
		wireBytes:     wireBytes,
		msgIdentifier: msgIdentifier,
		partyID:       from,
		isBroadcast:   isBroadcast,
		localParty:    party,
	}
}

// doTssJob 顺序消费任务通道中的消息，并调用本地 TSS party 完成状态推进。
func (t *TssCommon) doTssJob(tssJobChan chan *tssJob, jobWg *sync.WaitGroup) {
	defer func() {
		jobWg.Done()
	}()

	for tssjob := range tssJobChan {
		party := tssjob.localParty
		wireBytes := tssjob.wireBytes
		partyID := tssjob.partyID
		isBroadcast := tssjob.isBroadcast

		// 先解析消息所属轮次，便于做重复检测与异常归因。
		round, err := GetMsgRound(wireBytes, partyID, isBroadcast)
		if err != nil {
			t.logger.Error().Err(err).Msg("broken tss share")
			continue
		}
		round.MsgIdentifier = tssjob.msgIdentifier

		// 把收到的消息应用到本地 party；失败时进入归责流程。
		_, errUp := party.UpdateFromBytes(wireBytes, partyID, isBroadcast)
		if errUp != nil {
			err := t.processInvalidMsg(round.RoundMsg, round, errUp)
			t.logger.Error().Err(err).Msgf("fail to apply the share to tss")
			continue
		}
		t.abnormalMgr.UpdateAcceptShare(round, partyID.Id)
	}
}

func (t *TssCommon) GetConf() TssConfig {
	return t.conf
}

func (t *TssCommon) GetAbnormalMgr() *abnormal.Manager {
	return t.abnormalMgr
}

func (t *TssCommon) GetTaskDone() chan struct{} {
	return t.taskDone
}

func (t *TssCommon) GetThreshHold() int {
	return t.threshHold
}

// InsertPartyIDtoP2PID 批量写入 PartyID 与 PeerID 的映射。
func (t *TssCommon) InsertPartyIDtoP2PID(newMap map[string]peer.ID) {
	for k, v := range newMap {
		t.partyIDtoP2PIDMap.Store(k, v)
	}
}

// GetPartyIDtoP2PID 返回当前映射表的快照副本。
func (t *TssCommon) GetPartyIDtoP2PID() map[string]peer.ID {
	result := make(map[string]peer.ID)
	t.partyIDtoP2PIDMap.Range(func(key, value any) bool {
		result[key.(string)] = value.(peer.ID)
		return true
	})
	return result
}

// SetPartyInfo 设置当前本地 party 及其参与方映射信息。
func (t *TssCommon) SetPartyInfo(partyInfo *abnormal.PartyInfo) {
	t.partyLock.Lock()
	defer t.partyLock.Unlock()
	t.partyInfo = partyInfo
}

func (t *TssCommon) getPartyInfo() *abnormal.PartyInfo {
	t.partyLock.RLock()
	defer t.partyLock.RUnlock()
	return t.partyInfo
}

// GetLocalPeerID 返回本节点对应的 PeerID。
func (t *TssCommon) GetLocalPeerID() string {
	return t.localPeerID
}

func (t *TssCommon) SetLocalPeerID(peerID string) {
	t.localPeerID = peerID
}

// ProcessOutCh 处理本地 TSS 输出消息，缓存同轮数据后统一发送到网络层p2p。
// 当 `tss-lib` local party 从 `outCh` 产出一条 `tss.Message` 时，`TssCommon.ProcessOutCh(...)` 会：
// 1.调 `msg.WireBytes()` 拿到： 原始 wire bytes ,`tss.MessageRouting`
// 2. 生成 `BulkWireMsg` 按广播/单播缓存到 cachedWireBroadcastMsgLists ,cachedWireUnicastMsgLists
// 3. 逐个调用 `sendBulkMsg(...)`
func (t *TssCommon) ProcessOutCh(msg tss.Message, msgType messages.TSSMessageTpe) error {
	msgData, r, err := msg.WireBytes()
	// 无法导出 wire bytes 说明当前 TSS 流程已无法继续，直接返回错误。
	if err != nil {
		return fmt.Errorf("fail to get wire bytes: %w", err)
	}
	//按广播/单播缓存到
	if r.IsBroadcast {
		//todo :这里用Moniker 还是 id
		cachedWiredMsg := NewBulkWireMsg(msgData, msg.GetFrom().Moniker, r)
		// 广播消息按消息类型缓存，等待后续统一签名发送。
		_, ok := t.cachedWireBroadcastMsgLists.Load(msg.Type())
		if !ok {
			t.cachedWireBroadcastMsgLists.Store(msg.Type(), cachedWiredMsg)
		}
	} else {
		//todo :这里用Moniker 还是 id
		cachedWiredMsg := NewBulkWireMsg(msgData, msg.GetFrom().Moniker, r)
		_, ok := t.cachedWireUnicastMsgLists.Load(msg.Type() + ":" + r.To[0].String())
		if !ok {
			t.cachedWireUnicastMsgLists.Store(msg.Type()+":"+r.To[0].String(), cachedWiredMsg)
		}
	}
	t.cachedWireUnicastMsgLists.Range(func(key, value interface{}) bool {
		wiredMsg := value.(BulkWireMsg)
		ret := strings.Split(key.(string), ":")
		wiredMsgType := ret[0]
		err := t.sendBulkMsg(wiredMsgType, msgType, wiredMsg)
		if err != nil {
			t.logger.Error().Err(err).Msg("error in send bulk message")
			return true
		}
		t.cachedWireUnicastMsgLists.Delete(key)
		return true
	})
	t.cachedWireBroadcastMsgLists.Range(func(key, value interface{}) bool {
		wiredMsg := value.(BulkWireMsg)
		wiredMsgType := key.(string)

		err := t.sendBulkMsg(wiredMsgType, msgType, wiredMsg)
		if err != nil {
			t.logger.Error().Err(err).Msg("error in send bulk message")
			return true
		}
		t.cachedWireBroadcastMsgLists.Delete(key)

		return true
	})

	return nil
}

// ProcessInboundMessages 持续消费来自 P2P 层的消息，并按类型分发处理 ProcessOneMessage。
// 持续从 `TssMsg` 读消息,这个 channel 的上游来自 `Communication` 根据 `(messageType, msgID)` 做的订阅路由
func (t *TssCommon) ProcessInboundMessages(finishChan chan struct{}, wg *sync.WaitGroup) {
	t.logger.Debug().Msg("start processing inbound messages")
	defer func() {
		t.logger.Debug().Msg("stop processing inbound messages")
		wg.Done()
	}()
	for {
		select {
		case <-finishChan:
			return
		case m, ok := <-t.TssMsg:
			if !ok {
				return
			}
			var wrappedMsg messages.WrappedMessage
			if err := json.Unmarshal(m.Payload, &wrappedMsg); nil != err {
				t.logger.Error().Err(err).Msg("fail to unmarshal wrapped message bytes")
				continue
			}
			err := t.ProcessOneMessage(&wrappedMsg, m.PeerID.String())
			if err != nil {
				t.logger.Error().Err(err).Msg("fail to process the received message")
			}
		}
	}
}

// ProcessOneMessage 根据消息类型(按 `WrappedMessage.MessageType` 分三类)分别处理 TSS 数据消息和任务完成通知。
func (t *TssCommon) ProcessOneMessage(wrappedMsg *messages.WrappedMessage, peerID string) error {
	t.logger.Debug().Msg("start process one message")
	defer t.logger.Debug().Msg("finish processing one message")
	if wrappedMsg == nil {
		return errors.New("invalid wireMessage")
	}

	switch wrappedMsg.MessageType {
	case messages.TSSKeyGenMsg, messages.TSSKeySignMsg:
		var wireMsg messages.WireMessage
		if err := json.Unmarshal(wrappedMsg.Payload, &wireMsg); nil != err {
			return fmt.Errorf("fail to unmarshal wire message: %w", err)
		}
		return t.processTSSMsg(&wireMsg, wrappedMsg.MessageType, true)
	case messages.TSSTaskDone:
		var wireMsg messages.TssTaskNotifier
		err := json.Unmarshal(wrappedMsg.Payload, &wireMsg)
		if err != nil {
			t.logger.Error().Err(err).Msg("fail to unmarshal the notify message")
			return nil
		}
		if wireMsg.TaskDone {
			// if we have already logged this node, we return to avoid close of a close channel
			if t.finishedPeers[peerID] {
				return fmt.Errorf("duplicated notification from peer %s ignored", peerID)
			}
			t.finishedPeers[peerID] = true
			if len(t.finishedPeers) == len(t.partyInfo.PartyIDMap)-1 {
				t.logger.Debug().Msg("we get the confirm of the nodes that generate the signature")
				close(t.taskDone)
			}
			return nil
		}
	default:
		return errors.New("invalid message type.")
	}
	return nil
}

// sendBulkMsg 对缓存消息进行签名封装，并按路由信息发给目标 Peer。
// 1. 把 `BulkWireMsg` JSON 序列化
// 2. 用本节点私钥对 `message + msgID` 做 `Keccak256` 后签名
// 3. 构造 `WireMessage{Routing, RoundInfo, Message, Sig}`
// 4. 再包成 `WrappedMessage{MessageType, MsgID, Payload}`
// 5. 决定目标 peer：
// - `Routing.To` 为空：发给所有 `P2PPeers`
// - 否则：按 `partyID -> peer.ID` 找目标
// 6. 写入 `BroadcastMsgChan`
func (t *TssCommon) sendBulkMsg(wiredMsgType string, tssMsgType messages.TSSMessageTpe, wiredMsg BulkWireMsg) error {
	// 同一缓存项中的消息属于同一轮次，因此直接复用其路由信息即可。
	r := wiredMsg.Routing
	buf, err := json.Marshal(wiredMsg)
	if err != nil {
		return fmt.Errorf("error in marshal the cachedWireMsg: %w", err)
	}
	sig, err := generateSignature(buf, t.msgID, t.privateKey)
	if err != nil {
		t.logger.Error().Err(err).Msg("fail to generate the share's signature")
		return err
	}
	wireMsg := messages.WireMessage{
		Routing:   r,
		RoundInfo: wiredMsgType,
		Message:   buf,
		Sig:       sig,
	}
	wireMsgBytes, err := json.Marshal(wireMsg)
	if err != nil {
		return fmt.Errorf("fail to convert tss msg to wire bytes: %w", err)
	}
	wrappedMsg := messages.WrappedMessage{
		MsgID:       t.msgID,
		MessageType: tssMsgType,
		Payload:     wireMsgBytes,
	}

	// 广播发给全部已知 Peer；单播只发给路由指定目标。
	peerIDs := make([]peer.ID, 0)
	if len(r.To) == 0 {
		t.P2PPeersLock.RLock()
		peerIDs = t.P2PPeers
		t.P2PPeersLock.RUnlock()
	} else {
		for _, each := range r.To {
			peerID, ok := t.partyIDtoP2PIDMap.Load(each.Id)
			if !ok {
				t.logger.Error().Msg("error in find the P2P ID")
				continue
			}
			peerIDs = append(peerIDs, peerID.(peer.ID))
		}
	}
	t.renderToP2P(&messages.BroadcastMsgChan{
		WrappedMessage: wrappedMsg,
		PeersID:        peerIDs,
	})
	return nil
}

// processInvalidMsg 在收到非法 share 时收集证据并更新异常归责结果。
func (t *TssCommon) processInvalidMsg(roundInfo string, round abnormal.RoundInfo, err *tss.Error) error {
	// 收集作恶方的 PartyID，以及其发送过来的原始消息和签名证据。
	var culpritsID []string
	var invalidMsgs []*messages.WireMessage
	unicast := checkUnicast(round)
	t.culpritsLock.Lock()
	t.culprits = append(t.culprits, err.Culprits()...)
	t.culpritsLock.Unlock()
	for _, el := range err.Culprits() {
		culpritsID = append(culpritsID, el.Id)
		key := fmt.Sprintf("%s-%s", el.Id, roundInfo)
		//基于 `RoundMgr` 找回这些可疑 party 在对应 round 发过的 `WireMessage`
		storedMsg := t.abnormalMgr.GetRoundMgr().Get(key)
		invalidMsgs = append(invalidMsgs, storedMsg)
	}
	pubkeys, errBlame := conversion.AccPubKeysFromPartyIDs(culpritsID, t.partyInfo.PartyIDMap)
	if errBlame != nil {
		t.logger.Error().Err(err.Cause()).Msgf("error in get the blame nodes")
		t.abnormalMgr.GetAbnormal().SetAbnormal(abnormal.TssBrokenMsg, nil, unicast)
		return fmt.Errorf("error in getting the blame nodes")
	}
	// share 校验失败意味着消息内容本身有问题，因此要连同签名一起保留，作为该消息确实由发送方发出的证明。
	var blameNodes []*abnormal.Node
	var msgBody, sig []byte
	for i, pk := range pubkeys {
		invalidMsg := invalidMsgs[i]
		if invalidMsg == nil {
			t.logger.Error().Msg("we cannot find the record of this curlprit, set it as blank")
			msgBody = []byte{}
			sig = []byte{}
		} else {
			msgBody = invalidMsg.Message
			sig = invalidMsg.Sig
		}
		blameNodes = append(blameNodes, abnormal.NewNode(pk, msgBody, sig))
	}
	t.abnormalMgr.GetAbnormal().SetAbnormal(abnormal.TssBrokenMsg, blameNodes, unicast)
	return fmt.Errorf("fail to set bytes to local party: %w", err)
}

// generateSignature 对消息体和 msgID 的拼接摘要做签名，防止跨任务重放。
func generateSignature(msg []byte, msgID string, privKey *ecdsa.PrivateKey) ([]byte, error) {
	var dataForSigning bytes.Buffer
	dataForSigning.Write(msg)
	dataForSigning.WriteString(msgID)
	digestBz := crypto.Keccak256Hash(dataForSigning.Bytes()).Bytes()
	return crypto.Sign(digestBz, privKey)
}

// verifySignature 校验消息体和 msgID 拼接后的签名是否合法。
func verifySignature(pubKey, message, sig []byte, msgID string) bool {
	var dataForSign bytes.Buffer
	dataForSign.Write(message)
	dataForSign.WriteString(msgID)
	digestBz := crypto.Keccak256Hash(dataForSign.Bytes()).Bytes()
	if len(sig) == crypto.SignatureLength {
		sig = sig[:len(sig)-1]
	}
	return crypto.VerifySignature(pubKey, digestBz, sig)
}

// updateLocal(...) 做的事情是： 把消息喂回 `tss-lib`
// “收到其他 Node 的 TSS 协议消息，并推进本地密码学状态机”
// 1. 通过 `partyInfo` 找到当前协议实例对应的 local party
// 2. 根据 `Routing.From.Id` 找到发送方的 `PartyID`
// 3. 对 `WireMessage.Message` 做 `GetMsgRound(...)`，解析出当前 round
// 4. 做重复消息过滤
// 5. 如果该发送方已在 culprit 列表里，则拒绝
// 6. 调 `party.UpdateFromBytes(wireBytes, partyID, isBroadcast)`
// 7. 若成功，记录 accepted share
// 8. 若失败，进入 `processInvalidMsg(...)`
func (t *TssCommon) updateLocal(wireMsg *messages.WireMessage) error {
	if wireMsg == nil || wireMsg.Routing == nil || wireMsg.Routing.From == nil {
		t.logger.Warn().Msg("wire msg is nil")
		return errors.New("invalid wireMsg")
	}
	partyInfo := t.getPartyInfo()
	if partyInfo == nil {
		return nil
	}
	partyID, ok := partyInfo.PartyIDMap[wireMsg.Routing.From.Id]
	if !ok {
		return fmt.Errorf("get message from unknown party %s", partyID.Id)
	}
	dataOwnerPeerID, ok := t.partyIDtoP2PIDMap.Load(wireMsg.Routing.From.Id)
	if !ok {
		t.logger.Error().Msg("fail to find the peer ID of this party")
		return errors.New("fail to find the peer")
	}
	// 单播场景下记录最近一次发给本节点的对端，便于后续异常定位。
	if !wireMsg.Routing.IsBroadcast {
		t.abnormalMgr.SetLastUnicastPeer(dataOwnerPeerID.(peer.ID), wireMsg.RoundInfo)
	}
	var bulkMsg BulkWireMsg
	err := json.Unmarshal(wireMsg.Message, &bulkMsg)
	if err != nil {
		t.logger.Error().Err(err).Msg("error to unmarshal the BulkMsg")
		return err
	}

	tssJobChan := make(chan *tssJob)
	jobWg := sync.WaitGroup{}
	jobWg.Add(1)
	go t.doTssJob(tssJobChan, &jobWg)

	localMsgParty := partyInfo.Party
	rPartyID, ok := partyInfo.PartyIDMap[bulkMsg.Routing.From.Id]
	if !ok {
		t.logger.Error().Msg("error in find the partyID")
		return errors.New("cannot find the party to handle the message")
	}
	round, err := GetMsgRound(bulkMsg.WiredBulkMsg, rPartyID, bulkMsg.Routing.IsBroadcast)
	if err != nil {
		t.logger.Error().Err(err).Msg("broken tss share")
		return err
	}
	// 每条消息只允许被本地状态机应用一次，这里用 round + msgIdentifier 来区分唯一性。
	if bulkMsg.MsgIdentifier == "" {
		round.MsgIdentifier = bulkMsg.Routing.From.Id
	} else {
		round.MsgIdentifier = bulkMsg.MsgIdentifier
	}

	// 已确认过的重复 share 直接丢弃，避免重复推进轮次。
	if t.abnormalMgr.CheckMsgDuplication(round, partyID.Id) {
		t.logger.Debug().Msgf("we received the duplicated message from party %s", partyID.Id)
	} else {
		partyInlist := func(el *tss.PartyID, l []*tss.PartyID) bool {
			for _, each := range l {
				if el == each {
					return true
				}
			}
			return false
		}
		err = func() error {
			t.culpritsLock.RLock()
			defer t.culpritsLock.RUnlock()
			// 已被判定为恶意节点的消息不再进入本地 TSS 计算流程。
			if len(t.culprits) != 0 && partyInlist(partyID, t.culprits) {
				return errors.New("tss share verification failed")
			}
			return nil
		}()
		if err != nil {
			t.logger.Err(err).Msgf("the malicious party (party ID:%s) try to send incorrect message to me (party ID:%s)",
				partyID.Id, localMsgParty.PartyID().Id)
			return err
		}
		job := newJob(localMsgParty, bulkMsg.WiredBulkMsg, round.MsgIdentifier, partyID, bulkMsg.Routing.IsBroadcast)
		tssJobChan <- job
	}

	close(tssJobChan)
	jobWg.Wait()
	return nil
}

// renderToP2P 将待发送消息交给外部 P2P 广播通道。
func (t *TssCommon) renderToP2P(broadcastMsg *messages.BroadcastMsgChan) {
	if t.broadcastChannel == nil {
		t.logger.Warn().Msg("broadcast channel is not set")
		return
	}
	t.broadcastChannel <- broadcastMsg
}

// processTSSMsg 校验签名后处理单播或广播 TSS 消息。
func (t *TssCommon) processTSSMsg(wireMsg *messages.WireMessage, msgType messages.TSSMessageTpe, forward bool) error {
	t.logger.Debug().Msg("process wire message")
	defer t.logger.Debug().Msg("finish process wire message")
	if wireMsg == nil || wireMsg.Routing == nil || wireMsg.Routing.From == nil {
		t.logger.Warn().Msg("received msg invalid, msg type  " + msgType.String())
		if wireMsg == nil {
			t.logger.Warn().Msg("wireMsg is nil ! ")
		} else if wireMsg.Routing == nil {
			t.logger.Warn().Msg("wireMsg Routing is nil ! ")
		} else if wireMsg.Routing.From == nil {
			t.logger.Warn().Msg("wireMsg Routing From is nil ! ")
		}
		return errors.New("invalid wireMsg")
	}
	partyIDMap := t.getPartyInfo().PartyIDMap
	dataOwner, ok := partyIDMap[wireMsg.Routing.From.Id]
	if !ok {
		t.logger.Error().Msg("error in find the data owner")
		return errors.New("error in find the data owner")
	}
	keyBytes := dataOwner.GetKey()

	ok = verifySignature(keyBytes, wireMsg.Message, wireMsg.Sig, t.msgID)
	if !ok {
		t.logger.Error().Msg("fail to verify the signature")
		return errors.New("signature verify failed")
	}

	// 单播消息无需等待其他节点确认，直接推进本地 party 状态。
	if !wireMsg.Routing.IsBroadcast {
		t.logger.Debug().Msgf("msg from %s to %+v", wireMsg.Routing.From, wireMsg.Routing.To)
		return t.updateLocal(wireMsg)
	}
	// 广播消息需要做节点一致性观察 要存入LocalCacheItem
	key := wireMsg.GetCacheKey()
	msgHash, err := conversion.BytesToHashString(wireMsg.Message)
	if err != nil {
		return fmt.Errorf("fail to calculate hash of the wire message: %w", err)
	}
	localCacheItem := t.TryGetLocalCacheItem(key)
	if localCacheItem == nil {
		t.logger.Debug().Msgf("%s doesn't exist yet,add a new one", key)
		localCacheItem = NewLocalCacheItem(wireMsg, msgHash)
		t.unConfirmedMessagesMap.Store(key, localCacheItem)
	} else {
		// 说明此前先收到了其他节点针对该广播消息的确认，再补齐原始消息内容。
		t.logger.Debug().Msgf("%s exist", key)
		if localCacheItem.Msg == nil {
			t.logger.Debug().Msgf("%s exist, set message", key)
			localCacheItem.Msg = wireMsg
			localCacheItem.Hash = msgHash
		}
	}
	// 把“我本地看到的这个 hash”记进 ConfirmedList
	localCacheItem.UpdateConfirmList(t.localPeerID, msgHash)

	return t.applyShare(localCacheItem, key, msgType)
}

// TryGetLocalCacheItem 从未确认消息缓存中取出指定 key 的对象。
func (t *TssCommon) TryGetLocalCacheItem(key string) *LocalCacheItem {
	localCacheItem, ok := t.unConfirmedMessagesMap.Load(key)
	if !ok {
		return nil
	}
	return localCacheItem.(*LocalCacheItem)
}

// applyShare 将消息记录到轮次管理器，并推动本地 party 状态更新。
func (t *TssCommon) applyShare(localCacheItem *LocalCacheItem, key string, msgType messages.TSSMessageTpe) error {

	t.abnormalMgr.GetRoundMgr().Set(key, localCacheItem.Msg)
	//hashCheck 成功之后再 updateLocal
	if err := t.hashCheck(localCacheItem, t.threshHold); err != nil {
		return fmt.Errorf("fail to check the hash: %w", err)
	}
	if err := t.updateLocal(localCacheItem.Msg); nil != err {
		return fmt.Errorf("fail to update the message to local party: %w", err)
	}
	t.logger.Debug().Msgf("remove key: %s", key)
	// 该广播消息已经通过确认并成功应用，缓存可以删除。
	t.unConfirmedMessagesMap.Delete(key)
	return nil
}

// hashCheck 基于确认列表判断当前广播消息哈希是否达成多数一致。
// - 确认数够不够
// - 数据拥有者不能给自己的消息背书
// - 统计多数 hash
// - 判断当前消息 hash 是否和多数一致
func (t *TssCommon) hashCheck(localCacheItem *LocalCacheItem, threshold int) error {
	dataOwner := localCacheItem.Msg.Routing.From
	dataOwnerP2PID, ok := t.partyIDtoP2PIDMap.Load(dataOwner.Id)
	if !ok {
		t.logger.Warn().Msgf("error in find the data Owner P2PID\n")
		return errors.New("error in find the data Owner P2PID")
	}

	if localCacheItem.TotalConfirmParty() < threshold {
		t.logger.Debug().Msg("not enough nodes to evaluate the hash")
		return abnormal.ErrNotEnoughPeer
	}
	localCacheItem.lock.Lock()
	defer localCacheItem.lock.Unlock()

	targetHashValue := localCacheItem.Hash
	for P2PID := range localCacheItem.ConfirmedList {
		if P2PID == dataOwnerP2PID.(peer.ID).String() {
			t.logger.Warn().Msgf("we detect that the data owner try to send the hash for his own message\n")
			delete(localCacheItem.ConfirmedList, P2PID)
			return abnormal.ErrHashFromOwner
		}
	}
	hash, err := t.getMsgHash(localCacheItem, threshold)
	if err != nil {
		return err
	}
	if targetHashValue == hash {
		t.logger.Debug().Msgf("hash check complete for messageID: %v", t.msgID)
		return nil
	}
	return abnormal.ErrNotMajority
}

// getMsgHash 从确认列表中提取出现频率最高且达到阈值要求的哈希值。
func (t *TssCommon) getMsgHash(localCacheItem *LocalCacheItem, threshold int) (string, error) {
	hash, freq, err := getHighestFreq(localCacheItem.ConfirmedList)
	if err != nil {
		t.logger.Error().Err(err).Msg("fail to get the hash freq")
		return "", abnormal.ErrHashCheck
	}
	if freq < threshold-1 {
		t.logger.Debug().Msgf("fail to have more than 2/3 peers agree on the received message threshold(%d)--total confirmed(%d)\n", threshold, freq)
		return "", abnormal.ErrHashInconsistency
	}
	return hash, nil
}
