package common

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Sandwichzzy/tss/node/tsslib/abnormal"
	"github.com/Sandwichzzy/tss/node/tsslib/messages"
	"github.com/bnb-chain/tss-lib/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/ecdsa/signing"
	"github.com/bnb-chain/tss-lib/tss"
)

func MsgToHashString(msg []byte) (string, error) {
	if len(msg) == 0 {
		return "", errors.New("empty message")
	}
	h := sha256.New()
	_, err := h.Write(msg)
	if err != nil {
		return "", fmt.Errorf("fail to caculate sha256 hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func GetMsgRound(msg []byte, partyID *tss.PartyID, isBroadcast bool) (abnormal.RoundInfo, error) {
	parsedMsg, err := tss.ParseWireMessage(msg, partyID, isBroadcast)
	if err != nil {
		return abnormal.RoundInfo{}, err
	}
	switch parsedMsg.Content().(type) {
	case *keygen.KGRound1Message:
		return abnormal.RoundInfo{
			Index:    0,
			RoundMsg: messages.KEYGEN1,
		}, nil
	case *keygen.KGRound2Message1:
		return abnormal.RoundInfo{
			Index:    1,
			RoundMsg: messages.KEYGEN2aUnicast,
		}, nil

	case *keygen.KGRound2Message2:
		return abnormal.RoundInfo{
			Index:    2,
			RoundMsg: messages.KEYGEN2b,
		}, nil

	case *keygen.KGRound3Message:
		return abnormal.RoundInfo{
			Index:    3,
			RoundMsg: messages.KEYGEN3,
		}, nil

	case *signing.SignRound1Message1:
		return abnormal.RoundInfo{
			Index:    0,
			RoundMsg: messages.KEYSIGN1aUnicast,
		}, nil

	case *signing.SignRound1Message2:
		return abnormal.RoundInfo{
			Index:    1,
			RoundMsg: messages.KEYSIGN1b,
		}, nil

	case *signing.SignRound2Message:
		return abnormal.RoundInfo{
			Index:    2,
			RoundMsg: messages.KEYSIGN2Unicast,
		}, nil

	case *signing.SignRound3Message:
		return abnormal.RoundInfo{
			Index:    3,
			RoundMsg: messages.KEYSIGN3,
		}, nil

	case *signing.SignRound4Message:
		return abnormal.RoundInfo{
			Index:    4,
			RoundMsg: messages.KEYSIGN4,
		}, nil

	case *signing.SignRound5Message:
		return abnormal.RoundInfo{
			Index:    5,
			RoundMsg: messages.KEYSIGN5,
		}, nil

	case *signing.SignRound6Message:
		return abnormal.RoundInfo{
			Index:    6,
			RoundMsg: messages.KEYSIGN6,
		}, nil

	case *signing.SignRound7Message:
		return abnormal.RoundInfo{
			Index:    7,
			RoundMsg: messages.KEYSIGN7,
		}, nil

	case *signing.SignRound8Message:
		return abnormal.RoundInfo{
			Index:    8,
			RoundMsg: messages.KEYSIGN8,
		}, nil

	case *signing.SignRound9Message:
		return abnormal.RoundInfo{
			Index:    9,
			RoundMsg: messages.KEYSIGN9,
		}, nil

	default:
		return abnormal.RoundInfo{}, errors.New("unknown round")
	}

}

// 由于 TSS 的零知识证明和秘密分享具有延迟验证特性，当前轮收到的数据，可能只能验证出上一轮某个参与者发送的数据有问题
// 上一轮是不是点对点（unicast）消息。如果是，就可以定位到具体发送者；如果是广播（broadcast），则无法仅根据当前信息确定是谁作恶。
func checkUnicast(round abnormal.RoundInfo) bool {
	index := round.Index
	isKeyGen := strings.Contains(round.RoundMsg, "KGR")
	// keygen unicast blame
	if isKeyGen {
		if index == 1 || index == 2 {
			return true
		}
		return false
	}
	// keysign unicast blame
	if index < 5 {
		return true
	}
	return false
}

func getHighestFreq(confirmedList map[string]string) (string, int, error) {
	if len(confirmedList) == 0 {
		return "", 0, errors.New("empty input")
	}
	freq := make(map[string]int, len(confirmedList))
	for _, n := range confirmedList {
		freq[n]++
	}
	maxFreq := -1
	var data string
	for key, counter := range freq {
		if counter > maxFreq {
			maxFreq = counter
			data = key
		}
	}
	return data, maxFreq, nil
}
