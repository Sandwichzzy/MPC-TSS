package signer

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	tsscommon "github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/message"
	"github.com/Sandwichzzy/tss/node/tsslib/common"
	"github.com/Sandwichzzy/tss/node/tsslib/keysign"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/rs/zerolog"
	tmtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// Sign() 处理正式签名
// 它的关键逻辑是：
// 1. 先解析 `NodeSignRequest`
// 2. 再从 `RequestBody` 中解析出原始 `TransactionSignRequest`
// 3. 调用 `checkMessages()` 检查该请求是否已经在 ask 阶段被记录到 `waitSignMsgs`
// 4. 如果没有 ask 记录，拒绝签名
// 5. 如果有记录，则调用 `p.tssServer.KeySign(...)`
// 6. 把签名整理成 `SignResponse`
// 7. 通过 WebSocket 回给 Manager
// 8.  p.storeMessageHash(）

func (p *Processor) Sign() {
	logger := p.logger.With().Str("step", "sign Message").Logger()

	logger.Info().Msg("start to sign message ")

	p.launchTracked(func() {
		defer func() {
			logger.Info().Msg("exit sign process")
		}()

		for {
			select {
			case <-p.stopChan:
				return
			case req := <-p.signRequestChan:
				var resId = req.ID.(tmtypes.JSONRPCStringID).String()
				logger.Info().Msgf("sign request id: %s", resId)

				var nodeSignRequest tsscommon.NodeSignRequest
				rawMsg := json.RawMessage{}
				nodeSignRequest.RequestBody = &rawMsg

				if err := json.Unmarshal(req.Params, &nodeSignRequest); err != nil {
					logger.Error().Msg("failed to unmarshal sign request")
					RpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 201, "failed", err.Error())
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				}

				var requestBody tsscommon.TransactionSignRequest
				if err := json.Unmarshal(rawMsg, &requestBody); err != nil {
					logger.Error().Msg("failed to unmarshal asker's params request body")
					RpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 201, "failed", err.Error())
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				}
				if requestBody.MessageHash == "" {
					logger.Error().Msg("MessageHash must not be nil or negative")
					RpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 201, "failed", "MessageHash must not be nil or negative")
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				}
				nodeSignRequest.RequestBody = requestBody

				p.launchTracked(func() {
					err := p.SignGo(req.ID.(tmtypes.JSONRPCStringID), nodeSignRequest, logger)
					if err != nil {
						return
					}
				})
			}
		}
	})
}

func (p *Processor) SignGo(resId tmtypes.JSONRPCStringID, sign tsscommon.NodeSignRequest, logger zerolog.Logger) error {
	var data []byte
	requestBody := sign.RequestBody.(tsscommon.TransactionSignRequest)
	err, hash, cacheKey, signByte := p.checkMessages(requestBody, sign.ClusterPublicKey)
	hashStr := hexutil.Encode(hash)

	if err != nil {
		RpcResponse := tmtypes.NewRPCErrorResponse(resId, 201, "failed", err.Error())
		if err := p.wsClient.SendMsg(RpcResponse); err != nil {
			logger.Error().Err(err).Msg("failed to send msg to manager")
		}
		logger.Err(err).Msg("check event failed")
		return err
	}
	//cache can not find the sign result by hashStr,we need to handle sign request.
	if signByte == nil {
		signData, err := p.handleSign(sign, hash, logger)
		if err != nil {
			logger.Error().Msgf(" %s sign failed ", hashStr)
			var errorRes tmtypes.RPCResponse
			errorRes = tmtypes.NewRPCErrorResponse(resId, 201, "sign failed", err.Error())

			er := p.wsClient.SendMsg(errorRes)
			if er != nil {
				logger.Err(er).Msg("failed to send msg to tss manager")
			}
			return err
		}
		bol := p.CacheSign(cacheKey, signData)
		logger.Info().Msgf("cache sign byte behavior %t", bol)
		data = signData
	} else {
		data = signByte
	}
	signResponse := tsscommon.SignResponse{
		Signature: data,
	}
	RpcResponse := tmtypes.NewRPCSuccessResponse(resId, signResponse)
	logger.Info().Msg("start to send response to manager ")

	err = p.wsClient.SendMsg(RpcResponse)
	if err != nil {
		logger.Err(err).Msg("failed to sendMsg to tss manager ")
		return err
	} else {
		logger.Info().Msg("send sign response to manager successfully")
		err := p.storeMessageHash(requestBody.ElectionId, requestBody.MessageHash, sign.Nodes, sign.ClusterPublicKey)
		if err != nil {
			logger.Err(err).Msg("failed to store MessageHash to level db")
		}
		p.removeWaitEvent(hashStr)
		return nil
	}

}

