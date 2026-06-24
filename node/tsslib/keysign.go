package tsslib

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	common2 "github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/node/tsslib/abnormal"
	"github.com/Sandwichzzy/tss/node/tsslib/common"
	"github.com/Sandwichzzy/tss/node/tsslib/conversion"
	keysign2 "github.com/Sandwichzzy/tss/node/tsslib/keysign"
	"github.com/Sandwichzzy/tss/node/tsslib/messages"
	"github.com/Sandwichzzy/tss/node/tsslib/storage"
	"github.com/libp2p/go-libp2p/core/peer"
)

func (t *TssServer) KeySign(req keysign2.Request) (keysign2.Response, error) {
	t.logger.Info().Str("pool pub key", req.PoolPubKey).
		Str("signer pub keys", strings.Join(req.SignerPubKeys, ",")).
		Str("msg hex", hex.EncodeToString(req.Message)).
		Msg("received keysign request")

	emptyResp := keysign2.Response{}
	err := t.requestCheck(req)
	if err != nil {
		t.logger.Error().Err(err).Msg("keySign request has incorrect parameter values.")
		return emptyResp, err
	}
	var localStateItem storage.KeygenLocalState
	if t.shamirEnable {
		localStateItem, err = t.shamirManager.GetKeyFile(req.PoolPubKey, t.localNodePubKey)
		if err != nil {
			return emptyResp, fmt.Errorf("fail to get local keygen state from shamir manager: %w", err)
		}
	} else if t.secretsEnable {
		localStateItem, err = t.secretsManager.GetKeyFile(req.PoolPubKey)
		if err != nil {
			return emptyResp, fmt.Errorf("fail to get local keygen state from secrets manager: %w", err)
		}
	} else {
		localStateItem, err = t.stateManager.GetLocalState(req.PoolPubKey)
		if err != nil {
			return emptyResp, fmt.Errorf("fail to get local keygen state from local drive: %w", err)
		}
	}

	t.setParticipants(req.PoolPubKey, localStateItem.ParticipantKeys)
	//check signers if not contained in participants
	err = t.isContainPubkeys(req.SignerPubKeys, localStateItem.ParticipantKeys, req.PoolPubKey)
	if err != nil {
		return emptyResp, err
	}
	msgID, err := t.requestToMsgId(req)
	if err != nil {
		return emptyResp, err
	}

	keysignInstance := keysign2.NewTssKeySign(
		t.p2pCommunication.GetLocalPeerID(),
		t.conf,
		t.p2pCommunication.BroadcastMsgChan,
		t.stopChan,
		msgID,
		t.privateKey,
		t.p2pCommunication,
		t.stateManager,
		localStateItem.Threshold,
	)

	keySignChannels := keysignInstance.GetTssKeySignChannels()
	t.p2pCommunication.SetSubscribe(messages.TSSKeySignMsg, msgID, keySignChannels)
	t.p2pCommunication.SetSubscribe(messages.TSSTaskDone, msgID, keySignChannels)

	defer func() {
		t.p2pCommunication.CancelSubscribe(messages.TSSKeySignMsg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSTaskDone, msgID)

		t.p2pCommunication.ReleaseStream(msgID)
	}()

	if len(req.SignerPubKeys) == 0 {
		return emptyResp, errors.New("empty signer pub keys")
	}

	//没有在这里额外探测“这些 signer 当前是否真的在线”；它只是把签名参与者集合翻译成可以用于 p2p 建流的 peer id 列表。
	onlines, err := t.CheckPubKeys(req.SignerPubKeys, localStateItem.Threshold)
	if err != nil {
		return emptyResp, err
	}

	var generatedSig keysign2.Response
	var errGen error
	keysignStartTime := time.Now()
	generatedSig, errGen = t.generateSignature(onlines, req, localStateItem, keysignInstance)
	if errGen != nil {
		return generatedSig, errGen
	}
	keysignTime := time.Since(keysignStartTime)

	// we get the signature from our tss keysign
	t.updateKeySignResult(generatedSig, keysignTime)

	return generatedSig, nil
}

func (t *TssServer) GetParticipants(poolPubkey string) ([]string, error) {
	t.participantsLock.RLock()
	defer t.participantsLock.RUnlock()

	value, ok := t.participants[poolPubkey]
	if !ok {
		return nil, errors.New("There is wrong in participants map, need to store latest pool pubkey's participants!")
	}
	return append([]string(nil), value...), nil
}

func (t *TssServer) setParticipants(poolPubkey string, participants []string) {
	t.participantsLock.Lock()
	defer t.participantsLock.Unlock()

	t.participants[poolPubkey] = append([]string(nil), participants...)
}

func (t *TssServer) generateSignature(onlinePeers []peer.ID, req keysign2.Request, localStateItem storage.KeygenLocalState, keysignInstance *keysign2.TssKeySign) (keysign2.Response, error) {
	// 首先会检查当前 Node 是否属于 `onlinePeers`
	// 这一步的意思是：本次签名并不是所有已知节点都必须参与, 只有请求里指定的 signer 集合里的节点，才真正跑 keysign
	isKeySignMember := false
	for _, el := range onlinePeers {
		if el == t.p2pCommunication.GetHost().ID() {
			isKeySignMember = true
		}
	}
	if !isKeySignMember {
		// we are not the keysign member so we quit keysign and waiting for signature
		t.logger.Info().Msgf("we(%s) are not the active signer", t.p2pCommunication.GetHost().ID().String())
		return keysign2.Response{}, errors.New("not active signer")
	}

	parsedPeers := make([]string, len(onlinePeers))
	for i, el := range onlinePeers {
		parsedPeers[i] = el.String()
	}

	signers, err := conversion.GetPubKeysFromPeerIDs(parsedPeers)
	if err != nil {
		return keysign2.Response{
			Status:     common.Fail,
			FailReason: err.Error(),
		}, nil
	}
	signatureData, err := keysignInstance.SignMessage(req.Message, localStateItem, signers)
	// the statistic of keygen only care about Tss itself, even if the following http response aborts,
	// it still counted as a successful keygen as the Tss model runs successfully.
	if err != nil {
		t.logger.Error().Err(err).Msg("err in keysign")
		return keysign2.Response{
			Status:     common.Fail,
			FailReason: abnormal.SignatureError,
		}, nil
	}

	return keysign2.NewResponse(
		signatureData,
		common.Success,
		"",
	), nil
}

func (t *TssServer) updateKeySignResult(result keysign2.Response, timeSpent time.Duration) {
	if result.Status == common.Success {
		t.tssMetrics.UpdateKeySign(timeSpent, true)
		return
	}
	t.tssMetrics.UpdateKeySign(timeSpent, false)
	return
}

func (t *TssServer) isPartOfKeySignParty(parties []string) bool {
	for _, item := range parties {
		if t.localNodePubKey == item {
			return true
		}
	}
	return false
}

func (t *TssServer) isContainPubkeys(signerPubKeys, participants []string, poolPubkey string) error {
	var errPubKeys []string
	for _, pubKey := range signerPubKeys {
		if !common2.ExistsIgnoreCase(participants, pubKey) {
			errPubKeys = append(errPubKeys, pubKey)
		}
	}
	if len(errPubKeys) != 0 {
		return fmt.Errorf("these pub keys %s are not members of %s's participants", strings.Join(errPubKeys, ","), poolPubkey)
	}
	return nil
}
