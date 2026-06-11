package signer

import (
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/Sandwichzzy/tss/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	tmtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// Verify 处理 ask 阶段请求, 只做“是否接受进入签名流程”的确认。
// 它主要做这些事情：
//
// 1. 解析 `TransactionSignRequest`
// 2. 校验 `message_hash` 是否为空
// 3. 把消息哈希记入 `waitSignMsgs`
// 4. 回一个 `AskResponse{Result: true}`
func (p *Processor) Verify() {
	defer p.wg.Done()
	logger := p.logger.With().Str("step", "verify event").Logger()
	logger.Info().Msg("start to verify events ")

	go func() {
		defer func() {
			logger.Info().Msg("exit verify event process")
		}()
		for {
			select {
			case <-p.stopChan:
				return
			case req := <-p.askRequestChan:
				var askRequest common.TransactionSignRequest
				var RpcResponse tmtypes.RPCResponse
				if err := json.Unmarshal(req.Params, &askRequest); err != nil {
					logger.Error().Msg("failed to unmarshal ask request")
					RpcResponse = tmtypes.NewRPCErrorResponse(req.ID, 201, "failed to unmarshal", err.Error())
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				}
				if askRequest.MessageHash == "" {
					logger.Error().Msg("MessageHash must not be nil or negative")
					RpcResponse = tmtypes.NewRPCErrorResponse(req.ID, 201, "invalid askRequest", "MessageHash must not be nil or negative")
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					return
				}
				var resID = req.ID
				var size = len(askRequest.MessageHash)
				logger.Info().Msgf("stateroots size %d ", size)
				if len(askRequest.MessageHash) == 0 {
					logger.Error().Msg("stateroots size is empty")
					RpcResponse = tmtypes.NewRPCErrorResponse(req.ID, 201, "stateroots size is empty ", "do not need to sign")
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				} else {
					wg := &sync.WaitGroup{}
					wg.Add(1)

					hash, err := hex.DecodeString(askRequest.MessageHash)
					if err != nil {
						logger.Err(err).Msg("failed to conv msg to hash")
						RpcResponse = tmtypes.NewRPCErrorResponse(req.ID, 201, "failed to conv msg to hash", err.Error())
						if err := p.wsClient.SendMsg(RpcResponse); err != nil {
							logger.Error().Err(err).Msg("failed to send msg to manager")
						}
						continue
					} else {
						hashStr := hexutil.Encode(hash)
						p.UpdateWaitSignEvents(hashStr, askRequest)
					}

					askResponse := common.AskResponse{
						Result: true,
					}
					RpcResponse = tmtypes.NewRPCSuccessResponse(resID, askResponse)
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
				}
			}
		}
	}()
}

func (p *Processor) UpdateWaitSignEvents(uniqueId string, msg common.TransactionSignRequest) {
	p.waitSignLock.Lock()
	defer p.waitSignLock.Unlock()
	p.waitSignMsgs[uniqueId] = msg
}