// checkMessages 检查该请求是否已经在 ask 阶段被记录到 `waitSignMsgs`, 并检查是否缓存里有签名数据
func (p *Processor) checkMessages(sign tsscommon.TransactionSignRequest, clusterPublicKey string) (err error, hashByte []byte, cacheKey string, signByte []byte) {
	hashByte, err = hex.DecodeString(sign.MessageHash)
	if err != nil {
		return err, hashByte, "", nil
	}
	hashStr := hexutil.Encode(hashByte)
	cacheKey = buildSignCacheKey(hashStr, sign.ElectionId, clusterPublicKey)

	signByte, ok := p.GetSign(cacheKey)
	if ok {
		return nil, hashByte, cacheKey, signByte
	}

	p.waitSignLock.RLock()
	defer p.waitSignLock.RUnlock()
	_, ok = p.waitSignMsgs[hashStr]
	if !ok {
		return errors.New("sign request has the unverified state batch"), nil, "", nil
	}

	return nil, hashByte, cacheKey, nil
}

func buildSignCacheKey(hashStr string, electionId uint64, clusterPublicKey string) string {
	normalizedClusterKey := strings.TrimSpace(clusterPublicKey)
	normalizedClusterKey = strings.TrimPrefix(normalizedClusterKey, "0x")
	normalizedClusterKey = strings.TrimPrefix(normalizedClusterKey, "0X")
	normalizedClusterKey = strings.ToLower(normalizedClusterKey)

	return fmt.Sprintf("%s:%d:%s", hashStr, electionId, normalizedClusterKey)
}

func (p *Processor) CacheSign(key string, value []byte) bool {
	p.cacheSignLock.Lock()
	defer p.cacheSignLock.Unlock()
	return p.cacheSign.Set(key, value)
}

func (p *Processor) GetSign(key string) ([]byte, bool) {
	p.cacheSignLock.RLock()
	defer p.cacheSignLock.RUnlock()
	return p.cacheSign.Get(key)
}

func (p *Processor) handleSign(sign tsscommon.NodeSignRequest, hashTx []byte, logger zerolog.Logger) ([]byte, error) {
	logger.Info().Msgf(" timestamp (%d) ,dealing sign hex (%s)", sign.Timestamp, hexutil.Encode(hashTx))
	signedData, err := p.sign(hashTx, sign.Nodes, sign.ClusterPublicKey, logger)
	if err != nil {
		return nil, err
	}
	signatureBytes := getSignatureBytes(&signedData)
	return signatureBytes, nil
}

func (p *Processor) sign(digestBz []byte, signerPubKeys []string, poolPubKey string, logger zerolog.Logger) (signatureData tsscommon.SignatureData, err error) {
	logger.Info().Str("message", hex.EncodeToString(digestBz)).Msg("got message to be signed")
	keysignReq := keysign.NewRequest(poolPubKey, digestBz, signerPubKeys)

	// todo: 提取私钥签名
	keysignRes, err := p.tssServer.KeySign(keysignReq)
	if err != nil {
		logger.Err(err).Msg("fail to generate signature ")
		return signatureData, err
	}

	if keysignRes.Status == common.Success {
		signatureData = tsscommon.SignatureData{
			SignatureRecovery: keysignRes.SignatureData.SignatureRecovery,
			R:                 keysignRes.SignatureData.R,
			S:                 keysignRes.SignatureData.S,
			M:                 keysignRes.SignatureData.M,
		}
		return signatureData, nil
	}
	return signatureData, errors.New(keysignRes.FailReason)

}

// 把 TSS 返回的 ECDSA 签名 (R, S, RecoveryID) 转换成 Ethereum/Bitcoin
// 等常见的 65 字节可恢复签名格式 [R || S || V]，并且确保 S 是 Low-S 格式,避免可塑性问题
func getSignatureBytes(sig *tsscommon.SignatureData) []byte {
	R := new(big.Int).SetBytes(sig.R)
	S := new(big.Int).SetBytes(sig.S)
	N := btcec.S256().N //secp256k1 曲线阶
	halfOrder := new(big.Int).Rsh(N, 1)
	// S > N/2：  S = N - S
	if S.Cmp(halfOrder) == 1 {
		S.Sub(N, S)
	}
	rBytes := R.Bytes()
	sBytes := S.Bytes()
	cBytes := sig.SignatureRecovery

	sigBytes := make([]byte, 65)
	copy(sigBytes[32-len(rBytes):32], rBytes)
	copy(sigBytes[64-len(sBytes):64], sBytes)
	copy(sigBytes[64:65], cBytes)
	return sigBytes
}

func (p *Processor) storeMessageHash(electionId uint64, messageHash string, workingNodes []string, poolPubkey string) error {
	paricipants, err := p.tssServer.GetParticipants(poolPubkey)
	if err != nil {
		return err
	}
	absentNodes := make([]string, 0)
	for _, n := range paricipants {
		if !tsscommon.ExistsIgnoreCase(workingNodes, n) {
			absentNodes = append(absentNodes, n)
		}
	}
	sbi := message.MessageHashInfo{
		MessageHash:  messageHash,
		ElectionId:   electionId,
		AbsentNodes:  absentNodes,
		WorkingNodes: workingNodes,
	}
	if err = p.nodeStore.SetMessageHash(sbi); err != nil {
		return err
	}
	return nil
}

func (p *Processor) removeWaitEvent(key string) {
	p.waitSignLock.Lock()
	defer p.waitSignLock.Unlock()
	delete(p.waitSignMsgs, key)
}
