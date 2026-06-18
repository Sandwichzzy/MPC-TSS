package tsslib

import (
	"strconv"
	"strings"
	"time"

	"github.com/Sandwichzzy/tss/node/tsslib/abnormal"
	"github.com/Sandwichzzy/tss/node/tsslib/common"
	"github.com/Sandwichzzy/tss/node/tsslib/conversion"
	keygen2 "github.com/Sandwichzzy/tss/node/tsslib/keygen"
	"github.com/Sandwichzzy/tss/node/tsslib/messages"
)

// Keygen 做这些事情：
//
// 1. 先持有 `tssKeyGenLocker`，确保同一时刻只跑一个 keygen
// 2. 基于请求计算 `msgID`
// 3. 调用 `requestCheck(...)`
// 4. 创建 `TssKeyGen`
// 5. 建立该 `msgID` 对应的 p2p 订阅
// 6. 调用 `GenerateNewKey(...)`
// 7. 如果成功，把 `ECDSAPub` 转成：
// - 压缩公钥字符串
// - EVM 地址
// - 64 字节裸公钥
// 8. 返回 `keygen.Response`
func (t *TssServer) Keygen(req keygen2.Request) (keygen2.Response, error) {
	t.tssKeyGenLocker.Lock()
	defer t.tssKeyGenLocker.Unlock()

	status := common.Success
	msgID, err := t.requestToMsgId(req)
	if err != nil {
		return keygen2.Response{}, err
	}

	if err = t.requestCheck(req); err != nil {
		return keygen2.Response{}, err
	}
	t.logger.Info().Str("keygen keys", strings.Join(req.Keys, ",")).
		Str("threshold", strconv.Itoa(req.ThresHold)).
		Msg("received keygen request")

	keygenInstance := keygen2.NewTssKeyGen(
		t.p2pCommunication.GetLocalPeerID(),
		t.conf,
		t.localNodePubKey,
		t.p2pCommunication.BroadcastMsgChan,
		t.stopChan,
		t.preParams,
		msgID,
		t.stateManager,
		//t.secretsEnable,
		//t.secretsManager,
		//t.shamirEnable,
		//t.shamirManager,
		t.privateKey,
		t.p2pCommunication,
		req.ThresHold)

	keygenMsgChannel := keygenInstance.GetTssKeyGenChannels()
	t.p2pCommunication.SetSubscribe(messages.TSSKeyGenMsg, msgID, keygenMsgChannel)
	t.p2pCommunication.SetSubscribe(messages.TSSTaskDone, msgID, keygenMsgChannel)

	defer func() {
		t.p2pCommunication.CancelSubscribe(messages.TSSKeyGenMsg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSTaskDone, msgID)

		t.p2pCommunication.ReleaseStream(msgID)
	}()

	abnormalMgr := keygenInstance.GetTssCommonStruct().GetAbnormalMgr()
	keygenInstance.ParticipantKeys = req.Keys

	t.logger.Debug().Msg("keygen party formed")
	// the statistic of keygen only care about Tss itself, even if the
	// following http response aborts, it still counted as a successful keygen
	// as the Tss model runs successfully.
	beforeKeygen := time.Now()
	k, err := keygenInstance.GenerateNewKey(req)
	keygenTime := time.Since(beforeKeygen)
	if err != nil {
		t.tssMetrics.UpdateKeyGen(keygenTime, false)
		t.logger.Error().Err(err).Msg("err in keygen")

		return keygen2.NewResponse(
			"", nil, nil, common.Fail, abnormal.GenerateNewKeyError,
			abnormalMgr.GetAbnormalNodePubKeys()), err
	} else {
		t.tssMetrics.UpdateKeyGen(keygenTime, true)
	}

	pubkey, address, pubkeyByte, err := conversion.GetTssPubKey(k)
	if err != nil {
		return keygen2.NewResponse(
			"",
			nil,
			nil,
			common.Fail,
			abnormal.GenerateNewKeyError,
			abnormalMgr.GetAbnormalNodePubKeys()), err
	}

	return keygen2.NewResponse(
		pubkey,
		address,
		pubkeyByte,
		status,
		"",
		abnormalMgr.GetAbnormalNodePubKeys(),
	), nil
}
