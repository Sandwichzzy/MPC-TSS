package signer

import (
	"encoding/hex"
	"encoding/json"
	"errors"

	tsscommon "github.com/Sandwichzzy/tss/common"
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

func (p *Processor) Sign() {
	defer p.wg.Done()
	logger := p.logger.With().Str("step", "sign Message").Logger()

	logger.Info().Msg("start to sign message ")

	go func() {
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

				go func() {
					err := p.SignGo(req.ID.(tmtypes.JSONRPCStringID), nodeSignRequest, logger)
					if err != nil {
						return
					}
				}()
			}
		}
	}()
}

func (p *Processor) SignGo(resId tmtypes.JSONRPCStringID, sign tsscommon.NodeSignRequest, logger zerolog.Logger) error {
	var data []byte
	requestBody := sign.RequestBody.(tsscommon.TransactionSignRequest)
	err, hash, signByte := p.checkMessages(requestBody)
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
	}
}

// checkMessages 检查该请求是否已经在 ask 阶段被记录到 `waitSignMsgs`
func (p *Processor) checkMessages(sign tsscommon.TransactionSignRequest) (err error, hashByte, signByte []byte) {
	hashByte, err = hex.DecodeString(sign.MessageHash)
	if err != nil {
		return err, hashByte, nil
	}
	hashStr := hexutil.Encode(hashByte)

	signByte, ok := p.GetSign(hashStr)
	if ok {
		return nil, hashByte, signByte
	}

	p.waitSignLock.RLock()
	defer p.waitSignLock.RUnlock()
	_, ok = p.waitSignMsgs[hashStr]
	if !ok {
		return errors.New("sign request has the unverified state batch"), nil, nil
	}

	return nil, hashByte, nil
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
